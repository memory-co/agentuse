package run

import (
	"context"
	"encoding/json"
	"time"

	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
)

func (d *endpointConnection) recordSteer(s protocol.Steer) {
	if d.deliveries != nil {
		d.writeDelivery(func(ctx context.Context) error { return d.deliveries.Steer(ctx, s.RunID, s.AttemptID, s.ID) })
	}
}
func (d *endpointConnection) writeDelivery(write func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := write(ctx); err != nil {
		d.log.Printf("本机账本: %v", err)
	}
}

func (d *endpointConnection) recordReceivedLocal(m protocol.Dispatch, s session.Session, dir string) {
	var input protocol.PromptInput
	_ = json.Unmarshal(m.Input, &input)
	d.writeDelivery(func(ctx context.Context) error {
		return d.deliveries.Receive(ctx, Delivery{EndpointID: d.cfg.EndpointID, ConversationID: m.SessionID, RunID: m.RunID, AttemptID: m.AttemptID, Server: d.cfg.Server, DaemonID: d.cfg.DaemonID, Sender: m.Sender, AgentID: m.AgentID, SessionID: s.ID, WorkID: m.WorkID, Harness: m.Harness, HarnessRevision: s.HarnessRevision, Summary: input.Prompt, Directory: dir})
	})
}
func (d *endpointConnection) recordCompleteLocal(m protocol.Dispatch, s session.Session, r harness.DriveResult, known bool, canceled error) {
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
	d.writeDelivery(func(ctx context.Context) error {
		return d.deliveries.Complete(ctx, m.RunID, m.AttemptID, DeliveryFinish{Status: status, Error: r.Error, AttachmentCount: len(m.Attachments), HarnessRunIDs: r.RunIDs})
	})
}
