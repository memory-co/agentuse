package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

var (
	// ErrConversationBusy 表示会话还在等 harness 回复，不能再发消息。
	ErrConversationBusy = errors.New("会话还在等 harness 回复")
	// ErrRuntimeUnavailable 表示会话的运行时(device 上的 harness)没开启或不可用，派不出去。
	ErrRuntimeUnavailable = errors.New("harness 不可用")
	// ErrConversationState 表示会话当前的状态不能做这个操作(如标记完成时不是 idle)。
	ErrConversationState = errors.New("会话状态不对")
	// ErrCoordinator 表示这个操作不能对工作的协调者做。
	ErrCoordinator = errors.New("这是工作的协调者，不能单独操作，请操作整个工作")
)

func (s *ConversationService) insertMessage(ctx context.Context, tx *sql.Tx, conversationID, role string, content string, now int64) (Message, error) {
	var seq int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conversation_id=?", conversationID).Scan(&seq); err != nil {
		return Message{}, err
	}
	id := newUUID()
	_, err := tx.ExecContext(ctx, store.InsertMessageInsert, id, seq, conversationID, role, content, now)
	if err != nil {
		return Message{}, err
	}
	return Message{ID: id, Seq: seq, ConversationID: conversationID, AuthorRole: role, Content: content, CreatedAt: now, ToAgentIDs: []string{}, ToUserIDs: []string{}, Attachments: []sdk.Attachment{}}, nil
}

func (s *ConversationService) scanConversation(row scanner) (Conversation, error) {
	return store.ScanConversation(row)
}

func (s *ConversationService) getConversation(ctx context.Context, q querier, u User, id string) (Conversation, error) {
	query := conversationCols + store.GetConversationClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetConversationClause2
		args = append(args, u.ID, u.ID)
	}
	return s.scanConversation(q.QueryRowContext(ctx, query, args...))
}

func (s *ConversationService) GetConversation(ctx context.Context, u User, id string) (Conversation, error) {
	return s.getConversation(ctx, s.db, u, id)
}

// ListConversations 列最近的普通会话(不在工作里的):自己的 agent 上的、和自己发起的;admin 看所有人的。
func (s *ConversationService) ListConversations(ctx context.Context, u User, limit int) ([]Conversation, error) {
	q := conversationCols + store.ListConversationsClause
	var args []any
	if !u.IsAdmin() {
		q += store.ListConversationsClause2
		args = append(args, u.ID, u.ID)
	}
	return s.listConversations(ctx, q, limit, args...)
}

// ListWorkConversations 列工作里的线程(不含协调者)。工作看不到返回 ErrNotFound。
func (s *ConversationService) ListWorkConversations(ctx context.Context, u User, workID string, limit int) ([]Conversation, error) {
	w, err := s.works.getWork(ctx, s.db, u, workID)
	if err != nil {
		return nil, err
	}
	return s.listConversations(ctx, conversationCols+store.ListWorkConversationsClause, limit, w.ID, w.CoordinatorID)
}

func (s *ConversationService) listConversations(ctx context.Context, q string, limit int, args ...any) ([]Conversation, error) {
	q += store.ListConversationsClause3
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		ss, err := s.scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// DeleteConversation 删除会话和它的消息、回报。running / waiting 时返回 ErrConversationBusy。
func (s *ConversationService) DeleteConversation(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ss, err := s.getConversation(ctx, tx, u, id)
	if err != nil {
		return err
	}
	if ss.Status == ConversationRunning || ss.Status == ConversationWaiting || ss.Status == ConversationPending {
		return ErrConversationBusy
	}
	if ss.WorkRole == WorkCoordinator {
		return ErrCoordinator
	}
	if err := s.deleteConversationContents(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, store.DeleteConversationDelete, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishConversationChange(ss, "conversations")
	s.cleanupConversationFiles(ctx, id)
	return nil
}

func (s *ConversationService) Messages(ctx context.Context, conversationID string) ([]Message, error) {
	return messages(ctx, s.db, conversationID)
}

func messages(ctx context.Context, q rowsQuerier, conversationID string) ([]Message, error) {
	rows, err := q.QueryContext(ctx, messageCols+store.MessagesClause, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *ConversationService) projectConversationStatus(ctx context.Context, tx *sql.Tx, conversation string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE conversations SET status=CASE WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='waiting') THEN 'waiting' WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='running') THEN 'running' WHEN EXISTS(SELECT 1 FROM message_agents WHERE conversation_id=? AND status IN ('pending','sent','unknown')) THEN 'pending' WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=?`, conversation, conversation, conversation, now, conversation)
	return err
}

func (s *ConversationService) insertConversation(ctx context.Context, tx *sql.Tx, u User, work, title string) (Conversation, error) {
	if work != "" {
		if _, err := s.works.getWork(ctx, tx, u, work); err != nil {
			return Conversation{}, err
		}
	}
	id := newUUID()
	now := s.unix()
	if _, err := tx.ExecContext(ctx, store.InsertConversationInsert, id, u.ID, u.ID, nullIfEmpty(work), title, now, now); err != nil {
		return Conversation{}, err
	}
	return s.getConversation(ctx, tx, u, id)
}
func (s *ConversationService) createConversation(ctx context.Context, u User, work, title string, input string, toAgentIDs, toUserIDs []string) (Conversation, *Reply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, nil, err
	}
	defer tx.Rollback()
	ss, err := s.insertConversation(ctx, tx, u, work, title)
	if err != nil {
		return ss, nil, err
	}
	var reply *Reply
	if input == "" && (len(toAgentIDs) > 0 || len(toUserIDs) > 0) {
		return ss, nil, fmt.Errorf("指定收件人时需要消息内容")
	}
	if input != "" {
		r, e := s.sendMessageTx(ctx, tx, u, ss, input, toAgentIDs, toUserIDs, nil, "")
		if e != nil {
			return ss, nil, e
		}
		reply = &r
		ss, e = s.getConversation(ctx, tx, u, ss.ID)
		if e != nil {
			return ss, nil, e
		}
	}
	return ss, reply, tx.Commit()
}
func (s *ConversationService) sendMessage(ctx context.Context, u User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (Reply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reply{}, err
	}
	defer tx.Rollback()
	ss, err := s.getConversation(ctx, tx, u, conversation)
	if err != nil {
		return Reply{}, err
	}
	out, err := s.sendMessageTx(ctx, tx, u, ss, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *ConversationService) sendMessageTx(ctx context.Context, tx *sql.Tx, u User, ss Conversation, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (Reply, error) {
	if !utf8.ValidString(input) || (strings.TrimSpace(input) == "" && len(attachmentIDs) == 0) {
		return Reply{}, fmt.Errorf("message text or attachments required")
	}
	if replyToMessageID != "" {
		if uuid.Validate(replyToMessageID) != nil {
			return Reply{}, fmt.Errorf("invalid reply_to_message_id")
		}
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id=? AND conversation_id=?`, replyToMessageID, ss.ID).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return Reply{}, ErrNotFound
			}
			return Reply{}, err
		}
	}
	toAgentIDs, err := recipientIDs(toAgentIDs)
	if err != nil {
		return Reply{}, err
	}
	toUserIDs, err = recipientIDs(toUserIDs)
	if err != nil {
		return Reply{}, err
	}
	for _, id := range toUserIDs {
		recipient, err := store.ScanUser(tx.QueryRowContext(ctx, store.GetUserSelect, id))
		if err != nil {
			return Reply{}, err
		}
		if _, err = s.getConversation(ctx, tx, recipient, ss.ID); err != nil {
			return Reply{}, err
		}
	}
	files, err := s.messageAttachments(ctx, tx, ss.ID, attachmentIDs)
	if err != nil {
		return Reply{}, err
	}

	m, err := s.insertMessage(ctx, tx, ss.ID, RoleUserMsg, input, s.unix())
	if err != nil {
		return Reply{}, err
	}
	agentJSON, _ := json.Marshal(toAgentIDs)
	userJSON, _ := json.Marshal(toUserIDs)
	if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_user_id=?,to_agent_ids=?,to_user_ids=?,attachments=?,reply_to_message_id=? WHERE id=?`, u.ID, string(agentJSON), string(userJSON), string(mustAttachmentJSON(files)), nullIfEmpty(replyToMessageID), m.ID); err != nil {
		return Reply{}, err
	}
	m.AuthorUserID, m.ToAgentIDs, m.ToUserIDs, m.Attachments = u.ID, toAgentIDs, toUserIDs, files
	m.AuthorName, m.ReplyToMessageID = u.Name, replyToMessageID
	if err = s.runs.assignMessageTargets(ctx, tx, ss, m.ID, toAgentIDs); err != nil {
		return Reply{}, err
	}
	title := []rune(strings.TrimSpace(input))
	if len(title) == 0 && len(files) > 0 {
		title = []rune(files[0].Filename)
	}
	if len(title) > 50 {
		title = title[:50]
	}
	if ss.Title == "" {
		if _, err = tx.ExecContext(ctx, `UPDATE conversations SET title=? WHERE id=?`, string(title), ss.ID); err != nil {
			return Reply{}, err
		}
	}
	if err = s.projectConversationStatus(ctx, tx, ss.ID, s.unix()); err != nil {
		return Reply{}, err
	}
	out := Reply{Message: &m, Runs: []Run{}}
	out.Deliveries, err = deliveries(ctx, tx, `message_id=?`, m.ID)
	return out, err
}

// recipientIDs preserves the explicit recipient order and stores omitted arrays as [].
func recipientIDs(ids []string) ([]string, error) {
	out := []string{}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("收件人 ID 必须是非空且无首尾空格的字符串")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}

// deleteConversationContents coordinates each owner's cleanup in one transaction.
func (s *ConversationService) deleteConversationContents(ctx context.Context, tx *sql.Tx, id string) error {
	if err := s.works.deleteWorkConversationReferences(ctx, tx, id); err != nil {
		return err
	}
	if err := s.runs.deleteConversationExecution(ctx, tx, id); err != nil {
		return err
	}
	for _, query := range []string{`DELETE FROM attachments WHERE conversation_id=?`, `DELETE FROM messages WHERE conversation_id=?`} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}
