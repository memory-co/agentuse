package run

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"time"
)

func (d *endpointConnection) ticket(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a := d.tickets[key]; a != nil {
		return a.Token
	}
	return ""
}
func (d *endpointConnection) runAccess(ctx context.Context, run string) (string, error) {
	d.mu.Lock()
	if a := d.grants[run]; a != nil {
		if a.ExpiresAt > time.Now().Unix() {
			token := a.Token
			d.mu.Unlock()
			return token, nil
		}
		delete(d.grants, run)
	}
	id := "a:" + uuid.NewString()
	ch := make(chan protocol.Message, 1)
	d.accessPending[id] = ch
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.accessPending, id); d.mu.Unlock() }()
	if err := d.send(ctx, protocol.Message{Type: protocol.TypeAccess, ID: id, RunID: run}); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case m := <-ch:
		if m.Status != protocol.AckOK || m.Access == nil {
			return "", errors.New("execution access unavailable")
		}
		return m.Access.Token, nil
	}
}
