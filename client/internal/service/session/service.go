package session

import (
	"context"
	"fmt"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
)

// Service owns native session selection and conversation workspace policy.
// File persistence is delegated to store; harness execution remains separate.
type Service struct {
	Files    *store.Ledger
	Settings *setting.Service
}

func (s *Service) Acquire(ctx context.Context, endpoint, conversation, agent, harness, revision string) (store.Session, string, error) {
	cfg := s.Settings.Get()
	if cfg.Session.WorkRoot != s.Files.Root {
		return store.Session{}, "", fmt.Errorf("session.work_root changed; restart client after migrating existing directories")
	}
	if q := cfg.Session.Quota; q != nil {
		if _, e := s.Files.Reclaim(ctx, q.MaxBytes); e != nil {
			return store.Session{}, "", e
		}
	}
	return s.Files.AcquireSession(ctx, endpoint, conversation, agent, harness, revision)
}
func (s *Service) Release(session store.Session, seq int64, valid bool, run, attempt string) error {
	return s.Files.ReleaseSession(session, seq, valid, run, attempt)
}
func (s *Service) Reclaim(ctx context.Context) (int64, error) {
	cfg := s.Settings.Get()
	if cfg.Session.Quota == nil {
		return 0, nil
	}
	return s.Files.Reclaim(ctx, cfg.Session.Quota.MaxBytes)
}

func (s *Service) CreateConversation(ctx context.Context, ep endpoint.Endpoint, title string) (store.Conversation, error) {
	if !ep.IsLocal() {
		return store.Conversation{}, fmt.Errorf("platform conversations are created on their server")
	}
	if _, e := s.Reclaim(ctx); e != nil {
		return store.Conversation{}, e
	}
	return s.Files.CreateLocalConversation(ctx, title)
}
