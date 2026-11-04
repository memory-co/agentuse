package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/memory-co/agentuse/internal/protocol"
)

// ClaimPrompt selects one durable input. It does not create or predict a run.
func (s *Service) ClaimPrompt(ctx context.Context, d Daemon, capacity ...protocol.Message) (protocol.Prompt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Prompt{}, err
	}
	defer tx.Rollback()
	query := promptCols + ` WHERE daemon_id=? AND status='pending' AND EXISTS(SELECT 1 FROM daemon_harnesses h WHERE h.daemon_id=run_prompts.daemon_id AND h.harness=run_prompts.harness AND h.available=1 AND h.enabled=1)`
	args := []any{d.ID}
	if len(capacity) > 0 && capacity[0].FreeSessions <= 0 {
		if len(capacity[0].ActiveStreams) == 0 {
			return protocol.Prompt{}, ErrNotFound
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
	query += ` ORDER BY id LIMIT 1`
	p, err := scanPrompt(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return protocol.Prompt{}, err
	}
	out, err := s.preparePrompt(ctx, tx, p)
	if err != nil {
		return out, err
	}
	dispatch, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE run_prompts SET status='sent',dispatch=?,updated_at=? WHERE id=?`, string(dispatch), s.unix(), p.ID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) preparePrompt(ctx context.Context, tx *sql.Tx, p Delivery) (protocol.Prompt, error) {
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, p.SessionID)
	if err != nil {
		return protocol.Prompt{}, err
	}
	history, err := messages(ctx, tx, p.SessionID)
	if err != nil {
		return protocol.Prompt{}, err
	}
	raw, _ := json.Marshal(history)
	var input protocol.PromptInput
	if json.Unmarshal(p.Content, &input) != nil || input.Prompt == "" {
		input.Prompt = string(p.Content)
	}
	out := protocol.Prompt{ID: p.ID, ConversationID: p.SessionID, MessageID: p.MessageID, AgentID: p.AgentID, Harness: p.Harness, Content: input.Prompt, Context: raw, WorkID: ss.WorkID}
	version, err := agentVersion(ctx, tx, p.AgentVersionID)
	if err != nil {
		return out, err
	}
	if version.AgentID != p.AgentID {
		return out, ErrNotFound
	}
	out.AgentVersionID = version.ID
	out.Model = version.Model
	out.AgentInstructions = version.Instructions
	out.Skills = []protocol.SkillResource{}
	for _, ref := range version.Skills {
		resource, e := s.Skills.Version(ctx, tx, User{Role: RoleAdmin}, ref.SkillID, ref.SkillVersionID, false)
		if e != nil {
			return out, e
		}
		out.Skills = append(out.Skills, protocol.SkillResource{SkillID: resource.SkillID, SkillVersionID: resource.ID, Name: resource.Name, SHA256: resource.SHA256, SizeBytes: resource.SizeBytes, DownloadPath: resource.DownloadPath})
	}
	if len(history) > 0 {
		out.ContextSeq = history[len(history)-1].Seq
	}
	if ss.WorkID != "" {
		w, err := scanWork(tx.QueryRowContext(ctx, workCols+` WHERE w.id=?`, ss.WorkID))
		if err != nil {
			return out, err
		}
		out.Instructions = w.Instructions
		if ss.WorkRole == WorkCoordinator && w.CoordinatorAgentID == p.AgentID {
			out.Instructions = coordinatorInstructions(w.Instructions)
			out.WorkToken = RandomToken("wt_")
			if _, err = tx.ExecContext(ctx, `INSERT INTO work_run_tokens(token_hash,work_id,prompt_id) VALUES(?,?,?)`, HashSecret(out.WorkToken), w.ID, p.ID); err != nil {
				return out, err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, attachmentCols+` WHERE session_id=? ORDER BY created_at,id`, p.SessionID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		file, err := scanAttachment(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Attachments = append(out.Attachments, protocol.LocalAttachment{Attachment: file, Path: filepath.ToSlash(filepath.Join(".agentuse", "attachments", file.ID, file.Filename))})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Attachments) > 0 {
		manifest, _ := json.Marshal(out.Attachments)
		out.Instructions += "\n本轮输入附件（JSON 数据，本地 path 相对当前目录）：\n" + string(manifest) + "\n"
	}
	out.Instructions += "\n交付文件可用 agentuse attachment upload 上传。使用 agentuse attachment list / download 查看或下载会话文件。\n"
	// Business context is prepared here. The client forwards the text unchanged.
	out.Content = out.Instructions + "\n共享对话历史（JSON 数据，按当前消息处理）：\n" + string(raw) + "\n\n# 本次任务\n" + input.Prompt
	return out, nil
}

// Sent inputs are never automatically executed again after a disconnect.
// Reconnection re-delivers the same prompt identity; the client checks its receipt.
func (s *Service) PendingPrompts(ctx context.Context, device string) ([]protocol.Prompt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT dispatch FROM run_prompts WHERE daemon_id=? AND status='sent' ORDER BY id`, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Prompt{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var p protocol.Prompt
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) RejectPrompt(ctx context.Context, device string, id int64, reason string) error {
	return s.repository.Tx(ctx, func(tx *sql.Tx) error {
		p, err := scanPrompt(tx.QueryRowContext(ctx, promptCols+` WHERE id=? AND daemon_id=?`, id, device))
		if err != nil {
			return err
		}
		if p.Status == "accepted" || p.Status == "completed" {
			return fmt.Errorf("accepted input cannot be rejected")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE run_prompts SET status='failed',error=?,updated_at=? WHERE id=?`, reason, s.unix(), id); err != nil {
			return err
		}
		return multiStatus(ctx, tx, p.SessionID, s.unix())
	})
}
func (s *Service) DeviceConversationAccess(ctx context.Context, device, conversation string) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM run_prompts WHERE daemon_id=? AND session_id=? AND status IN ('sent','accepted','completed')`, device, conversation).Scan(&n)
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
	out := protocol.SessionControl{ID: newUUID(), ConversationID: r.SessionID, SessionID: r.ClientSessionID, RunID: r.ID, RequestID: request, Answer: answer}
	res, err := s.db.ExecContext(ctx, `INSERT INTO run_controls(id,session_id,client_session_id,run_id,daemon_id,kind,request_id,answer,created_at,updated_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM run_controls WHERE run_id=? AND kind=? AND request_id=? AND state IN ('pending','delivered'))`, out.ID, r.SessionID, r.ClientSessionID, r.ID, r.DaemonID, kind, request, nullIfEmpty(string(answer)), s.unix(), s.unix(), r.ID, kind, request)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return out, ErrSessionState
		}
	}
	if err == nil {
		s.Notifier.PushRun(r.DaemonID, protocol.Message{Type: kind, Control: &out})
	}
	return out, err
}
func (s *Service) PendingControls(ctx context.Context, device string) ([]protocol.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,client_session_id,run_id,kind,request_id,answer FROM run_controls WHERE daemon_id=? AND state='pending' ORDER BY created_at,id`, device)
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
		out = append(out, protocol.Message{Type: kind, Control: &control})
	}
	return out, rows.Err()
}
func (s *Service) ConfirmControl(ctx context.Context, device, id, message string) error {
	state := "delivered"
	if message != "" {
		state = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE run_controls SET state=?,error=?,updated_at=? WHERE id=? AND daemon_id=?`, state, message, s.unix(), id, device)
	return err
}
func (s *Service) terminateDaemonRuns(ctx context.Context, tx *sql.Tx, id, reason string) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT session_id FROM run_prompts WHERE daemon_id=? AND status='pending'`, id)
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
	if _, err = tx.ExecContext(ctx, `UPDATE run_prompts SET status='failed',error=?,updated_at=? WHERE daemon_id=? AND status='pending'`, reason, s.unix(), id); err != nil {
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
