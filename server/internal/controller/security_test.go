package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/securews"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

func TestPreparationScopeAndCertificateRevocation(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	_, err := f.e.st.ConversationService.SendMessage(ctx, f.u, f.conversation.ID, "work", []string{f.agent}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.st.RunService.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	c := sdk.New(f.e.ts.URL, p.Access.Token)
	view, err := c.GetConversation(ctx, "", f.conversation.ID)
	if err != nil || len(view.Messages) != 0 || len(view.Runs) != 0 || view.Conversation.ID != f.conversation.ID {
		t.Fatal("preparation exposed history", view, err)
	}
	for _, path := range []string{f.path(), "/api/conversations/" + f.conversation.ID + "?round=1", "/api/agents", "/api/devices"} {
		code := f.e.code(p.Access.Token, "GET", path, nil)
		if code != 401 && code != 403 {
			t.Fatal("preparation scope escaped", path, code)
		}
	}
	if code := f.e.req(p.Access.Token, "GET", "/api/agents/"+f.agent+"/versions/"+p.AgentVersionID, nil, nil, actAsHeader, f.u.ID).StatusCode; code != 403 {
		t.Fatal("impersonation permitted", code)
	}
	if code := f.e.code(p.Access.Token[:len(p.Access.Token)-2]+"xx", "GET", "/api/agents/"+f.agent+"/versions/"+p.AgentVersionID, nil); code != 403 {
		t.Fatal("tampered signature accepted", code)
	}
	peer, err := securews.VerifyDevice(f.credentials.CA, f.credentials.Certificate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other, csr, err := securews.NewCredentials(f.credentials.CA)
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	if _, err = f.e.st.DeviceService.RenewCertificate(ctx, peer, csr); err == nil {
		t.Fatal("renewal changed device private key")
	}
	csr, err = f.credentials.CSR()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := f.e.st.DeviceService.RenewCertificate(ctx, peer, csr)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := securews.VerifyDevice(f.credentials.CA, cert, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Lost renewal response does not invalidate the original credential.
	if _, err = f.e.st.DeviceService.AuthenticateCertificate(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if _, err = f.e.st.DeviceService.AuthenticateCertificate(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if _, err = f.e.st.DeviceService.ResetDeviceSetup(ctx, f.u, f.d.ID); err != nil {
		t.Fatal(err)
	}
	for _, cert := range []securews.Peer{peer, renewed} {
		if _, err = f.e.st.DeviceService.AuthenticateCertificate(ctx, cert); err == nil {
			t.Fatal("reset certificate stayed valid")
		}
	}
	if _, err = f.e.st.RunService.AccessForRequest(ctx, p.Access.Token); err == nil {
		t.Fatal("reset ticket stayed valid")
	}
}

func TestLongDownloadStopsWhenRunEnds(t *testing.T) {
	f := attachmentEnv(t, 4096, 16384)
	ctx := context.Background()
	_, err := f.e.st.ConversationService.SendMessage(ctx, f.u, f.conversation.ID, "work", []string{f.agent}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.st.RunService.DispatchMessage(ctx, f.d)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	report := func(seq int64, event any) {
		raw, _ := json.Marshal(event)
		if err := f.e.st.ConversationService.ReportClientEvent(ctx, f.d.ID, protocol.ClientEvent{AccessToken: p.Access.Token, ConversationID: p.ConversationID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, MessageID: p.MessageID, SessionID: "native", Seq: seq, Event: raw}); err != nil {
			t.Fatal(err)
		}
	}
	report(1, map[string]any{"type": "accepted", "run_id": id, "input_id": "one", "text": "work"})
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest(http.MethodGet, "/content", nil)
	request.Header.Set("Authorization", "Bearer "+p.Access.Token)
	done := make(chan struct{})
	wrote := make(chan struct{}, 1)
	go func() {
		defer close(done)
		defer reader.Close()
		_, _ = f.e.srv.copyResource(notifyingWriter{ResponseWriter: httptest.NewRecorder(), wrote: wrote}, request, reader)
	}()
	if _, err = io.Copy(writer, strings.NewReader("first bytes")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wrote:
	case <-time.After(3 * time.Second):
		t.Fatal("first stream bytes not sent")
	}
	report(2, map[string]any{"type": "result", "run_id": id, "input_ids": []string{"one"}, "status": "completed", "output": "done"})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked stream kept blocked content source open")
	}
	if _, err = writer.Write([]byte("after terminal")); err == nil {
		t.Fatal("stream accepted bytes after authorization ended")
	}
}

type notifyingWriter struct {
	http.ResponseWriter
	wrote chan struct{}
}

func (w notifyingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	select {
	case w.wrote <- struct{}{}:
	default:
	}
	return n, err
}
