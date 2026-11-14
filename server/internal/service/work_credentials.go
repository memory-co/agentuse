package service

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/store"
)

// Revoking a coordinator changes Work authority, never immutable Agent versions.
func (s *WorkService) detachCoordinatorAgent(ctx context.Context, tx *sql.Tx, agent string) error {
	_, err := tx.ExecContext(ctx, `UPDATE works SET coordinator_agent_id=NULL WHERE coordinator_agent_id=?`, agent)
	return err
}

func (s *WorkService) runWorkToken(ctx context.Context, token string) (Work, User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.work_id`+store.ActiveWorkTokenFrom+` WHERE t.token_hash=? AND p.status IN ('sent','accepted')`, HashSecret(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return Work{}, User{}, ErrNotFound
	}
	if err != nil {
		return Work{}, User{}, err
	}
	w, err := scanWork(s.db.QueryRowContext(ctx, workCols+` WHERE w.id=?`, id))
	if err != nil {
		return w, User{}, err
	}
	u, err := s.users.GetUser(ctx, w.UserID)
	return w, u, err
}

func (s *WorkService) WorkRunByToken(ctx context.Context, token string) (Run, error) {
	return scanNativeRun(s.db.QueryRowContext(ctx, nativeRunCols+` WHERE id=(SELECT p.run_id`+store.ActiveWorkTokenFrom+` WHERE t.token_hash=? AND p.status IN ('sent','accepted'))`, HashSecret(token)))
}

// WorkByToken validates the server-scoped business credential for a delivered input.
func (s *WorkService) WorkByToken(ctx context.Context, token string) (Work, User, error) {
	return s.runWorkToken(ctx, token)
}
