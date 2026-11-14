package service

import (
	"context"
	"sync"

	"github.com/memory-co/agentuse/server/internal/store"
)

// BrowserChange is an invalidation hint, never conversation content or a task.
type BrowserChange struct {
	Resource  string `json:"resource"`
	ID        string `json:"id,omitempty"`
	WorkID    string `json:"work_id,omitempty"`
	owners    []string
	adminOnly bool
}

type BrowserSubscription struct {
	Wake    chan struct{}
	mu      sync.Mutex
	pending map[string]BrowserChange
	user    User
}
type BrowserChanges struct {
	mu          sync.Mutex
	subscribers map[*BrowserSubscription]struct{}
}

func (b *BrowserChanges) Subscribe(u User) (*BrowserSubscription, func()) {
	sub := &BrowserSubscription{Wake: make(chan struct{}, 1), pending: map[string]BrowserChange{}, user: u}
	b.mu.Lock()
	if b.subscribers == nil {
		b.subscribers = map[*BrowserSubscription]struct{}{}
	}
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()
	return sub, func() { b.mu.Lock(); delete(b.subscribers, sub); b.mu.Unlock() }
}
func (sub *BrowserSubscription) Drain() []BrowserChange {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	out := make([]BrowserChange, 0, len(sub.pending))
	for _, c := range sub.pending {
		out = append(out, c)
	}
	clear(sub.pending)
	return out
}
func (b *BrowserChanges) publish(c BrowserChange) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subscribers {
		allowed := sub.user.IsAdmin()
		if !c.adminOnly {
			for _, id := range c.owners {
				if id == sub.user.ID {
					allowed = true
				}
			}
		}
		if !allowed {
			continue
		}
		sub.mu.Lock()
		if _, reset := sub.pending["reset"]; !reset {
			if len(sub.pending) >= 128 {
				clear(sub.pending)
				sub.pending["reset"] = BrowserChange{Resource: "reset"}
			} else {
				sub.pending[c.Resource+":"+c.ID] = c
			}
		}
		sub.mu.Unlock()
		select {
		case sub.Wake <- struct{}{}:
		default:
		}
	}
}
func (s *serviceRuntime) notifyResource(resource, id, owner string) {
	s.BrowserChanges.publish(BrowserChange{Resource: resource, ID: id, owners: []string{owner}, adminOnly: resource == "users"})
}
func (s *serviceRuntime) publishConversationChange(ss Conversation, resource string) {
	s.BrowserChanges.publish(BrowserChange{Resource: resource, ID: ss.ID, WorkID: ss.WorkID, owners: []string{ss.UserID, ss.CreatedBy}})
}
func (s *serviceRuntime) notifyConversation(ctx context.Context, id string) {
	s.BrowserChanges.mu.Lock()
	listening := len(s.BrowserChanges.subscribers) > 0
	s.BrowserChanges.mu.Unlock()
	if !listening {
		return
	}
	if ss, err := store.ScanConversation(s.db.QueryRowContext(ctx, conversationCols+` WHERE id=?`, id)); err == nil {
		s.publishConversationChange(ss, "conversations")
	}
}
func (s *serviceRuntime) notifyCoordinator(ctx context.Context, id string) {
	if ss, err := store.ScanConversation(s.db.QueryRowContext(ctx, conversationCols+` WHERE id=?`, id)); err == nil && ss.WorkID != "" {
		if w, e := scanWork(s.db.QueryRowContext(ctx, workCols+` WHERE w.id=?`, ss.WorkID)); e == nil && w.CoordinatorID != id {
			s.notifyConversation(ctx, w.CoordinatorID)
		}
	}
}

func (s *serviceRuntime) notifyDeviceConversations(ctx context.Context, id string) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM message_agents WHERE device_id=?`, id)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var conversation string
		if rows.Scan(&conversation) == nil {
			ids = append(ids, conversation)
		}
	}
	rows.Close()
	for _, conversation := range ids {
		s.notifyConversation(ctx, conversation)
		s.notifyCoordinator(ctx, conversation)
	}
}
