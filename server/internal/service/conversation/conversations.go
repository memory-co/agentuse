package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) insertMessage(ctx context.Context, tx *sql.Tx, conversationID, role string, content string, now int64) (core.Message, error) {
	var seq int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conversation_id=?", conversationID).Scan(&seq); err != nil {
		return core.Message{}, err
	}
	id := core.NewUUID()
	_, err := tx.ExecContext(ctx, store.InsertMessageInsert, id, seq, conversationID, role, content, now)
	if err != nil {
		return core.Message{}, err
	}
	return core.Message{ID: id, Seq: seq, ConversationID: conversationID, AuthorRole: role, Content: content, CreatedAt: now, ToAgentIDs: []string{}, ToUserIDs: []string{}, Attachments: []sdk.Attachment{}}, nil
}

func (s *Service) scanConversation(row core.Scanner) (core.Conversation, error) {
	return store.ScanConversation(row)
}

func (s *Service) LookupConversation(ctx context.Context, q core.Querier, u core.User, id string) (core.Conversation, error) {
	query := core.ConversationCols + store.GetConversationClause
	args := []any{id}
	if !u.IsAdmin() {
		query += store.GetConversationClause2
		args = append(args, u.ID, u.ID)
	}
	return s.scanConversation(q.QueryRowContext(ctx, query, args...))
}

func (s *Service) GetConversation(ctx context.Context, u core.User, id string) (core.Conversation, error) {
	return s.LookupConversation(ctx, s.DB, u, id)
}

// ListConversations 列最近的普通会话(不在工作里的):自己的 agent 上的、和自己发起的;admin 看所有人的。
func (s *Service) ListConversations(ctx context.Context, u core.User, limit int) ([]core.Conversation, error) {
	q := core.ConversationCols + store.ListConversationsClause
	var args []any
	if !u.IsAdmin() {
		q += store.ListConversationsClause2
		args = append(args, u.ID, u.ID)
	}
	return s.listConversations(ctx, q, limit, args...)
}

// ListWorkConversations 列工作里的线程(不含协调者)。工作看不到返回 ErrNotFound。
func (s *Service) ListWorkConversations(ctx context.Context, u core.User, workID string, limit int) ([]core.Conversation, error) {
	w, err := s.WorkService.LookupWork(ctx, s.DB, u, workID)
	if err != nil {
		return nil, err
	}
	return s.listConversations(ctx, core.ConversationCols+store.ListWorkConversationsClause, limit, w.ID, w.CoordinatorID)
}

func (s *Service) listConversations(ctx context.Context, q string, limit int, args ...any) ([]core.Conversation, error) {
	q += store.ListConversationsClause3
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Conversation{}
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
func (s *Service) DeleteConversation(ctx context.Context, u core.User, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil {
		return err
	}
	if ss.Status == core.ConversationRunning || ss.Status == core.ConversationWaiting || ss.Status == core.ConversationPending {
		return core.ErrConversationBusy
	}
	if ss.WorkRole == core.WorkCoordinator {
		return core.ErrCoordinator
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
	s.PublishConversationChange(ss, "conversations")
	s.CleanupFiles(ctx, id)
	return nil
}

func (s *Service) Messages(ctx context.Context, conversationID string) ([]core.Message, error) {
	return messages(ctx, s.DB, conversationID)
}

func messages(ctx context.Context, q core.RowsQuerier, conversationID string) ([]core.Message, error) {
	rows, err := q.QueryContext(ctx, core.MessageCols+store.MessagesClause, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Message{}
	for rows.Next() {
		m, err := core.ScanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) ProjectStatus(ctx context.Context, tx *sql.Tx, conversation string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE conversations SET status=CASE WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='waiting') THEN 'waiting' WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='running') THEN 'running' WHEN EXISTS(SELECT 1 FROM message_agents WHERE conversation_id=? AND status IN ('pending','sent','unknown')) THEN 'pending' WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=?`, conversation, conversation, conversation, now, conversation)
	return err
}

func (s *Service) InsertConversation(ctx context.Context, tx *sql.Tx, u core.User, work, title string) (core.Conversation, error) {
	if work != "" {
		if _, err := s.WorkService.LookupWork(ctx, tx, u, work); err != nil {
			return core.Conversation{}, err
		}
	}
	id := core.NewUUID()
	now := s.Unix()
	if _, err := tx.ExecContext(ctx, store.InsertConversationInsert, id, u.ID, u.ID, core.NullIfEmpty(work), title, now, now); err != nil {
		return core.Conversation{}, err
	}
	return s.LookupConversation(ctx, tx, u, id)
}
func (s *Service) createConversation(ctx context.Context, u core.User, work, title string, input string, toAgentIDs, toUserIDs []string) (core.Conversation, *core.Reply, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Conversation{}, nil, err
	}
	defer tx.Rollback()
	ss, err := s.InsertConversation(ctx, tx, u, work, title)
	if err != nil {
		return ss, nil, err
	}
	var reply *core.Reply
	if input == "" && (len(toAgentIDs) > 0 || len(toUserIDs) > 0) {
		return ss, nil, fmt.Errorf("指定收件人时需要消息内容")
	}
	if input != "" {
		r, e := s.SendMessageTx(ctx, tx, u, ss, input, toAgentIDs, toUserIDs, nil, "")
		if e != nil {
			return ss, nil, e
		}
		reply = &r
		ss, e = s.LookupConversation(ctx, tx, u, ss.ID)
		if e != nil {
			return ss, nil, e
		}
	}
	return ss, reply, tx.Commit()
}
func (s *Service) sendMessage(ctx context.Context, u core.User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (core.Reply, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return core.Reply{}, err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, conversation)
	if err != nil {
		return core.Reply{}, err
	}
	out, err := s.SendMessageTx(ctx, tx, u, ss, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) SendMessageTx(ctx context.Context, tx *sql.Tx, u core.User, ss core.Conversation, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (core.Reply, error) {
	if !utf8.ValidString(input) || (strings.TrimSpace(input) == "" && len(attachmentIDs) == 0) {
		return core.Reply{}, fmt.Errorf("message text or attachments required")
	}
	if replyToMessageID != "" {
		if uuid.Validate(replyToMessageID) != nil {
			return core.Reply{}, fmt.Errorf("invalid reply_to_message_id")
		}
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id=? AND conversation_id=?`, replyToMessageID, ss.ID).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return core.Reply{}, core.ErrNotFound
			}
			return core.Reply{}, err
		}
	}
	toAgentIDs, err := core.RecipientIDs(toAgentIDs)
	if err != nil {
		return core.Reply{}, err
	}
	toUserIDs, err = core.RecipientIDs(toUserIDs)
	if err != nil {
		return core.Reply{}, err
	}
	for _, id := range toUserIDs {
		recipient, err := store.ScanUser(tx.QueryRowContext(ctx, store.GetUserSelect, id))
		if err != nil {
			return core.Reply{}, err
		}
		if _, err = s.LookupConversation(ctx, tx, recipient, ss.ID); err != nil {
			return core.Reply{}, err
		}
	}
	files, err := s.messageAttachments(ctx, tx, ss.ID, attachmentIDs)
	if err != nil {
		return core.Reply{}, err
	}

	m, err := s.insertMessage(ctx, tx, ss.ID, core.RoleUserMsg, input, s.Unix())
	if err != nil {
		return core.Reply{}, err
	}
	agentJSON, _ := json.Marshal(toAgentIDs)
	userJSON, _ := json.Marshal(toUserIDs)
	if _, err = tx.ExecContext(ctx, `UPDATE messages SET author_user_id=?,to_agent_ids=?,to_user_ids=?,attachments=?,reply_to_message_id=? WHERE id=?`, u.ID, string(agentJSON), string(userJSON), string(mustAttachmentJSON(files)), core.NullIfEmpty(replyToMessageID), m.ID); err != nil {
		return core.Reply{}, err
	}
	m.AuthorUserID, m.ToAgentIDs, m.ToUserIDs, m.Attachments = u.ID, toAgentIDs, toUserIDs, files
	m.AuthorName, m.ReplyToMessageID = u.Name, replyToMessageID
	if err = s.RunService.AssignMessageTargets(ctx, tx, ss, m.ID, toAgentIDs); err != nil {
		return core.Reply{}, err
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
			return core.Reply{}, err
		}
	}
	if err = s.ProjectStatus(ctx, tx, ss.ID, s.Unix()); err != nil {
		return core.Reply{}, err
	}
	out := core.Reply{Message: &m, Runs: []core.Run{}}
	out.Deliveries, err = s.RunService.DeliveriesIn(ctx, tx, `message_id=?`, m.ID)
	return out, err
}

// recipientIDs preserves the explicit recipient order and stores omitted arrays as [].
// deleteConversationContents coordinates each owner's cleanup in one transaction.
func (s *Service) deleteConversationContents(ctx context.Context, tx *sql.Tx, id string) error {
	if err := s.WorkService.DeleteWorkConversationReferences(ctx, tx, id); err != nil {
		return err
	}
	if err := s.RunService.DeleteConversationExecution(ctx, tx, id); err != nil {
		return err
	}
	for _, query := range []string{`DELETE FROM attachments WHERE conversation_id=?`, `DELETE FROM messages WHERE conversation_id=?`} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}
