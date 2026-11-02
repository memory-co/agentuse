package harness

import (
	"sync"
	"time"
)

const DefaultAskTimeout = 30 * time.Minute

type Acceptance struct {
	RunID   string `json:"run_id"`
	InputID string `json:"input_id"`
}
type queuedSend struct {
	Text     string
	Accepted func(Acceptance)
}
type queuedReply struct {
	RequestID, Body string
	Done            func(error)
}
type control struct {
	send  *queuedSend
	reply *queuedReply
}

// Input queues session operations. Only the adapter assigns run/input IDs.
type Input struct {
	mu      sync.Mutex
	items   []control
	closed  bool
	timeout time.Duration
}

func NewInput(timeout time.Duration) *Input {
	if timeout <= 0 {
		timeout = DefaultAskTimeout
	}
	return &Input{timeout: timeout}
}
func (in *Input) enqueue(v control) bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return false
	}
	in.items = append(in.items, v)
	return true
}
func (in *Input) Send(text string, accepted func(Acceptance)) bool {
	return in.enqueue(control{send: &queuedSend{Text: text, Accepted: accepted}})
}
func (in *Input) Reply(id, body string, done func(error)) bool {
	return in.enqueue(control{reply: &queuedReply{RequestID: id, Body: body, Done: done}})
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
