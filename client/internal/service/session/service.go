package session

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/setting"
)

// Service owns native session selection and conversation workspace policy.
// Session YAML, workspace layout and quota are owned here.
type Service struct {
	Root      string
	mu        sync.Mutex
	reserved  map[string]int
	Protected func(context.Context, string, string) (bool, error)
	Settings  *setting.Service
}

func (s *Service) Reclaim(ctx context.Context) (int64, error) {
	cfg := setting.Defaults()
	if s.Settings != nil {
		cfg = s.Settings.Get()
	}
	if cfg.Session.Quota == nil {
		return 0, nil
	}
	return s.ReclaimToLimit(ctx, cfg.Session.Quota.MaxBytes)
}

func (s *Service) CreateConversation(ctx context.Context, ep endpoint.Endpoint, title string) (Conversation, error) {
	if !ep.IsLocal() {
		return Conversation{}, fmt.Errorf("platform conversations are created on their server")
	}
	if _, e := s.Reclaim(ctx); e != nil {
		return Conversation{}, e
	}
	return s.CreateLocalConversation(ctx, title)
}

func New(root string) (*Service, error) {
	path, err := fileutil.SafePath(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	return &Service{Root: path}, nil
}
