package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/access"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	deviceservice "github.com/memory-co/agentuse/server/internal/service/device"
)

const roundColumns = `SELECT conversation_id,seq,start_message_seq,end_message_seq,created_at,finished_at FROM conversation_rounds`

func scanRound(row database.Scanner) (agentuse.ConversationRound, error) {
	var r agentuse.ConversationRound
	var end, finished sql.NullInt64
	err := row.Scan(&r.ConversationID, &r.Seq, &r.StartMessageSeq, &end, &r.CreatedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return r, database.ErrNotFound
	}
	if end.Valid {
		r.EndMessageSeq = &end.Int64
	}
	if finished.Valid {
		r.FinishedAt = &finished.Int64
	}
	return r, err
}

// Start a local round only for input with Agent recipients. All inputs arriving
// before quiescence join this interval, irrespective of native run boundaries.
func (s *Service) startRound(ctx context.Context, tx *gorm.DB, conversation string, messageSeq int64) (*agentuse.ConversationRound, error) {
	r, err := scanRound(database.Row(ctx, tx, roundColumns+` WHERE conversation_id=? AND finished_at IS NULL`, conversation))
	if err == nil {
		return &r, nil
	}
	if !errors.Is(err, database.ErrNotFound) {
		return nil, err
	}
	r = agentuse.ConversationRound{ConversationID: conversation, StartMessageSeq: messageSeq, CreatedAt: s.Now().Unix()}
	if err = database.Row(ctx, tx, `SELECT COALESCE(MAX(seq),0)+1 FROM conversation_rounds WHERE conversation_id=?`, conversation).Scan(&r.Seq); err != nil {
		return nil, err
	}
	_, err = database.Exec(ctx, tx, `INSERT INTO conversation_rounds(conversation_id,seq,start_message_seq,created_at) VALUES(?,?,?,?)`, conversation, r.Seq, messageSeq, r.CreatedAt)
	return &r, err
}
func (s *Service) finishRound(ctx context.Context, tx *gorm.DB, conversation string, now int64) error {
	tq := database.MessageTargets(s.DB, "m", "t")

	_, err := database.Exec(ctx, tx, `UPDATE conversation_rounds SET end_message_seq=(SELECT MAX(seq) FROM messages WHERE conversation_id=?),finished_at=? WHERE conversation_id=? AND finished_at IS NULL AND NOT EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status IN ('running','waiting')) AND NOT EXISTS(SELECT 1 FROM `+tq.From+` WHERE m.conversation_id=? AND `+tq.Pending+`)`, conversation, now, conversation, conversation, conversation)
	return err
}
func (s *Service) rounds(ctx context.Context, q *gorm.DB, conversation string) ([]agentuse.ConversationRound, error) {
	rows, err := database.Rows(ctx, q, roundColumns+` WHERE conversation_id=? ORDER BY seq`, conversation)
	if err != nil {
		return nil, err
	}
	out := []agentuse.ConversationRound{}
	for rows.Next() {
		r, e := scanRound(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.projectRoundDelivery(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// These predicates use r for the round and c for its conversation. Only routed
// inputs from the creating Agent count, including when that Agent also executes
// work and writes results in the child.
func (s *Service) roundHasParentInput() string {
	targets := database.MessageTargets(s.DB, "m", "t")
	return `EXISTS(SELECT 1 FROM ` + targets.From + `
		WHERE m.conversation_id=r.conversation_id
			AND m.seq BETWEEN r.start_message_seq AND r.end_message_seq
			AND m.author_role='agent' AND m.author_agent_id=c.parent_agent_id)`
}

func (s *Service) pendingParentRound() string {
	return `r.finished_at IS NOT NULL AND ` + s.roundHasParentInput() + `
		AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.parent_conversation_id
			AND m.author_conversation_id=c.id AND m.author_round_seq=r.seq)`
}

func (s *Service) projectRoundDelivery(ctx context.Context, q *gorm.DB, r *agentuse.ConversationRound) error {
	if r.FinishedAt == nil {
		return nil
	}
	var parent, agent string
	var hasParentInput bool
	if err := database.Row(ctx, q, `
		SELECT COALESCE(c.parent_conversation_id,''),COALESCE(c.parent_agent_id,''),`+s.roundHasParentInput()+`
		FROM conversation_rounds r JOIN conversations c ON c.id=r.conversation_id
		WHERE r.conversation_id=? AND r.seq=?`, r.ConversationID, r.Seq).Scan(&parent, &agent, &hasParentInput); err != nil {
		return err
	}
	if parent == "" {
		return nil
	}
	err := database.Row(ctx, q, `SELECT id FROM messages WHERE conversation_id=? AND author_role='child_conversation' AND author_conversation_id=? AND author_round_seq=?`, parent, r.ConversationID, r.Seq).Scan(&r.ParentMessageID)
	if err == nil {
		r.ParentDelivery = "delivered"
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !hasParentInput {
		return nil
	}
	r.ParentDelivery = "pending"
	var exists int
	if err = database.Row(ctx, q, `SELECT count(*) FROM agents WHERE id=? AND deleted_at IS NULL`, agent).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		r.ParentDelivery = "failed"
		r.ParentDeliveryError = "The creating parent Agent was deleted"
	}
	return nil
}

// ReconcileRoundNotifications calls this for finished rounds with parent input.
// Parent unavailability does not roll back an already committed child result.
func (s *Service) notifyFinishedRound(ctx context.Context, childID string, seq int64) error {
	var parentID string
	err := s.Database.Tx(ctx, func(tx *gorm.DB) error {
		r, err := scanRound(database.Row(ctx, tx, roundColumns+` WHERE conversation_id=? AND seq=? AND finished_at IS NOT NULL`, childID, seq))
		if err != nil {
			return err
		}
		child, err := s.LookupConversation(ctx, tx, agentuse.User{Role: agentuse.RoleAdmin}, childID)
		if err != nil {
			return err
		}
		if child.ParentConversationID == "" {
			return ErrConversationScope
		}
		parent, err := s.LookupConversation(ctx, tx, agentuse.User{Role: agentuse.RoleAdmin}, child.ParentConversationID)
		if err != nil {
			return err
		}
		if parent.Mode != "collaborative" || parent.ParentConversationID != "" || parent.UserID != child.UserID {
			return ErrConversationScope
		}
		var found int
		if err = database.Row(ctx, tx, `SELECT count(*) FROM messages WHERE conversation_id=? AND author_conversation_id=? AND author_round_seq=?`, parent.ID, childID, seq).Scan(&found); err != nil {
			return err
		}
		if found > 0 {
			return nil
		}
		var messageSeq int64
		if err = database.Row(ctx, tx, `SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conversation_id=?`, parent.ID).Scan(&messageSeq); err != nil {
			return err
		}
		targets, _ := json.Marshal([]string{child.ParentAgentID})
		id := uuid.NewString()
		text := fmt.Sprintf("Child conversation %q finished round %d. Read this round and review every Agent's results and any failures against this child's original objective. Respond to the user, send follow-up instructions for that same objective, or create a new child for an independent objective.", child.Title, r.Seq)
		_, err = database.Exec(ctx, tx, `INSERT INTO messages(id,seq,conversation_id,author_role,author_conversation_id,author_round_seq,to_agent_ids,content,created_at) VALUES(?,?,?,'child_conversation',?,?,?,?,?)`, id, messageSeq, parent.ID, childID, seq, string(targets), text, s.Now().Unix())
		if err != nil {
			return err
		}
		// Carry the originating round's authorization ceiling through delayed
		// notifications. A background reconciler must not mint a fresh root budget.
		rows, e := database.Rows(ctx, tx, "SELECT agent_targets FROM messages WHERE conversation_id=? AND seq>=? AND seq<=?", childID, r.StartMessageSeq, *r.EndMessageSeq)
		if e != nil {
			return e
		}
		var limit int64
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			var entries map[string]struct {
				AccessLimit int64 `json:"access_limit"`
				Claims      *struct {
					Limit int64 `json:"access_limit"`
				} `json:"access_claims"`
			}
			if e = json.Unmarshal([]byte(raw), &entries); e != nil {
				rows.Close()
				return e
			}
			for _, entry := range entries {
				ceiling := entry.AccessLimit
				if entry.Claims != nil && (ceiling == 0 || entry.Claims.Limit < ceiling) {
					ceiling = entry.Claims.Limit
				}
				if ceiling > 0 && (limit == 0 || ceiling < limit) {
					limit = ceiling
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if limit > 0 {
			ctx = access.WithLimit(ctx, limit)
		}
		if err = s.setMessageTargets(ctx, tx, parent, id, []string{child.ParentAgentID}); err != nil {
			return err
		}
		if _, err = s.startRound(ctx, tx, parent.ID, messageSeq); err != nil {
			return err
		}
		if err = s.ProjectStatus(ctx, tx, parent.ID, s.Now().Unix()); err != nil {
			return err
		}
		parentID = parent.ID
		return nil
	})
	if err == nil && parentID != "" {
		s.NotifyConversation(ctx, childID)
		s.NotifyConversation(ctx, parentID)
		s.Notifier.KickAll()
	}
	return err
}

// The unique author/round key is the delivery receipt for a delegated round.
func (s *Service) ReconcileRoundNotifications(ctx context.Context) error {
	rows, err := database.Rows(ctx, s.DB, `
		SELECT r.conversation_id,r.seq FROM conversation_rounds r
		JOIN conversations c ON c.id=r.conversation_id
		JOIN agents a ON a.id=c.parent_agent_id AND a.deleted_at IS NULL
		WHERE `+s.pendingParentRound()+` ORDER BY r.finished_at,r.conversation_id,r.seq`)
	if err != nil {
		return err
	}
	type ref struct {
		id  string
		seq int64
	}
	var pending []ref
	for rows.Next() {
		var v ref
		if err = rows.Scan(&v.id, &v.seq); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	blocked := map[string]bool{}
	for _, v := range pending {
		if blocked[v.id] {
			continue
		}
		if err = s.notifyFinishedRound(ctx, v.id, v.seq); err != nil {
			// Preserve round order for this child while allowing other children
			// to notify their coordinators independently.
			blocked[v.id] = true
			if !errors.Is(err, deviceservice.ErrRuntimeUnavailable) && !errors.Is(err, database.ErrNotFound) {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) ParentAgentDeleted(ctx context.Context, agent string) {
	rows, err := database.Rows(ctx, s.DB, `SELECT id FROM conversations WHERE parent_agent_id=?`, agent)
	if err != nil {
		s.Log.Printf("parent Agent notification: %v", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		s.Log.Printf("parent Agent notification: %v", err)
		return
	}
	for _, id := range ids {
		s.NotifyConversation(ctx, id)
	}
}
