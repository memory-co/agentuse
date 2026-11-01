package harness

import (
	"github.com/memory-co/agentuse/internal/protocol"
	"sync"
	"time"
)

const DefaultAskTimeout = 30 * time.Minute

type control struct {
	steer  *protocol.Steer
	answer *protocol.Answer
}

// Input contains only AgentUse business controls; native queues and RPC live in harnessctl.
type Input struct {
	mu      sync.Mutex
	items   []control
	seen    map[int64]bool
	closed  bool
	timeout time.Duration
}

func NewInput(timeout time.Duration) *Input {
	if timeout <= 0 {
		timeout = DefaultAskTimeout
	}
	return &Input{seen: map[int64]bool{}, timeout: timeout}
}
func (in *Input) Steer(v protocol.Steer) bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed || in.seen[v.ID] && v.ID > 0 {
		return false
	}
	if v.ID > 0 {
		in.seen[v.ID] = true
	}
	in.items = append(in.items, control{steer: &v})
	return true
}
func (in *Input) Answer(v protocol.Answer) bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return false
	}
	in.items = append(in.items, control{answer: &v})
	return true
}
func (in *Input) take() []control {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	out := in.items
	in.items = nil
	return out
}
func (in *Input) finish() bool {
	if in == nil {
		return true
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.items) > 0 {
		return false
	}
	in.closed = true
	return true
}
