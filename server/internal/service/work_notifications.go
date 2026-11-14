package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *WorkService) notifyRunMessages(ctx context.Context, tx *sql.Tx, conversation string) error {
	ss, err := s.conversations.getConversation(ctx, tx, User{Role: RoleAdmin}, conversation)
	if err != nil {
		return err
	}
	if ss.WorkRole != WorkThread {
		return nil
	}
	prompts, err := deliveries(ctx, tx, `conversation_id=?`, conversation)
	if err != nil {
		return err
	}
	groups := map[string][]Delivery{}
	for _, p := range prompts {
		groups[p.MessageID] = append(groups[p.MessageID], p)
	}
	for id, group := range groups {
		done := true
		for _, p := range group {
			if p.Status != "completed" && p.Status != "failed" && p.Status != "cancelled" && p.Status != "rejected" {
				done = false
			}
			if p.RunID != "" && p.Status != "cancelled" {
				var reply sql.NullString
				if err := tx.QueryRowContext(ctx, `SELECT result_message_id FROM runs WHERE id=?`, p.RunID).Scan(&reply); err != nil {
					return err
				}
				if !reply.Valid {
					done = false
				}
			}
		}
		if !done {
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO run_notifications(message_id) VALUES(?)`, id)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		var summary strings.Builder
		for _, p := range group {
			fmt.Fprintf(&summary, "\n@%s: %s", p.AgentID, p.Status)
			if p.Error != "" {
				summary.WriteString("\n" + p.Error)
			}
			if p.RunID != "" {
				var output string
				if err = tx.QueryRowContext(ctx, `SELECT output FROM runs WHERE id=?`, p.RunID).Scan(&output); err != nil {
					return err
				}
				summary.WriteString("\n" + output)
			}
		}
		w, err := s.getWork(ctx, tx, User{Role: RoleAdmin}, ss.WorkID)
		if err != nil {
			return err
		}
		coord, err := s.conversations.getConversation(ctx, tx, User{Role: RoleAdmin}, w.CoordinatorID)
		if err != nil {
			return err
		}
		payload := fmt.Sprintf("[Thread %s (%s) replied]\n%s", ss.ID, ss.Title, summary.String())
		targets := []string{}
		if w.CoordinatorAgentID != "" {
			targets = append(targets, w.CoordinatorAgentID)
		}
		if _, err = s.conversations.sendMessageTx(ctx, tx, User{ID: w.UserID}, coord, payload, targets, nil, nil, ""); err != nil {
			return err
		}
	}
	return nil
}

// Summaries have their own atomic message+dedup transaction after the final reply commits.
func (s *WorkService) reconcileWorkConversation(ctx context.Context, conversation string) error {
	err := s.repository.Tx(ctx, func(tx *sql.Tx) error { return s.notifyRunMessages(ctx, tx, conversation) })
	if err == nil {
		s.Notifier.KickAll()
		s.notifyCoordinator(ctx, conversation)
	}
	return err
}

// ReconcileWorkNotifications recovers unreported terminal inputs after a restart
// or a coordinator configuration/capability change. The dedup row and summary
// message still commit together inside reconcileWorkConversation.
func (s *WorkService) ReconcileWorkNotifications(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT c.id FROM conversations c JOIN works w ON c.work_id=w.id JOIN message_agents p ON p.conversation_id=c.id WHERE c.id<>w.coordinator_id AND p.status IN ('completed','failed','cancelled','rejected') AND NOT EXISTS(SELECT 1 FROM run_notifications n WHERE n.message_id=p.message_id)`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err = s.reconcileWorkConversation(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *WorkService) deleteWorkConversationReferences(ctx context.Context, tx *sql.Tx, id string) error {
	for _, query := range []string{
		`DELETE FROM work_run_tokens WHERE message_id IN (SELECT id FROM messages WHERE conversation_id=?)`,
		`DELETE FROM run_notifications WHERE message_id IN (SELECT id FROM messages WHERE conversation_id=?)`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}
