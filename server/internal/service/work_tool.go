package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AuthorizeWorkTool derives business scope from a delivered coordinator input.
// When no message is specified, Server resolves it from its accepted run mapping.
// An explicit message can authorize while native acceptance is still in flight;
// once accepted, the reported run must match the caller.
func (s *WorkService) AuthorizeWorkTool(ctx context.Context, device, conversation, message, agent, run string) (string, error) {
	if uuid.Validate(run) != nil {
		return "", fmt.Errorf("invalid run UUID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var work string
	err = tx.QueryRowContext(ctx, `SELECT w.id, p.message_id FROM message_agents p
 JOIN works w ON w.coordinator_id=p.conversation_id AND w.coordinator_agent_id=p.agent_id
 WHERE p.device_id=? AND p.conversation_id=? AND p.agent_id=?
 AND ((?<>'' AND p.message_id=? AND p.status='sent')
 OR (p.status='accepted' AND p.run_id=? AND (?='' OR p.message_id=?)))
 LIMIT 1`, device, conversation, agent, message, message, run, message, message).Scan(&work, &message)
	if err != nil {
		return "", err
	}
	token := RandomToken("wt_")
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_run_tokens(token_hash,work_id,message_id,agent_id) VALUES(?,?,?,?)`, HashSecret(token), work, message, agent); err != nil {
		return "", err
	}
	return token, tx.Commit()
}
