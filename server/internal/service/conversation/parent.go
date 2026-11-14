package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// Parent delivery is a projection of ordinary source and destination messages.
// The child's creating Agent determines the destination, independently of inputs.
func (s *Service) projectParentDelivery(ctx context.Context, q core.Querier, m *core.Message) error {
	if !m.ParentDeliveryRequested {
		return nil
	}
	var parent, agent string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(parent_conversation_id,''),COALESCE(parent_agent_id,'') FROM conversations WHERE id=?`, m.ConversationID).Scan(&parent, &agent); err != nil {
		return err
	}
	if parent == "" || agent == "" {
		m.ParentDelivery = "failed"
		m.ParentDeliveryError = "The source conversation has no parent Agent"
		return nil
	}
	err := q.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id=? AND source_message_id=?`, parent, m.ID).Scan(&m.ParentMessageID)
	if err == nil {
		m.ParentDelivery = "delivered"
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var exists int
	if err = q.QueryRowContext(ctx, `SELECT count(*) FROM agents WHERE id=? AND deleted_at IS NULL`, agent).Scan(&exists); err != nil {
		return err
	}
	m.ParentDelivery = "pending"
	if exists == 0 {
		m.ParentDelivery = "failed"
		m.ParentDeliveryError = "The creating parent Agent was deleted"
	}
	return nil
}

// SendParentMessage first commits an Agent feedback message in its own conversation.
// Parent unavailability cannot undo this durable source; reconciliation retries it.
func (s *Service) SendParentMessage(ctx context.Context, source core.AgentSource, input string, attachmentIDs []string) (sdk.ParentMessageResponse, error) {
	var response sdk.ParentMessageResponse
	if !utf8.ValidString(input) || (strings.TrimSpace(input) == "" && len(attachmentIDs) == 0) {
		return response, fmt.Errorf("message text or attachments required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return response, err
	}
	defer tx.Rollback()
	child, err := s.LookupConversation(ctx, tx, source.User, source.ConversationID)
	if err != nil {
		return response, err
	}
	if child.ParentConversationID == "" {
		return response, core.ErrConversationScope
	}
	files, err := s.messageAttachments(ctx, tx, child.ID, attachmentIDs)
	if err != nil {
		return response, err
	}
	m, err := s.insertMessage(ctx, tx, child.ID, core.RoleAgentMsg, input, s.Unix())
	if err != nil {
		return response, err
	}
	if err = s.setAgentAuthor(ctx, tx, &m, &source); err != nil {
		return response, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE messages SET parent_delivery_requested=1,attachments=? WHERE id=?`, string(mustAttachmentJSON(files)), m.ID); err != nil {
		return response, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, s.Unix(), child.ID); err != nil {
		return response, err
	}
	if err = tx.Commit(); err != nil {
		return response, err
	}
	m.ParentDeliveryRequested = true
	m.Attachments = files
	s.NotifyConversation(ctx, child.ID)
	if err = s.forwardParentMessage(ctx, m.ID); err != nil && !errors.Is(err, core.ErrRuntimeUnavailable) && !errors.Is(err, core.ErrNotFound) {
		s.Log.Printf("parent feedback pending: %v", err)
	}
	if err = s.projectParentDelivery(ctx, s.DB, &m); err != nil {
		return response, err
	}
	return sdk.ParentMessageResponse{Message: m, ParentConversationID: child.ParentConversationID, ParentMessageID: m.ParentMessageID, Delivery: m.ParentDelivery, Error: m.ParentDeliveryError}, nil
}

func (s *Service) forwardParentMessage(ctx context.Context, id string) error {
	var sourceID, parentID string
	err := s.Repository.Tx(ctx, func(tx *sql.Tx) error {
		m, err := core.ScanMessage(tx.QueryRowContext(ctx, core.MessageCols+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if !m.ParentDeliveryRequested || m.SourceMessageID != "" {
			return nil
		}
		child, err := s.LookupConversation(ctx, tx, core.User{Role: core.RoleAdmin}, m.ConversationID)
		if err != nil {
			return err
		}
		if child.ParentConversationID == "" || child.ParentAgentID == "" {
			return core.ErrConversationState
		}
		parent, err := s.LookupConversation(ctx, tx, core.User{Role: core.RoleAdmin}, child.ParentConversationID)
		if err != nil {
			return err
		}
		if parent.ParentConversationID != "" || parent.Mode != "collaborative" || parent.UserID != child.UserID {
			return core.ErrConversationScope
		}
		var existing int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id=? AND source_message_id=?`, parent.ID, m.ID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		copy, err := s.insertMessage(ctx, tx, parent.ID, core.RoleAgentMsg, m.Content, s.Unix())
		if err != nil {
			return err
		}
		targets, _ := json.Marshal([]string{child.ParentAgentID})
		if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_agent_id=?,author_agent_version_id=?,to_agent_ids=?,source_conversation_id=?,source_message_id=? WHERE id=?`, m.AuthorAgentID, m.AuthorAgentVersionID, string(targets), child.ID, m.ID, copy.ID); err != nil {
			return err
		}
		if err = s.RunService.AssignMessageTargets(ctx, tx, parent, copy.ID, []string{child.ParentAgentID}); err != nil {
			return err
		}
		if err = s.ProjectStatus(ctx, tx, parent.ID, s.Unix()); err != nil {
			return err
		}
		sourceID, parentID = child.ID, parent.ID
		return nil
	})
	if err == nil && parentID != "" {
		s.NotifyConversation(ctx, sourceID)
		s.NotifyConversation(ctx, parentID)
		s.Notifier.KickAll()
	}
	return err
}

// RecordRejectedFeedback publishes only confirmed rejections, never unknown sends.
func (s *Service) recordRejectedFeedback(ctx context.Context, conversation string) error {
	changed := false
	err := s.Repository.Tx(ctx, func(tx *sql.Tx) error {
		child, err := s.LookupConversation(ctx, tx, core.User{Role: core.RoleAdmin}, conversation)
		if err != nil {
			return err
		}
		if child.ParentConversationID == "" {
			return nil
		}
		deliveries, err := s.RunService.DeliveriesIn(ctx, tx, `conversation_id=? AND status='rejected'`, conversation)
		if err != nil {
			return err
		}
		for _, d := range deliveries {
			var found int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id=? AND reply_to_message_id=? AND author_agent_id=? AND parent_delivery_requested=1`, conversation, d.MessageID, d.AgentID).Scan(&found); err != nil {
				return err
			}
			if found > 0 {
				continue
			}
			changed = true
			text := fmt.Sprintf("@%s could not start this input. %s", d.AgentID, d.Error)
			m, err := s.insertMessage(ctx, tx, conversation, core.RoleAgentMsg, strings.TrimSpace(text), s.Unix())
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_agent_id=?,author_agent_version_id=?,reply_to_message_id=?,parent_delivery_requested=1 WHERE id=?`, d.AgentID, d.AgentVersionID, d.MessageID, m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && changed {
		s.NotifyConversation(ctx, conversation)
	}
	return err
}

// ReconcileParentMessages recovers committed source messages after transient
// delivery failures or a restart. The unique message source index is the receipt.
func (s *Service) ReconcileParentMessages(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT c.id FROM conversations c JOIN message_agents p ON p.conversation_id=c.id WHERE c.parent_conversation_id IS NOT NULL AND p.status='rejected' AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.reply_to_message_id=p.message_id AND m.author_agent_id=p.agent_id AND m.parent_delivery_requested=1)`)
	if err != nil {
		return err
	}
	var children []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		children = append(children, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range children {
		if err = s.recordRejectedFeedback(ctx, id); err != nil {
			return err
		}
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN agents a ON a.id=c.parent_agent_id AND a.deleted_at IS NULL WHERE m.parent_delivery_requested=1 AND m.source_message_id IS NULL AND NOT EXISTS(SELECT 1 FROM messages f WHERE f.conversation_id=c.parent_conversation_id AND f.source_message_id=m.id) ORDER BY m.created_at,m.id`)
	if err != nil {
		return err
	}
	var messages []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range messages {
		if err = s.forwardParentMessage(ctx, id); err != nil && !errors.Is(err, core.ErrRuntimeUnavailable) && !errors.Is(err, core.ErrNotFound) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ParentAgentDeleted refreshes the read-only delivery projection without changing
// creation lineage or redirecting feedback to a replacement recipient.
func (s *Service) ParentAgentDeleted(ctx context.Context, agent string) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM conversations WHERE parent_agent_id=?`, agent)
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
