package test

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/internal/access"
	"github.com/memory-co/agentuse/server/internal/service"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/testdb"
	"github.com/memory-co/agentuse/server/internal/database"
)

func TestPreparationTicketBecomesOneRunCredential(t *testing.T) {
	s, u, d, a := runFixture(t)
	ctx := context.Background()
	c, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "first", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil || p.Access == nil {
		t.Fatal(p, err)
	}
	prepared, err := s.RunService.AccessForRequest(ctx, p.Access.Token)
	if err != nil || !prepared.Preparing {
		t.Fatal(prepared, err)
	}
	var runs int64
	s.DB.Model(&database.Run{}).Count(&runs)
	if runs != 0 {
		t.Fatal("preparation created run")
	}
	var stored string
	s.DB.Raw("SELECT agent_targets FROM messages WHERE id=?", p.MessageID).Row().Scan(&stored)
	if strings.Contains(stored, p.Access.Token) || strings.Contains(stored, "at1.") {
		t.Fatal("ticket leaked into dispatch")
	}
	runID := uuid.NewString()
	envelope := reportNative(t, s, d, p, "session", 1, acceptedEvent(runID, "one", p.Content))
	active, err := s.RunService.AccessForRequest(ctx, p.Access.Token)
	if err != nil || active.Preparing || active.RunID != runID {
		t.Fatal(active, err)
	}
	var original database.Run
	s.DB.Where("id=?", runID).Take(&original)
	if original.AccessTokenHash == nil || original.AccessExpiresAt == nil {
		t.Fatal("run was not bound")
	}
	if _, err = s.ConversationService.SendMessage(ctx, u, c.ID, "second", []string{a.ID}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	p2, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	reportNative(t, s, d, p2, "session", 2, acceptedEvent(runID, "two", p2.Content))
	if _, err = s.RunService.AccessForRequest(ctx, p2.Access.Token); err == nil {
		t.Fatal("supplement ticket retained preparation authority")
	}
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, envelope); err != nil {
		t.Fatal(err)
	}
	var after database.Run
	s.DB.Where("id=?", runID).Take(&after)
	if *after.AccessTokenHash != *original.AccessTokenHash || *after.AccessExpiresAt != *original.AccessExpiresAt {
		t.Fatal("supplement or replay changed authorization")
	}
	// bind_before limits preparation, not the lifetime of an already bound run.
	now := time.Unix(p.Access.BindBefore+10, 0)
	s.Now = func() time.Time { return now }
	if _, err = s.RunService.AccessForRequest(ctx, p.Access.Token); err != nil {
		t.Fatal("bound ticket expired with preparation window", err)
	}
	credential, err := s.RunService.RecoverAccess(ctx, d.ID, runID)
	if err != nil || credential.Token != p.Access.Token {
		t.Fatal("recovery changed token", credential, err)
	}
	now = time.Unix(*original.AccessExpiresAt, 0)
	if _, err = s.RunService.AccessForRequest(ctx, p.Access.Token); err == nil {
		t.Fatal("run timeout did not stop access")
	}
}

func TestTerminalAndLateAcceptanceCannotReopenPreparation(t *testing.T) {
	s, u, d, a := runFixture(t)
	ctx := context.Background()
	_, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "work", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	reportNative(t, s, d, p, "native", 1, acceptedEvent(runID, "one", p.Content))
	if _, err = s.RunService.AccessForRequest(ctx, p.Access.Token); err != nil {
		t.Fatal(err)
	}
	restore := testdb.RejectWrite(t, s.DB, "messages", "INSERT", "NEW.author_role='agent'", "forced reply failure")
	raw, _ := json.Marshal(map[string]any{"type": "result", "run_id": runID, "input_ids": []string{"one"}, "status": "completed", "output": "done"})
	e := protocol.ClientEvent{ConversationID: p.ConversationID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, SessionID: "native", Seq: 2, MessageID: p.MessageID, Event: raw}
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, e); err == nil {
		t.Fatal("result failure ignored")
	}
	if _, err = s.RunService.AccessForRequest(ctx, p.Access.Token); err != nil {
		t.Fatal("rolled back result revoked access", err)
	}
	restore()
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, e); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunService.AccessForRequest(ctx, p.Access.Token); err == nil {
		t.Fatal("terminal ticket fell back to preparation")
	}
	if _, err = s.RunService.RecoverAccess(ctx, d.ID, runID); err == nil {
		t.Fatal("terminal authorization recovered")
	}
}

func TestLateOrMissingCredentialPreservesFactsWithoutGrant(t *testing.T) {
	for _, late := range []bool{true, false} {
		t.Run(map[bool]string{true: "late", false: "missing"}[late], func(t *testing.T) {
			s, u, d, a := runFixture(t)
			ctx := context.Background()
			_, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "work", []string{a.ID}, nil, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.RunService.DispatchMessage(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			token := p.Access.Token
			if late {
				s.Now = func() time.Time { return time.Unix(p.Access.BindBefore+1, 0) }
			} else {
				p.Access = nil
			}
			id := uuid.NewString()
			reportNative(t, s, d, p, "session", 1, acceptedEvent(id, "one", "work"))
			var r database.Run
			if err = s.DB.Where("id=?", id).Take(&r).Error; err != nil || r.AccessTokenHash != nil || r.AccessExpiresAt != nil {
				t.Fatal(r, err)
			}
			if _, err = s.RunService.AccessForRequest(ctx, token); err == nil {
				t.Fatal("unbound authorization fell back to preparation")
			}
			if _, err = s.RunService.RecoverAccess(ctx, d.ID, id); err == nil {
				t.Fatal("recovery minted missing authorization")
			}
			reportNative(t, s, d, p, "session", 2, map[string]any{"type": "result", "run_id": id, "input_ids": []string{"one"}, "status": "completed", "output": "done"})
		})
	}
}

func TestAccessBindingRollsBackWithAcceptedAndRecoversAcrossInstances(t *testing.T) {
	s, u, d, a := runFixture(t)
	ctx := context.Background()
	directory := t.TempDir()
	if err := s.ConfigureSecurity(service.SecurityConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "work", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	raw, _ := json.Marshal(acceptedEvent(id, "one", "work"))
	event := protocol.ClientEvent{AccessToken: p.Access.Token, ConversationID: p.ConversationID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, MessageID: p.MessageID, SessionID: "session", Seq: 1, Event: raw}
	restore := testdb.RejectWrite(t, s.DB, "run_inputs", "INSERT", "NEW.input_id='one'", "reject accepted")
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, event); err == nil {
		t.Fatal("accepted transaction unexpectedly committed")
	}
	var count int64
	s.DB.Model(&database.Run{}).Where("id=?", id).Count(&count)
	if count != 0 {
		t.Fatal("rolled back accepted left run/grant")
	}
	restore()
	if err = s.ConversationService.ReportClientEvent(ctx, d.ID, event); err != nil {
		t.Fatal(err)
	}
	other := service.New(s.Database)
	defer other.UpgradeService.Close()
	other.Now = s.Now
	if err = other.ConfigureSecurity(service.SecurityConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	grant, err := other.RunService.RecoverAccess(ctx, d.ID, id)
	if err != nil || grant.Token != p.Access.Token {
		t.Fatal("persistent keys failed across instances", grant, err)
	}
	if _, err = s.DeviceService.ResetDeviceSetup(ctx, u, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = other.RunService.AccessForRequest(ctx, p.Access.Token); err == nil {
		t.Fatal("other instance ignored revoked generation")
	}
	if _, err = other.RunService.RecoverAccess(ctx, d.ID, id); err == nil {
		t.Fatal("reset authorization was recovered")
	}
}

func TestDerivedRoundNotificationKeepsAuthorizationCeiling(t *testing.T) {
	s, u, d, a := runFixture(t)
	ctx := context.Background()
	parent, _, err := s.ConversationService.CreateConversation(ctx, u, "", "", "coordinate", []string{a.ID}, nil, "collaborative", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	parentRun := uuid.NewString()
	reportNative(t, s, d, p, "parent", 1, acceptedEvent(parentRun, "one", p.Content))
	principal, err := s.RunService.AccessForRequest(ctx, p.Access.Token)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.RunService.ExecutionSource(ctx, d.ID, parent.ID, a.ID, "parent", parentRun)
	if err != nil {
		t.Fatal(err)
	}
	source.User = u
	authorized := access.WithCredential(access.WithLimit(ctx, principal.ExpiresAt), p.Access.Token)
	_, _, err = s.ConversationService.CreateConversation(authorized, u, parent.ID, "child", "execute", []string{a.ID}, nil, "single", &source)
	if err != nil {
		t.Fatal(err)
	}
	childInput, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.RunService.AccessSigner.Verify(childInput.Access.Token)
	if err != nil || claims.AccessLimit > principal.ExpiresAt {
		t.Fatal(claims, err)
	}
	childRun := uuid.NewString()
	reportNative(t, s, d, childInput, "child", 1, acceptedEvent(childRun, "two", childInput.Content))
	reportNative(t, s, d, p, "parent", 2, map[string]any{"type": "result", "run_id": parentRun, "input_ids": []string{"one"}, "status": "completed", "output": "delegated"})
	reportNative(t, s, d, childInput, "child", 2, map[string]any{"type": "result", "run_id": childRun, "input_ids": []string{"two"}, "status": "completed", "output": "done"})
	notice, err := s.RunService.DispatchMessage(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	claims, err = s.RunService.AccessSigner.Verify(notice.Access.Token)
	if err != nil || notice.ConversationID != parent.ID || claims.AccessLimit > principal.ExpiresAt {
		t.Fatal("round notification extended root access", claims, err)
	}
}
