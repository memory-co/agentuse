package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

// DispatchMessage selects one durable input. It does not create or predict a run.
func (s *Service) DispatchMessage(ctx context.Context, d Daemon, capacity ...protocol.Message) (protocol.ConversationMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	defer tx.Rollback()
	query := messageTargetCols + ` WHERE daemon_id=? AND status='pending' AND EXISTS(SELECT 1 FROM daemon_harnesses h WHERE h.daemon_id=message_agents.daemon_id AND h.harness=message_agents.harness AND h.available=1 AND h.enabled=1) AND NOT EXISTS(SELECT 1 FROM message_agents waiting WHERE waiting.daemon_id=message_agents.daemon_id AND waiting.session_id=message_agents.session_id AND waiting.agent_id=message_agents.agent_id AND waiting.agent_version_id=message_agents.agent_version_id AND waiting.status IN ('sent','unknown'))`
	args := []any{d.ID}
	if len(capacity) > 0 && capacity[0].FreeSessions <= 0 {
		if len(capacity[0].ActiveStreams) == 0 {
			return protocol.ConversationMessage{}, ErrNotFound
		}
		query += ` AND (`
		for i, stream := range capacity[0].ActiveStreams {
			if i > 0 {
				query += ` OR `
			}
			query += `(session_id=? AND agent_id=? AND agent_version_id=?)`
			args = append(args, stream.ConversationID, stream.AgentID, stream.AgentVersionID)
		}
		query += `)`
	}
	query += ` ORDER BY created_at,(SELECT seq FROM messages WHERE id=message_agents.message_id),agent_id LIMIT 1`
	p, err := scanMessageTarget(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	out, err := s.prepareMessage(ctx, tx, p)
	if err != nil {
		return out, err
	}
	dispatch, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE message_agents SET status='sent',dispatch=?,updated_at=? WHERE message_id=? AND agent_id=?`, string(dispatch), s.unix(), p.MessageID, p.AgentID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) prepareMessage(ctx context.Context, tx *sql.Tx, p Delivery) (protocol.ConversationMessage, error) {
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, p.SessionID)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	history, err := messages(ctx, tx, p.SessionID)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	raw, _ := json.Marshal(history)
	var input protocol.PromptInput
	if json.Unmarshal(p.Content, &input) != nil || input.Prompt == "" {
		input.Prompt = string(p.Content)
	}
	out := protocol.ConversationMessage{ConversationID: p.SessionID, MessageID: p.MessageID, AgentID: p.AgentID, AgentVersionID: p.AgentVersionID, Content: input.Prompt, Attachments: []protocol.LocalAttachment{}}
	instructions := ""
	if ss.WorkID != "" {
		w, err := scanWork(tx.QueryRowContext(ctx, workCols+` WHERE w.id=?`, ss.WorkID))
		if err != nil {
			return out, err
		}
		instructions = w.Instructions
		if ss.WorkRole == WorkCoordinator && w.CoordinatorAgentID == p.AgentID {
			instructions = coordinatorInstructions(w.Instructions)
		}
	}
	source, err := store.ScanMessage(tx.QueryRowContext(ctx, messageCols+` WHERE id=? AND session_id=?`, p.MessageID, p.SessionID))
	if err != nil {
		return out, err
	}
	for _, file := range source.Attachments {
		out.Attachments = append(out.Attachments, protocol.LocalAttachment{Attachment: file, Path: filepath.ToSlash(filepath.Join(".agentuse", "attachments", file.ID, file.Filename))})
	}
	if strings.TrimSpace(input.Prompt) == "" && len(source.Attachments) > 0 {
		out.Content = "请查看本条消息的附件。"
	}

	if len(out.Attachments) > 0 {
		manifest, _ := json.Marshal(out.Attachments)
		instructions += "\n本轮输入附件（JSON 数据，本地 path 相对当前目录）：\n" + string(manifest) + "\n"
	}
	instructions += "\n输入文件可用 context attachment list 查看，再用 context attachment read FILE_ID 取得已校验的本地路径。图片使用原生看图工具读取。输出文件用 context attachment upload FILE 上传，在回复中引用返回的 download_path；上传不会结束执行。\n"
	// Business context is prepared here. The client forwards the text unchanged.
	out.Context = instructions + "\n共享对话历史（JSON 数据，按当前消息处理）：\n" + string(raw)
	return out, nil
}

// Sent inputs are never automatically executed again after a disconnect.
// Reconnection re-delivers the same prompt identity; the client checks its receipt.
func (s *Service) PendingMessages(ctx context.Context, device string) ([]protocol.ConversationMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT dispatch FROM message_agents WHERE daemon_id=? AND status IN ('sent','unknown') ORDER BY created_at,(SELECT seq FROM messages WHERE id=message_agents.message_id),agent_id`, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ConversationMessage{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p protocol.ConversationMessage
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) RejectMessage(ctx context.Context, device, id, status, reason string) error {
	message, agent, err := protocol.ParseMessageID(id)
	if err != nil {
		return err
	}
	if status != protocol.AckError && status != protocol.AckUnknown {
		return fmt.Errorf("invalid message acknowledgement")
	}
	return s.repository.Tx(ctx, func(tx *sql.Tx) error {
		p, err := scanMessageTarget(tx.QueryRowContext(ctx, messageTargetCols+` WHERE message_id=? AND agent_id=? AND daemon_id=?`, message, agent, device))
		if err != nil {
			return err
		}
		if p.RunID != "" {
			return nil // A late transport error cannot override accepted facts.
		}
		if p.Status != "sent" && p.Status != "unknown" && p.Status != "rejected" {
			return fmt.Errorf("message was not delivered")
		}
		state := "rejected"
		if status == protocol.AckUnknown || p.Status == "unknown" {
			state = "unknown"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE message_agents SET status=?,error=?,updated_at=? WHERE message_id=? AND agent_id=?`, state, reason, s.unix(), message, agent); err != nil {
			return err
		}
		if err = s.notifyRunMessages(ctx, tx, p.SessionID); err != nil {
			return err
		}
		return multiStatus(ctx, tx, p.SessionID, s.unix())
	})
}
func (s *Service) DeviceConversationAccess(ctx context.Context, device, conversation string) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM message_agents WHERE daemon_id=? AND session_id=? AND status IN ('sent','unknown','accepted','completed','failed','cancelled')`, device, conversation).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) cancelRun(ctx context.Context, u User, conversation, id string) (Run, error) {
	r, err := s.GetRun(ctx, u, conversation, id)
	if err != nil {
		return r, err
	}
	if r.Status != "running" && r.Status != "waiting" {
		return r, ErrSessionState
	}
	_, err = s.queueControl(ctx, r, "cancel", "", nil, u.ID)
	return r, err
}
func (s *Service) queueControl(ctx context.Context, r Run, kind, request string, answer json.RawMessage, user string) (protocol.SessionControl, error) {
	prefix := "c:"
	if kind == protocol.TypeAnswer {
		prefix = "a:"
	}
	out := protocol.SessionControl{ID: prefix + newUUID(), ConversationID: r.SessionID, SessionID: r.ClientSessionID, RunID: r.ID, RequestID: request, Answer: answer}
	res, err := s.db.ExecContext(ctx, `INSERT INTO run_controls(id,session_id,client_session_id,run_id,daemon_id,kind,request_id,answer,created_at,updated_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM run_controls WHERE run_id=? AND kind=? AND request_id=? AND state IN ('pending','unknown','delivered'))`, out.ID, r.SessionID, r.ClientSessionID, r.ID, r.DaemonID, kind, request, nullIfEmpty(string(answer)), s.unix(), s.unix(), r.ID, kind, request)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return out, ErrSessionState
		}
	}
	if err == nil {
		s.Notifier.PushRun(r.DaemonID, protocol.Message{Type: kind, ID: out.ID, Control: &out})
	}
	return out, err
}
func (s *Service) PendingControls(ctx context.Context, device string) ([]protocol.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,client_session_id,run_id,kind,request_id,answer FROM run_controls WHERE daemon_id=? AND state IN ('pending','unknown') ORDER BY created_at,id`, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Message{}
	for rows.Next() {
		var control protocol.SessionControl
		var kind string
		var answer sql.NullString
		if err = rows.Scan(&control.ID, &control.ConversationID, &control.SessionID, &control.RunID, &kind, &control.RequestID, &answer); err != nil {
			return nil, err
		}
		if answer.Valid {
			control.Answer = json.RawMessage(answer.String)
		}
		out = append(out, protocol.Message{Type: kind, ID: control.ID, Control: &control})
	}
	return out, rows.Err()
}
func (s *Service) ConfirmControl(ctx context.Context, device, id, status, message string) error {
	state := "delivered"
	if status == protocol.AckError {
		state = "failed"
	} else if status == protocol.AckUnknown {
		state = "unknown"
	} else if status != protocol.AckOK {
		return fmt.Errorf("invalid control ACK")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE run_controls SET state=?,error=?,updated_at=? WHERE id=? AND daemon_id=? AND state IN ('pending','unknown')`, state, message, s.unix(), id, device)
	return err
}
func (s *Service) terminateDaemonRuns(ctx context.Context, tx *sql.Tx, id, reason string) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT session_id FROM message_agents WHERE daemon_id=? AND status='pending'`, id)
	if err != nil {
		return err
	}
	var conversations []string
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		conversations = append(conversations, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE message_agents SET status='failed',error=?,updated_at=? WHERE daemon_id=? AND status='pending'`, reason, s.unix(), id); err != nil {
		return err
	}
	for _, c := range conversations {
		if err = multiStatus(ctx, tx, c, s.unix()); err != nil {
			return err
		}
	}
	return nil
}

// Connection loss changes device presence only. Client result remains authoritative.

func runRuntime(ctx context.Context, q querier, a Agent) error {
	return runtimeErr(ctx, q, a.DaemonID, a.Harness)
}
