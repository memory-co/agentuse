package run

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
	"time"
)

func (d *endpointConnection) recordSteer(s protocol.Steer) {
	if d.ledger != nil {
		d.ledgerWrite(func(ctx context.Context) error { return d.ledger.Steer(ctx, s.RunID, s.AttemptID, s.ID) })
	}
}
func (d *endpointConnection) ledgerWrite(write func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := write(ctx); err != nil {
		d.log.Printf("本机账本: %v", err)
	}
}

func (d *endpointConnection) recordReceivedLocal(m protocol.Dispatch, s store.Session, dir string) {
	var input protocol.PromptInput
	_ = json.Unmarshal(m.Input, &input)
	d.ledgerWrite(func(ctx context.Context) error {
		return d.ledger.Receive(ctx, store.Run{EndpointID: d.cfg.EndpointID, ConversationID: m.SessionID, RunID: m.RunID, AttemptID: m.AttemptID, Server: d.cfg.Server, DaemonID: d.cfg.DaemonID, Sender: m.Sender, AgentID: m.AgentID, SessionID: s.ID, WorkID: m.WorkID, Harness: m.Harness, HarnessRevision: s.HarnessRevision, Summary: input.Prompt, Directory: dir})
	})
}
func (d *endpointConnection) recordCompleteLocal(m protocol.Dispatch, s store.Session, r harness.Result, known bool, canceled error) {
	status := "succeeded"
	if !r.OK {
		status = "failed"
	}
	if !known {
		status = "rejected"
	}
	if canceled != nil {
		status = "cancelled"
		r.Error = canceled.Error()
	}
	d.ledgerWrite(func(ctx context.Context) error {
		return d.ledger.Complete(ctx, m.RunID, m.AttemptID, store.Finish{Status: status, Error: r.Error, Output: r.Output, AttachmentCount: len(m.Attachments), Usage: r.Usage, CostUSD: r.CostUSD})
	})
}
