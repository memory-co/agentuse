package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
)

func (s *Service) Reconcile(ctx context.Context) error {
	if err := (&serverDeliverySink{l: s}).Reconcile(ctx); err != nil {
		return err
	}
	return s.rebuildNativeIndexes(ctx)
}

// pendingConversation is the server protocol's directory protection policy.
// sessionService owns quota and asks this policy without interpreting deliveries.
func (s *Service) pendingConversation(ctx context.Context, endpoint, conversation string) (bool, error) {
	sessions, err := s.Sessions.Sessions(ctx, endpoint, conversation)
	if err != nil {
		return false, err
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	for _, metadata := range sessions {
		dir, err := fileutil.SafePath(s.Sessions.Root, endpoint, conversation, "sessions", metadata.ID, ".server")
		if err != nil {
			return false, err
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			path, err := fileutil.SafePath(dir, entry.Name())
			if err != nil {
				return false, err
			}
			records, err := readDeliveryJournal(path)
			if err != nil {
				return false, err
			}
			for _, record := range records {
				if len(record.PendingReport) > 0 && !record.ServerConfirmed {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (s *Service) Recover(ctx context.Context) error {
	endpoints, err := os.ReadDir(s.Sessions.Root)
	if err != nil {
		return err
	}
	var active []harness.RecoverySession
	for _, ep := range endpoints {
		if !ep.IsDir() {
			continue
		}
		conversations, err := s.Sessions.Conversations(ctx, ep.Name())
		if err != nil {
			return err
		}
		for _, c := range conversations {
			sessions, err := s.Sessions.Sessions(ctx, ep.Name(), c.ID)
			if err != nil {
				return err
			}
			for _, metadata := range sessions {
				if metadata.Status == "running" {
					active = append(active, harness.RecoverySession{Harness: metadata.Harness, Revision: metadata.HarnessRevision, EventsPath: filepath.Join(s.Sessions.Root, ep.Name(), c.ID, "sessions", metadata.ID, "events.jsonl")})
				}
			}
		}
	}
	return s.Harness.Recover(ctx, active)
}
