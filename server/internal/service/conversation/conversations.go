package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/google/uuid"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	runservice "github.com/memory-co/agentuse/server/internal/service/run"
)

func (s *Service) insertMessage(ctx context.Context, tx *gorm.DB, conversationID, role string, content string, now int64) (sdk.Message, error) {
	var seq int64
	if err := database.Row(ctx, tx, "SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conversation_id=?", conversationID).Scan(&seq); err != nil {
		return sdk.Message{}, err
	}
	id := uuid.NewString()
	_, err := database.Exec(ctx, tx, database.InsertMessageInsert, id, seq, conversationID, role, content, now)
	if err != nil {
		return sdk.Message{}, err
	}
	return sdk.Message{ID: id, Seq: seq, ConversationID: conversationID, AuthorRole: role, Content: content, CreatedAt: now, ToAgentIDs: []string{}, ToUserIDs: []string{}, Attachments: []sdk.Attachment{}}, nil
}

func (s *Service) scanConversation(row database.Scanner) (sdk.Conversation, error) {
	return database.ScanConversation(row)
}

func (s *Service) LookupConversation(ctx context.Context, q *gorm.DB, u sdk.User, id string) (sdk.Conversation, error) {
	query := database.ConversationCols + database.GetConversationClause
	args := []any{id}
	if !u.IsAdmin() {
		query += database.GetConversationClause2
		args = append(args, u.ID, u.ID)
	}
	return s.scanConversation(database.Row(ctx, q, query, args...))
}

func (s *Service) GetConversation(ctx context.Context, u sdk.User, id string) (sdk.Conversation, error) {
	return s.LookupConversation(ctx, s.DB, u, id)
}

// ListConversations lists visible top-level conversations.
func (s *Service) ListConversations(ctx context.Context, u sdk.User, limit int) ([]sdk.Conversation, error) {
	q := database.ConversationCols + database.ListConversationsClause
	var args []any
	if !u.IsAdmin() {
		q += database.ListConversationsClause2
		args = append(args, u.ID, u.ID)
	}
	return s.listConversations(ctx, q, limit, args...)
}

// ListChildren lists only direct children of an existing, authorized parent.
func (s *Service) ListChildren(ctx context.Context, u sdk.User, parent string, limit int) ([]sdk.Conversation, error) {
	if _, err := s.GetConversation(ctx, u, parent); err != nil {
		return nil, err
	}
	return s.listConversations(ctx, database.ConversationCols+` WHERE parent_conversation_id=?`, limit, parent)
}

func (s *Service) listConversations(ctx context.Context, q string, limit int, args ...any) ([]sdk.Conversation, error) {
	q += database.ListConversationsClause3
	args = append(args, limit)
	rows, err := database.Rows(ctx, s.DB, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sdk.Conversation{}
	for rows.Next() {
		ss, err := s.scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Service) DeleteConversation(ctx context.Context, u sdk.User, id string) error {
	return s.deleteConversation(ctx, u, id, false)
}
func (s *Service) DeleteTree(ctx context.Context, u sdk.User, id string) error {
	return s.deleteConversation(ctx, u, id, true)
}
func (s *Service) deleteConversation(ctx context.Context, u sdk.User, id string, recursive bool) error {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	root, err := s.LookupConversation(ctx, tx, u, id)
	if err != nil {
		return err
	}
	children := []sdk.Conversation{}
	rows, err := database.Rows(ctx, tx, database.ConversationCols+` WHERE parent_conversation_id=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		v, e := s.scanConversation(rows)
		if e != nil {
			rows.Close()
			return e
		}
		children = append(children, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(children) > 0 && !recursive {
		return ErrConversationChildren
	}
	nodes := append(children, root)
	for _, v := range nodes {
		if _, err = s.LookupConversation(ctx, tx, u, v.ID); err != nil {
			return err
		}
		if v.Status == sdk.ConversationRunning || v.Status == sdk.ConversationWaiting || v.Status == sdk.ConversationPending {
			return ErrConversationBusy
		}
		if v.ParentConversationID != "" && v.ParentConversationID != root.ID {
			var pending int
			if err = database.Row(ctx, tx, `SELECT count(*) FROM conversation_rounds r WHERE r.conversation_id=? AND r.finished_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=? AND m.author_conversation_id=r.conversation_id AND m.author_round_seq=r.seq)`, v.ID, v.ParentConversationID).Scan(&pending); err != nil {
				return err
			}
			if pending > 0 {
				return ErrConversationBusy
			}
		}
	}
	for _, v := range nodes {
		if err = s.deleteConversationContents(ctx, tx, v.ID); err != nil {
			return err
		}
		if _, err = database.Exec(ctx, tx, database.DeleteConversationDelete, v.ID); err != nil {
			return err
		}
	}
	if err = tx.Commit().Error; err != nil {
		return err
	}
	for _, v := range nodes {
		s.PublishConversationChange(v, "conversations")
		s.CleanupFiles(ctx, v.ID)
	}
	return nil
}

func (s *Service) Messages(ctx context.Context, conversationID string) ([]sdk.Message, error) {
	return s.messages(ctx, s.DB, conversationID)
}

func (s *Service) messages(ctx context.Context, q *gorm.DB, conversationID string) ([]sdk.Message, error) {
	rows, err := database.Rows(ctx, q, database.MessageCols+database.MessagesClause, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sdk.Message{}
	for rows.Next() {
		m, err := database.ScanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) ProjectStatus(ctx context.Context, tx *gorm.DB, conversation string, now int64) error {
	tq := database.MessageTargets(s.DB, "m", "t")

	_, err := database.Exec(ctx, tx, `UPDATE conversations SET status=CASE WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='waiting') THEN 'waiting' WHEN EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='running') THEN 'running' WHEN EXISTS(SELECT 1 FROM `+tq.From+` WHERE m.conversation_id=? AND `+tq.Pending+`) THEN 'pending' WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=?`, conversation, conversation, conversation, now, conversation)
	if err != nil {
		return err
	}
	return s.finishRound(ctx, tx, conversation, now)
}

func (s *Service) createConversation(ctx context.Context, u sdk.User, parent, title, input string, toAgentIDs, toUserIDs []string, mode string, source *runservice.AgentSource) (sdk.Conversation, *sdk.Reply, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return sdk.Conversation{}, nil, err
	}
	defer tx.Rollback()
	if mode == "" {
		mode = sdk.ConversationModeSingle
	}
	if mode != sdk.ConversationModeSingle && mode != sdk.ConversationModeCollaborative {
		return sdk.Conversation{}, nil, fmt.Errorf("mode must be single or collaborative")
	}
	v := sdk.Conversation{Mode: mode, ID: uuid.NewString(), UserID: u.ID, CreatedBy: u.ID, Title: title, Status: sdk.ConversationIdle, CreatedAt: s.Now().Unix(), UpdatedAt: s.Now().Unix()}
	if source != nil {
		if parent == "" || parent != source.ConversationID || source.User.ID != u.ID {
			return v, nil, ErrConversationScope
		}
		p, err := s.LookupConversation(ctx, tx, u, parent)
		if err != nil {
			return v, nil, err
		}
		if p.ParentConversationID != "" || p.Mode != sdk.ConversationModeCollaborative || mode != sdk.ConversationModeSingle {
			return v, nil, ErrConversationScope
		}
		v.UserID = p.UserID
		v.ParentConversationID = p.ID
		v.ParentAgentID = source.AgentID
	} else if parent != "" {
		return v, nil, ErrConversationScope
	}
	if _, err = database.Exec(ctx, tx, database.InsertConversationInsert, v.ID, v.UserID, v.CreatedBy, database.NullIfEmpty(v.ParentConversationID), database.NullIfEmpty(v.ParentAgentID), mode, title, v.CreatedAt, v.UpdatedAt); err != nil {
		return v, nil, err
	}
	if input == "" && (len(toAgentIDs) > 0 || len(toUserIDs) > 0) {
		return v, nil, fmt.Errorf("message required when recipients are specified")
	}
	var reply *sdk.Reply
	if input != "" {
		r, e := s.SendMessageTx(ctx, tx, u, v, input, toAgentIDs, toUserIDs, nil, "")
		if e != nil {
			return v, nil, e
		}
		if source != nil {
			if e = s.setAgentAuthor(ctx, tx, r.Message, source); e != nil {
				return v, nil, e
			}
		}
		reply = &r
		v, e = s.LookupConversation(ctx, tx, u, v.ID)
		if e != nil {
			return v, nil, e
		}
	}
	return v, reply, tx.Commit().Error
}

// CheckAgentAccess confines a verified execution to its own metadata and direct children.
func (s *Service) CheckAgentAccess(ctx context.Context, source runservice.AgentSource, target string, write bool) error {
	return s.checkAgentAccessIn(ctx, s.DB, source, target, write)
}
func (s *Service) checkAgentAccessIn(ctx context.Context, q *gorm.DB, source runservice.AgentSource, target string, write bool) error {
	from, err := s.LookupConversation(ctx, q, source.User, source.ConversationID)
	if err != nil {
		return err
	}
	if target == from.ID && !write {
		return nil
	}
	if from.ParentConversationID != "" || from.Mode != sdk.ConversationModeCollaborative {
		return ErrConversationScope
	}
	dest, err := s.LookupConversation(ctx, q, source.User, target)
	if err != nil {
		return err
	}
	if dest.ParentConversationID != from.ID {
		return ErrConversationScope
	}
	return nil
}
func (s *Service) SendAgentMessage(ctx context.Context, source runservice.AgentSource, target, input string, agents, users, attachments []string, replyTo string) (sdk.Reply, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return sdk.Reply{}, err
	}
	defer tx.Rollback()
	if err = s.checkAgentAccessIn(ctx, tx, source, target, true); err != nil {
		return sdk.Reply{}, err
	}
	v, err := s.LookupConversation(ctx, tx, source.User, target)
	if err != nil {
		return sdk.Reply{}, err
	}
	r, err := s.SendMessageTx(ctx, tx, source.User, v, input, agents, users, attachments, replyTo)
	if err != nil {
		return r, err
	}
	if err = s.setAgentAuthor(ctx, tx, r.Message, &source); err != nil {
		return r, err
	}
	if err = tx.Commit().Error; err != nil {
		return r, err
	}
	s.NotifyConversation(ctx, target)
	s.Notifier.KickAll()
	return r, nil
}
func (s *Service) setAgentAuthor(ctx context.Context, tx *gorm.DB, m *sdk.Message, source *runservice.AgentSource) error {
	if _, err := database.Exec(ctx, tx, `UPDATE messages SET author_role='agent',author_user_id=NULL,author_agent_id=?,author_agent_version_id=? WHERE id=?`, source.AgentID, source.AgentVersionID, m.ID); err != nil {
		return err
	}
	m.AuthorRole = sdk.RoleAgentMsg
	m.AuthorUserID = ""
	m.AuthorAgentID = source.AgentID
	m.AuthorAgentVersionID = source.AgentVersionID
	return database.Row(ctx, tx, `SELECT name FROM agent_versions WHERE id=? AND agent_id=?`, source.AgentVersionID, source.AgentID).Scan(&m.AuthorName)
}

func (s *Service) sendMessage(ctx context.Context, u sdk.User, conversation string, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (sdk.Reply, error) {
	tx, err := database.Begin(ctx, s.DB)
	if err != nil {
		return sdk.Reply{}, err
	}
	defer tx.Rollback()
	ss, err := s.LookupConversation(ctx, tx, u, conversation)
	if err != nil {
		return sdk.Reply{}, err
	}
	out, err := s.SendMessageTx(ctx, tx, u, ss, input, toAgentIDs, toUserIDs, attachmentIDs, replyToMessageID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit().Error
}
func (s *Service) SendMessageTx(ctx context.Context, tx *gorm.DB, u sdk.User, ss sdk.Conversation, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (sdk.Reply, error) {
	if !utf8.ValidString(input) || (strings.TrimSpace(input) == "" && len(attachmentIDs) == 0) {
		return sdk.Reply{}, fmt.Errorf("message text or attachments required")
	}
	if replyToMessageID != "" {
		if uuid.Validate(replyToMessageID) != nil {
			return sdk.Reply{}, fmt.Errorf("invalid reply_to_message_id")
		}
		var found int
		if err := database.Row(ctx, tx, `SELECT 1 FROM messages WHERE id=? AND conversation_id=?`, replyToMessageID, ss.ID).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return sdk.Reply{}, database.ErrNotFound
			}
			return sdk.Reply{}, err
		}
	}
	toAgentIDs, err := recipientIDs(toAgentIDs)
	if err != nil {
		return sdk.Reply{}, err
	}
	toUserIDs, err = recipientIDs(toUserIDs)
	if err != nil {
		return sdk.Reply{}, err
	}
	for _, id := range toUserIDs {
		recipient, err := database.ScanUser(database.Row(ctx, tx, database.GetUserSelect, id))
		if err != nil {
			return sdk.Reply{}, err
		}
		if _, err = s.LookupConversation(ctx, tx, recipient, ss.ID); err != nil {
			return sdk.Reply{}, err
		}
	}
	files, err := s.messageAttachments(ctx, tx, ss.ID, attachmentIDs)
	if err != nil {
		return sdk.Reply{}, err
	}

	m, err := s.insertMessage(ctx, tx, ss.ID, sdk.RoleUserMsg, input, s.Now().Unix())
	if err != nil {
		return sdk.Reply{}, err
	}
	agentJSON, _ := json.Marshal(toAgentIDs)
	userJSON, _ := json.Marshal(toUserIDs)
	if _, err = database.Exec(ctx, tx, `UPDATE messages SET author_user_id=?,to_agent_ids=?,to_user_ids=?,attachments=?,reply_to_message_id=? WHERE id=?`, u.ID, string(agentJSON), string(userJSON), string(mustAttachmentJSON(files)), database.NullIfEmpty(replyToMessageID), m.ID); err != nil {
		return sdk.Reply{}, err
	}
	m.AuthorUserID, m.ToAgentIDs, m.ToUserIDs, m.Attachments = u.ID, toAgentIDs, toUserIDs, files
	m.AuthorName, m.ReplyToMessageID = u.Name, replyToMessageID
	if err = s.setMessageTargets(ctx, tx, ss, m.ID, toAgentIDs); err != nil {
		return sdk.Reply{}, err
	}
	title := []rune(strings.TrimSpace(input))
	if len(title) == 0 && len(files) > 0 {
		title = []rune(files[0].Filename)
	}
	if len(title) > 50 {
		title = title[:50]
	}
	if ss.Title == "" {
		if _, err = database.Exec(ctx, tx, `UPDATE conversations SET title=? WHERE id=?`, string(title), ss.ID); err != nil {
			return sdk.Reply{}, err
		}
	}
	out := sdk.Reply{Message: &m, Runs: []sdk.Run{}, RunInputs: []sdk.RunInput{}}
	if len(toAgentIDs) > 0 {
		if out.Round, err = s.startRound(ctx, tx, ss.ID, m.Seq); err != nil {
			return sdk.Reply{}, err
		}
	}
	if err = s.ProjectStatus(ctx, tx, ss.ID, s.Now().Unix()); err != nil {
		return sdk.Reply{}, err
	}
	return out, nil
}

// recipientIDs preserves the explicit recipient order and stores omitted arrays as [].
// deleteConversationContents coordinates each owner's cleanup in one transaction.
func (s *Service) deleteConversationContents(ctx context.Context, tx *gorm.DB, id string) error {
	if err := s.RunService.DeleteConversationExecution(ctx, tx, id); err != nil {
		return err
	}
	for _, query := range []string{`DELETE FROM conversation_rounds WHERE conversation_id=?`, `DELETE FROM attachments WHERE conversation_id=?`, `DELETE FROM messages WHERE conversation_id=?`} {
		if _, err := database.Exec(ctx, tx, query, id); err != nil {
			return err
		}
	}
	return nil
}

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
