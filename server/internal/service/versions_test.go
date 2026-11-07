package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/file"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

func skillZIP(t *testing.T, name, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("SKILL.md")
	fmt.Fprintf(f, "---\nname: %s\ndescription: Test skill\n---\n%s", name, body)
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestVersionsPinQueuedInputAndPreserveHistoricalResources(t *testing.T) {
	s, u, d, a := runStore(t)
	ctx := context.Background()
	files, e := file.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureFiles(files)
	v1, e := s.Skills.Publish(ctx, u, "", "", bytes.NewReader(skillZIP(t, "review", "first")))
	if e != nil {
		t.Fatal(e)
	}
	config := agentuse.AgentConfig{Name: a.Name, DaemonID: d.ID, Harness: a.Harness, Instructions: "Agent v2", Model: "model-one", Skills: []agentuse.SkillRef{{SkillID: v1.ID, SkillVersionID: v1.CurrentVersionID}}}
	a, e = s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, config)
	if e != nil {
		t.Fatal(e)
	}
	another, e := s.CreateConfiguredAgent(ctx, u.ID, "reviewer", agentuse.AgentConfig{Name: "Reviewer", DaemonID: d.ID, Harness: a.Harness, Skills: config.Skills})
	if e != nil {
		t.Fatal(e)
	}
	ss, _, e := s.CreateMultiSession(ctx, u, "", "", json.RawMessage(`{"prompt":"first"}`), []string{a.ID}, "")
	if e != nil {
		t.Fatal(e)
	}
	v2, e := s.Skills.Publish(ctx, u, v1.ID, v1.CurrentVersionID, bytes.NewReader(skillZIP(t, "review", "second")))
	if e != nil {
		t.Fatal(e)
	}
	config.Instructions = "Agent v3"
	config.Skills = []agentuse.SkillRef{{SkillID: v2.ID, SkillVersionID: v2.CurrentVersionID}}
	next, e := s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, config)
	if e != nil || next.Version != 3 {
		t.Fatal(next, e)
	}
	if _, e = s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, config); !errors.Is(e, skillservice.ErrConflict) {
		t.Fatal("stale base accepted", e)
	}
	unchanged, e := s.PublishAgentVersion(ctx, u, a.ID, next.CurrentVersionID, config)
	if e != nil || unchanged.CurrentVersionID != next.CurrentVersionID {
		t.Fatal("no-op published a version", e)
	}
	p, e := s.DispatchMessage(ctx, d)
	if e != nil || p.AgentVersionID != a.CurrentVersionID {
		t.Fatalf("queued input changed: %+v %v", p, e)
	}
	fixed, err := s.GetAgentVersion(ctx, u, p.AgentID, p.AgentVersionID)
	if err != nil || fixed.Instructions != "Agent v2" || fixed.Model != "model-one" || len(fixed.Skills) != 1 || fixed.Skills[0].SkillVersionID != v1.CurrentVersionID {
		t.Fatal("fixed configuration changed", fixed, err)
	}
	frame, _ := json.Marshal(protocol.DeliveryFrame(p))
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(frame, &fields)
	for _, key := range []string{"skills", "model", "config", "agent_instructions", "prompt_id", "task_id"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("inline configuration/identity retained: %s", key)
		}
	}
	runID := uuid.NewString()
	reportNative(t, s, d, p, "native-v2", 1, acceptedEvent(runID, "input1", p.Content))
	if _, e = s.SendMultiMessage(ctx, u, ss.ID, json.RawMessage(`{"prompt":"next"}`), []string{a.ID}, ""); e != nil {
		t.Fatal(e)
	}
	p2, e := s.DispatchMessage(ctx, d)
	if e != nil || p2.AgentVersionID != next.CurrentVersionID {
		t.Fatal(p2, e)
	}
	bad := acceptedEvent(uuid.NewString(), "input2", p2.Content)
	raw, _ := json.Marshal(bad)
	envelope := protocol.ClientEvent{ConversationID: p2.ConversationID, AgentID: p2.AgentID, AgentVersionID: p2.AgentVersionID, Revision: "client-revision", SessionID: "native-v2", Seq: 2, MessageID: p2.MessageID, Event: raw}
	if e = s.ReportClientEvent(ctx, d.ID, envelope); e == nil {
		t.Fatal("cross-version native session reuse accepted")
	}
	if e = s.Skills.Delete(ctx, u, v1.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Skills.Version(ctx, s.db, u, v1.ID, v1.CurrentVersionID, false); e != nil {
		t.Fatal("historical package disappeared", e)
	}
	if _, e = s.PublishAgentVersion(ctx, u, another.ID, another.CurrentVersionID, agentuse.AgentConfig{Name: "Renamed", DaemonID: d.ID, Harness: a.Harness, Skills: []agentuse.SkillRef{{SkillID: v1.ID, SkillVersionID: v1.CurrentVersionID}}}); e == nil {
		t.Fatal("deleted skill selected for new publication")
	}
	if e = s.DeleteAgent(ctx, u, a.ID); e != nil {
		t.Fatal(e)
	}
	old, e := s.GetAgentVersion(ctx, u, a.ID, a.CurrentVersionID)
	if e != nil || old.Instructions != "Agent v2" {
		t.Fatal(old, e)
	}
	if _, e = s.GetAgent(ctx, u, a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("soft-deleted agent remains selectable", e)
	}
}

func TestFailedSkillPublicationRemovesCandidateObject(t *testing.T) {
	s, u, _, _ := runStore(t)
	ctx := context.Background()
	files, e := file.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureFiles(files)
	if _, e = s.db.Exec(`CREATE TRIGGER fail_skill BEFORE INSERT ON skill_versions BEGIN SELECT RAISE(ABORT,'forced publication failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Skills.Publish(ctx, u, "", "", bytes.NewReader(skillZIP(t, "test", "body"))); e == nil {
		t.Fatal("database failure ignored")
	}
	rows, e := files.List(ctx, "skills", "")
	if e != nil || len(rows) != 0 {
		t.Fatal("unpublished package leaked", rows, e)
	}
	var count int
	if e = s.db.QueryRow(`SELECT count(*) FROM skills`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestConcurrentSkillPublicationHasOneWinner(t *testing.T) {
	s, u, _, _ := runStore(t)
	ctx := context.Background()
	files, e := file.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureFiles(files)
	first, e := s.Skills.Publish(ctx, u, "", "", bytes.NewReader(skillZIP(t, "review", "v1")))
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, body := range []string{"v2a", "v2b"} {
		data := skillZIP(t, "review", body)
		go func() {
			<-start
			_, err := s.Skills.Publish(ctx, u, first.ID, first.CurrentVersionID, bytes.NewReader(data))
			results <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else if errors.Is(e, skillservice.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	rows, e := files.List(ctx, "skills", first.ID)
	if e != nil || len(rows) != 2 {
		t.Fatal("losing upload leaked", rows, e)
	}
	history, e := s.Skills.Versions(ctx, u, first.ID)
	if e != nil || len(history) != 2 || history[0].Version != 2 {
		t.Fatal(history, e)
	}
}
