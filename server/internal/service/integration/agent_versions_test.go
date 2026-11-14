package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
	"github.com/memory-co/agentuse/server/internal/service/file"
	skillservice "github.com/memory-co/agentuse/server/internal/service/skill"
)

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
	config := agentuse.AgentConfig{Name: a.Name, DeviceID: d.ID, Harness: a.Harness, Instructions: "Agent v2", Model: "model-one", Skills: []agentuse.SkillRef{{SkillID: v1.ID, SkillVersionID: v1.CurrentVersionID}}}
	a, e = s.PublishAgentVersion(ctx, u, a.ID, a.CurrentVersionID, config)
	if e != nil {
		t.Fatal(e)
	}
	another, e := s.CreateConfiguredAgent(ctx, u.ID, "reviewer", agentuse.AgentConfig{Name: "Reviewer", DeviceID: d.ID, Harness: a.Harness, Skills: config.Skills})
	if e != nil {
		t.Fatal(e)
	}
	ss, _, e := s.CreateConversation(ctx, u, "", "", "first", []string{a.ID}, nil)
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
	if _, e = s.SendMessage(ctx, u, ss.ID, "next", []string{a.ID}, nil, nil, ""); e != nil {
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
	if _, e = s.Skills.Version(ctx, s.DB, u, v1.ID, v1.CurrentVersionID, false); e != nil {
		t.Fatal("historical package disappeared", e)
	}
	if _, e = s.PublishAgentVersion(ctx, u, another.ID, another.CurrentVersionID, agentuse.AgentConfig{Name: "Renamed", DeviceID: d.ID, Harness: a.Harness, Skills: []agentuse.SkillRef{{SkillID: v1.ID, SkillVersionID: v1.CurrentVersionID}}}); e == nil {
		t.Fatal("deleted skill selected for new publication")
	}
	if e = s.DeleteAgent(ctx, u, a.ID); e != nil {
		t.Fatal(e)
	}
	old, e := s.GetAgentVersion(ctx, u, a.ID, a.CurrentVersionID)
	if e != nil || old.Instructions != "Agent v2" {
		t.Fatal(old, e)
	}
	if _, e = s.GetAgent(ctx, u, a.ID); !errors.Is(e, service.ErrNotFound) {
		t.Fatal("soft-deleted agent remains selectable", e)
	}
}
