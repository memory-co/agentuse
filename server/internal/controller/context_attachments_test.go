package controller

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestContextOutputAttributionFilteringAndUploadLifetime(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	user := sdk.New(f.e.ts.URL, f.token)
	device := sdk.New(f.e.ts.URL, f.key)
	if _, err := user.UploadAttachment(ctx, "", f.session.ID, "input.txt", strings.NewReader("input")); err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	report := func(p protocol.ConversationMessage, event any) {
		t.Helper()
		seq++
		data, _ := json.Marshal(event)
		err := f.e.st.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{ConversationID: f.session.ID, SessionID: "native", AgentID: f.agent, AgentVersionID: p.AgentVersionID, Revision: "test", MessageID: p.MessageID, Seq: seq, Event: data})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := func(prompt, input string) (protocol.ConversationMessage, string) {
		t.Helper()
		_, err := f.e.st.SendMultiMessage(ctx, f.u, f.session.ID, json.RawMessage(`{"prompt":"`+prompt+`"}`), []string{f.agent}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.e.st.DispatchMessage(ctx, f.d)
		if err != nil {
			t.Fatal(err)
		}
		run := uuid.NewString()
		report(p, map[string]any{"type": "accepted", "run_id": run, "input_id": input, "text": prompt})
		return p, run
	}
	first, one := start("first", "one")
	second, two := start("queued", "two")
	for _, run := range []string{"", uuid.NewString()} {
		device.RunID = run
		if _, err := device.UploadAttachment(ctx, "", f.session.ID, "bad.txt", strings.NewReader("invalid")); !sdk.IsStatus(err, 409) {
			t.Fatal("unattributed output accepted", err)
		}
	}
	device.RunID = one
	for i := 0; i < 2; i++ {
		output, err := device.UploadAttachment(ctx, "", f.session.ID, "report.txt", strings.NewReader("output"))
		if err != nil || output.CreatedByRunID != one || output.CreatedByAgentKey != f.agent || output.Source != "daemon" {
			t.Fatal(output, err)
		}
	}
	device.RunID = two
	if _, err := device.UploadAttachment(ctx, "", f.session.ID, "other.txt", strings.NewReader("other run")); err != nil {
		t.Fatal(err)
	}
	if _, err := user.UploadAttachment(ctx, "", f.session.ID, "future.txt", strings.NewReader("new input")); err != nil {
		t.Fatal(err)
	}
	page, err := device.ListRunAttachments(ctx, "", f.session.ID, one, 1, "")
	if err != nil || len(page.Attachments) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	next, err := device.ListRunAttachments(ctx, "", f.session.ID, one, 1, *page.NextCursor)
	if err != nil || len(next.Attachments) != 1 || next.NextCursor != nil || next.Attachments[0].ID == page.Attachments[0].ID {
		t.Fatal(next, err)
	}
	if _, err := device.ListRunAttachments(ctx, "", f.session.ID, two, 1, *page.NextCursor); !sdk.IsStatus(err, 400) {
		t.Fatal("cross-run cursor accepted", err)
	}
	outputs, err := device.AllRunAttachments(ctx, "", f.session.ID, two)
	if err != nil || len(outputs) != 1 || outputs[0].CreatedByRunID != two {
		t.Fatal(outputs, err)
	}
	report(first, map[string]any{"type": "result", "run_id": one, "status": "completed", "input_ids": []string{"one"}, "output": "done"})
	device.RunID = one
	if _, err := device.UploadAttachment(ctx, "", f.session.ID, "late.txt", strings.NewReader("late")); !sdk.IsStatus(err, 409) {
		t.Fatal("terminal run uploaded", err)
	}
	if outputs, err = device.AllRunAttachments(ctx, "", f.session.ID, one); err != nil || len(outputs) != 2 {
		t.Fatal("historical outputs unreadable", outputs, err)
	}
	foreign, _, err := f.e.st.CreateMultiSession(ctx, f.u, "", "elsewhere", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := user.ListRunAttachments(ctx, "", foreign.ID, one, 100, ""); !sdk.IsStatus(err, 404) {
		t.Fatal("run queried in another conversation", err)
	}

	// End run two after the body has started, but before publishing its file.
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	req, _ := http.NewRequest("POST", f.e.ts.URL+f.path(), reader)
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("X-Agentuse-Run", two)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	done := make(chan int, 1)
	go func() {
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- 0
			return
		}
		response.Body.Close()
		done <- response.StatusCode
	}()
	part, err := multipartWriter.CreateFormFile("file", "racing.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(strings.Repeat("x", 512))); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ready := false
		entries, _ := os.ReadDir(filepath.Join(f.e.srv.cfg.AttachmentsDir, ".tmp"))
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && info.Size() >= 512 {
				ready = true
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload body did not reach the Server")
		}
		time.Sleep(10 * time.Millisecond)
	}
	report(second, map[string]any{"type": "result", "run_id": two, "status": "completed", "input_ids": []string{"two"}, "output": "done"})
	multipartWriter.Close()
	writer.Close()
	if status := <-done; status != 409 {
		t.Fatal("upload did not recheck run at commit", status)
	}
	files, err := user.AllAttachments(ctx, "", f.session.ID)
	if err != nil || len(files) != 5 {
		t.Fatal("failed upload published a file", files, err)
	}
}

func TestDeviceReadsItsOutputsAfterAnyTerminalResult(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			fixture := attachmentEnv(t, 4096, 16384)
			ctx := context.Background()
			_, err := fixture.e.st.SendMultiMessage(ctx, fixture.u, fixture.session.ID, json.RawMessage(`{"prompt":"output"}`), []string{fixture.agent}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			message, err := fixture.e.st.DispatchMessage(ctx, fixture.d)
			if err != nil {
				t.Fatal(err)
			}
			run := uuid.NewString()
			send := func(seq int64, event any) {
				data, _ := json.Marshal(event)
				err := fixture.e.st.ReportClientEvent(ctx, fixture.d.ID, protocol.ClientEvent{ConversationID: fixture.session.ID, SessionID: "native", AgentID: fixture.agent, AgentVersionID: message.AgentVersionID, Revision: "test", MessageID: message.MessageID, Seq: seq, Event: data})
				if err != nil {
					t.Fatal(err)
				}
			}
			send(1, map[string]any{"type": "accepted", "run_id": run, "input_id": "one", "text": "output"})
			client := sdk.New(fixture.e.ts.URL, fixture.key)
			client.RunID = run
			if _, err := client.UploadAttachment(ctx, "", fixture.session.ID, "output.txt", strings.NewReader("bytes")); err != nil {
				t.Fatal(err)
			}
			send(2, map[string]any{"type": "result", "run_id": run, "status": status, "input_ids": []string{"one"}, "output": "done", "error": ""})
			files, err := client.AllRunAttachments(ctx, "", fixture.session.ID, run)
			if err != nil || len(files) != 1 {
				t.Fatal("terminal output unreadable", files, err)
			}
		})
	}
}
