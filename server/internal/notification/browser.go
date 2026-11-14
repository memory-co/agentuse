// Package notification distributes change hints without querying business data.
package notification

import (
	"sync"
)

// Change invalidates a resource; it never contains message content or credentials.
type Change struct {
	Resource             string `json:"resource"`
	ID                   string `json:"id,omitempty"`
	ParentConversationID string `json:"parent_conversation_id,omitempty"`
}

type Subscription struct {
	Wake    chan struct{}
	mu      sync.Mutex
	pending map[string]Change
	userID  string
	admin   bool
}

type Broker struct {
	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
}

func (b *Broker) Subscribe(userID string, admin bool) (*Subscription, func()) {
	sub := &Subscription{Wake: make(chan struct{}, 1), pending: map[string]Change{}, userID: userID, admin: admin}
	b.mu.Lock()
	if b.subscribers == nil {
		b.subscribers = map[*Subscription]struct{}{}
	}
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()
	return sub, func() { b.mu.Lock(); delete(b.subscribers, sub); b.mu.Unlock() }
}
func (sub *Subscription) Drain() []Change {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	out := make([]Change, 0, len(sub.pending))
	for _, change := range sub.pending {
		out = append(out, change)
	}
	clear(sub.pending)
	return out
}
func (b *Broker) HasSubscribers() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers) > 0
}

// Publish is visible to admins and the supplied owners. No owners means admin-only.
// The caller determines the audience after its business transaction commits.
func (b *Broker) Publish(change Change, owners ...string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subscribers {
		allowed := sub.admin
		for _, owner := range owners {
			if owner == sub.userID {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		sub.mu.Lock()
		if _, reset := sub.pending["reset"]; !reset {
			if len(sub.pending) >= 128 {
				clear(sub.pending)
				sub.pending["reset"] = Change{Resource: "reset"}
			} else {
				sub.pending[change.Resource+":"+change.ID] = change
			}
		}
		sub.mu.Unlock()
		select {
		case sub.Wake <- struct{}{}:
		default:
		}
	}
}
