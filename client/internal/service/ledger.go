package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
)

func (d *Daemon) recordReceived(m protocol.Dispatch) {
	if d.ledger == nil {
		return
	}
	var input protocol.PromptInput
	_ = json.Unmarshal(m.Input, &input)
	if input.Prompt == "" {
		input.Prompt = string(m.Input)
	}
	directory := ""
	if m.SessionID != "" && filepath.Base(m.SessionID) == m.SessionID {
		directory = filepath.Join(d.workdir, m.SessionID)
	}
	d.ledgerWrite(func(ctx context.Context) error {
		return d.ledger.Receive(ctx, store.Run{RunID: m.RunID, AttemptID: m.AttemptID, Server: d.cfg.Server, DaemonID: d.cfg.DaemonID, Sender: m.Sender, AgentID: m.AgentID, SessionID: m.SessionID, WorkID: m.WorkID, Harness: m.Harness, Summary: input.Prompt, Directory: directory})
	})
}
func (d *Daemon) recordSteer(s protocol.Steer) {
	if d.ledger != nil {
		d.ledgerWrite(func(ctx context.Context) error { return d.ledger.Steer(ctx, s.RunID, s.AttemptID, s.ID) })
	}
}
func (d *Daemon) recordComplete(m protocol.Dispatch, r Result, known bool, canceled error) {
	if d.ledger == nil {
		return
	}
	status := "done"
	if !r.OK {
		status = "failed"
	}
	if !known {
		status = "rejected"
	}
	if canceled != nil {
		status = "canceled"
		r.Error = canceled.Error()
	}
	d.ledgerWrite(func(ctx context.Context) error {
		return d.ledger.Complete(ctx, m.RunID, m.AttemptID, store.Finish{Status: status, Error: r.Error, AttachmentCount: len(m.Attachments), Usage: r.Usage, CostUSD: r.CostUSD})
	})
}
func (d *Daemon) ledgerWrite(write func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := write(ctx); err != nil {
		d.log.Printf("本机账本: %v", err)
	}
}
