package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/attachment"
	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
)

func TestDeleteWorkRollsBackAllOwnersBeforeRemovingFiles(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	files, err := fileservice.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureFiles(files)
	s.ConfigureConversationAttachments(1024, 4096, nil)
	w, coordinator, err := s.WorkService.CreateWork(ctx, u, "Work", a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConversationService.SendMessage(ctx, u, coordinator.ID, "coordinate", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "coordinator", 1, acceptedEvent(run, "one", delivery.Content))
	if _, err = s.WorkService.AuthorizeWorkTool(ctx, d.ID, coordinator.ID, "", a.ID, run); err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, d, delivery, "coordinator", 2, map[string]any{"type": "request", "run_id": run, "request_id": "question", "state": "pending"})
	if _, err = s.RunService.CancelRun(ctx, u, coordinator.ID, run); err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, d, delivery, "coordinator", 3, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "ready"})
	child, _, err := s.ConversationService.CreateConversation(ctx, u, w.ID, "Child", "task", []string{a.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.UserService.ResetUserToken(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "input.txt")
	if err = os.WriteFile(temp, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := attachment.Attachment{ID: s.NewAttachmentID(), ConversationID: child.ID, Filename: "input.txt", RelativePath: "user_input/input.txt", ContentType: "text/plain", SizeBytes: 5, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("input"))), Source: "user", CreatedBy: u.ID, CreatedAt: s.unix()}
	if err = s.ConversationService.PublishAttachment(ctx, AttachmentAccess{User: u, Token: token}, &metadata, temp); err != nil {
		t.Fatal(err)
	}
	// Complete the child and its resulting coordinator summary, exercising all
	// execution tables and Work's durable summary marker through business APIs.
	for _, session := range []string{"child", "summary"} {
		delivery, err = s.RunService.DispatchMessage(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		run = uuid.NewString()
		reportNative(t, s, d, delivery, session, 1, acceptedEvent(run, "one", delivery.Content))
		reportNative(t, s, d, delivery, session, 2, map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"one"}, "output": "done"})
	}
	if err = files.Put(ctx, "skills", "retained/skill.zip", strings.NewReader("keep"), true); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, table := range []string{"works", "conversations", "messages", "attachments", "message_agents", "runs", "run_events", "run_controls", "run_requests", "work_run_tokens", "run_notifications"} {
		var count int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count == 0 {
			t.Fatalf("fixture did not exercise %s: %d %v", table, count, err)
		}
		counts[table] = count
	}
	sub, unsubscribe := s.BrowserChanges.Subscribe(u)
	defer unsubscribe()
	if _, err = s.db.Exec(`CREATE TRIGGER reject_work_delete BEFORE DELETE ON works BEGIN SELECT RAISE(ABORT,'forced work cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.WorkService.DeleteWork(ctx, u, w.ID); err == nil {
		t.Fatal("work deletion ignored the final owner's failure")
	}
	for table, want := range counts {
		var count int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s cleanup escaped rollback: got %d, want %d (%v)", table, count, want, err)
		}
	}
	stream, err := s.ConversationService.OpenAttachmentFile(ctx, child.ID, metadata.ID)
	if err != nil {
		t.Fatal("rollback removed published bytes", err)
	}
	stream.Close()
	if changes := sub.Drain(); len(changes) != 0 {
		t.Fatal("rolled-back work deletion published changes", changes)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_work_delete`); err != nil {
		t.Fatal(err)
	}
	if err = s.WorkService.DeleteWork(ctx, u, w.ID); err != nil {
		t.Fatal(err)
	}
	for table := range counts {
		var count int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s left business rows after deletion: %d %v", table, count, err)
		}
	}
	if _, _, err = files.Get(ctx, conversationFileNamespace, child.ID+"/"+metadata.RelativePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("committed deletion retained attachment bytes", err)
	}
	stream, _, err = files.Get(ctx, "skills", "retained/skill.zip")
	if err != nil {
		t.Fatal("conversation cleanup removed unrelated skill bytes", err)
	}
	stream.Close()
	if _, err = s.AgentService.GetAgent(ctx, u, a.ID); err != nil {
		t.Fatal("work deletion removed its agent", err)
	}
}

func TestDeleteAgentRollsBackWhenWorkGrantRevocationFails(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	w, coordinator, err := s.CreateWork(ctx, u, "Work", a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, u, coordinator.ID, "coordinate", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	reportNative(t, s, d, delivery, "coordinator", 1, acceptedEvent(run, "one", delivery.Content))
	token, err := s.AuthorizeWorkTool(ctx, d.ID, coordinator.ID, "", a.ID, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_work_revocation BEFORE UPDATE ON works BEGIN SELECT RAISE(ABORT,'forced grant revocation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.AgentService.DeleteAgent(ctx, u, a.ID); err == nil {
		t.Fatal("agent deletion ignored Work failure")
	}
	if _, err = s.AgentService.GetAgent(ctx, u, a.ID); err != nil {
		t.Fatal("failed revocation deleted the agent", err)
	}
	current, _, err := s.WorkService.WorkByToken(ctx, token)
	if err != nil || current.ID != w.ID || current.CoordinatorAgentID != a.ID {
		t.Fatal("failed deletion revoked the coordinator grant", current, err)
	}
	// The original delivery is still accepted; no synthetic execution result.
	actual, err := s.GetRun(ctx, u, coordinator.ID, run)
	if err != nil || actual.Status != "running" || actual.ResultMessageID != nil {
		t.Fatal("failed agent deletion changed execution", actual, err)
	}
}
