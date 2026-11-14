package work

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

// Revoking a coordinator changes Work authority, never immutable Agent versions.
func (s *Service) DetachCoordinatorAgent(ctx context.Context, tx *sql.Tx, agent string) error {
	_, err := tx.ExecContext(ctx, `UPDATE works SET coordinator_agent_id=NULL WHERE coordinator_agent_id=?`, agent)
	return err
}

func (s *Service) runWorkToken(ctx context.Context, token string) (core.Work, core.User, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT t.work_id`+store.ActiveWorkTokenFrom+` WHERE t.token_hash=? AND p.status IN ('sent','accepted')`, core.HashSecret(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return core.Work{}, core.User{}, core.ErrNotFound
	}
	if err != nil {
		return core.Work{}, core.User{}, err
	}
	w, err := core.ScanWork(s.DB.QueryRowContext(ctx, core.WorkCols+` WHERE w.id=?`, id))
	if err != nil {
		return w, core.User{}, err
	}
	u, err := s.UserService.GetUser(ctx, w.UserID)
	return w, u, err
}

func (s *Service) WorkRunByToken(ctx context.Context, token string) (core.Run, error) {
	// Keep the grant check and the Run owner's lookup in one database snapshot.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Run{}, err
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT p.run_id`+store.ActiveWorkTokenFrom+` WHERE t.token_hash=? AND p.status IN ('sent','accepted')`, core.HashSecret(token)).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return core.Run{}, core.ErrNotFound
		}
		return core.Run{}, err
	}
	run, err := s.RunService.LookupRun(ctx, tx, id)
	if err != nil {
		return core.Run{}, err
	}
	return run, tx.Commit()
}

// WorkByToken validates the server-scoped business credential for a delivered input.
func (s *Service) WorkByToken(ctx context.Context, token string) (core.Work, core.User, error) {
	return s.runWorkToken(ctx, token)
}
