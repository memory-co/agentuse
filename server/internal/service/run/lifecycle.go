package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

// DispatchMessage selects one durable input. It does not create or predict a run.
func (s *Service) DispatchMessage(ctx context.Context, d agentuse.Device, capacity ...protocol.Message) (protocol.ConversationMessage, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	defer tx.Rollback()
	query := messageTargetCols + ` WHERE json_extract(t.value,'$.device_id')=? AND ` + store.MessageTargetPending + ` AND NOT (` + store.MessageTargetDispatched + `)
      AND EXISTS(SELECT 1 FROM device_harnesses h WHERE h.device_id=json_extract(t.value,'$.device_id') AND h.harness=json_extract(t.value,'$.harness') AND h.available=1 AND h.enabled=1)
      AND NOT EXISTS(SELECT 1 FROM messages waiting JOIN json_each(waiting.agent_targets) w WHERE waiting.conversation_id=m.conversation_id AND w.key=t.key
        AND json_extract(w.value,'$.device_id')=json_extract(t.value,'$.device_id') AND json_extract(w.value,'$.agent_version_id')=json_extract(t.value,'$.agent_version_id')
        AND json_type(w.value,'$.dispatch')='object' AND (json_type(w.value,'$.error') IS NULL OR json_extract(w.value,'$.error.uncertain')=1)
        AND NOT EXISTS(SELECT 1 FROM run_inputs i JOIN runs r ON r.id=i.run_id WHERE i.message_id=waiting.id AND r.agent_id=w.key))`
	args := []any{d.ID}
	if len(capacity) > 0 && capacity[0].FreeSessions <= 0 {
		if len(capacity[0].ActiveStreams) == 0 {
			return protocol.ConversationMessage{}, store.ErrNotFound
		}
		query += ` AND (`
		for i, stream := range capacity[0].ActiveStreams {
			if i > 0 {
				query += ` OR `
			}
			query += `(m.conversation_id=? AND t.key=? AND json_extract(t.value,'$.agent_version_id')=?)`
			args = append(args, stream.ConversationID, stream.AgentID, stream.AgentVersionID)
		}
		query += `)`
	}
	query += ` ORDER BY m.created_at,m.seq,t.key LIMIT 1`
	p, err := scanMessageTarget(tx.QueryRowContext(ctx, query, args...))
	if err != nil {
		return protocol.ConversationMessage{}, err
	}
	out, err := s.ConversationService.PrepareMessage(ctx, tx, p)
	if err != nil {
		return out, err
	}
	if err = s.ConversationService.SaveMessageDispatch(ctx, tx, p.MessageID, p.AgentID, out); err != nil {
		return out, err
	}
	err = tx.Commit()
	if err == nil {
		s.ConversationService.NotifyConversation(ctx, out.ConversationID)
	}
	return out, err
}

// Sent inputs are never automatically executed again after a disconnect.
// Reconnection re-delivers the same prompt identity; the client checks its receipt.
func (s *Service) PendingMessages(ctx context.Context, device string) ([]protocol.ConversationMessage, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT json_extract(t.value,'$.dispatch') FROM `+store.MessageTargetsFrom+` WHERE json_extract(t.value,'$.device_id')=? AND `+store.MessageTargetPending+` AND `+store.MessageTargetDispatched+` ORDER BY m.created_at,m.seq,t.key`, device)
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
		var message protocol.ConversationMessage
		if err = json.Unmarshal([]byte(raw), &message); err != nil {
			return nil, err
		}
		out = append(out, message)
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
	var conversation string
	err = s.Repository.Tx(ctx, func(tx *sql.Tx) error {
		p, err := scanMessageTarget(tx.QueryRowContext(ctx, messageTargetCols+` WHERE m.id=? AND t.key=? AND json_extract(t.value,'$.device_id')=? AND `+store.MessageTargetDispatched, message, agent, device))
		if err != nil {
			return err
		}
		conversation = p.ConversationID
		var accepted int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run_inputs i JOIN runs r ON r.id=i.run_id WHERE i.message_id=? AND r.agent_id=?`, message, agent).Scan(&accepted); err != nil {
			return err
		}
		if accepted > 0 {
			return nil
		}
		if err = s.ConversationService.SaveMessageError(ctx, tx, message, agent, &agentuse.MessageError{Error: reason, Uncertain: status == protocol.AckUnknown}); err != nil {
			return err
		}
		return s.ConversationService.ProjectStatus(ctx, tx, p.ConversationID, s.Now().Unix())
	})
	if err == nil {
		s.ConversationService.DeliveryChanged(ctx, conversation)
	}
	return err
}
func (s *Service) DeviceConversationAccess(ctx context.Context, device, conversation string) error {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+store.MessageTargetsFrom+` WHERE json_extract(t.value,'$.device_id')=? AND m.conversation_id=? AND `+store.MessageTargetDispatched, device, conversation).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
func (s *Service) CancelRun(ctx context.Context, u agentuse.User, conversation, id string) (agentuse.Run, error) {
	r, err := s.GetRun(ctx, u, conversation, id)
	if err != nil {
		return r, err
	}
	if r.Status != "running" && r.Status != "waiting" {
		return r, ErrRunState
	}
	_, err = s.queueControl(ctx, r, "cancel", "", nil, u.ID)
	return r, err
}
func (s *Service) queueControl(ctx context.Context, r agentuse.Run, kind, request string, answer json.RawMessage, user string) (protocol.SessionControl, error) {
	prefix := "c:"
	if kind == protocol.TypeAnswer {
		prefix = "a:"
	}
	out := protocol.SessionControl{ID: prefix + store.NewUUID(), ConversationID: r.ConversationID, SessionID: r.SessionID, RunID: r.ID, RequestID: request, Answer: answer}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO run_controls(id,conversation_id,session_id,run_id,device_id,kind,request_id,answer,created_at,updated_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM run_controls WHERE run_id=? AND kind=? AND request_id=? AND state IN ('pending','unknown','delivered'))`, out.ID, r.ConversationID, r.SessionID, r.ID, r.DeviceID, kind, request, store.NullIfEmpty(string(answer)), s.Now().Unix(), s.Now().Unix(), r.ID, kind, request)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return out, ErrRunState
		}
	}
	if err == nil {
		s.Notifier.PushRun(r.DeviceID, protocol.Message{Type: kind, ID: out.ID, Control: &out})
		s.ConversationService.NotifyConversation(ctx, r.ConversationID)
	}
	return out, err
}
func (s *Service) PendingControls(ctx context.Context, device string) ([]protocol.Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,conversation_id,session_id,run_id,kind,request_id,answer FROM run_controls WHERE device_id=? AND state IN ('pending','unknown') ORDER BY created_at,id`, device)
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
	_, err := s.DB.ExecContext(ctx, `UPDATE run_controls SET state=?,error=?,updated_at=? WHERE id=? AND device_id=? AND state IN ('pending','unknown')`, state, message, s.Now().Unix(), id, device)
	if err == nil {
		var conversation string
		if e := s.DB.QueryRowContext(ctx, `SELECT conversation_id FROM run_controls WHERE id=? AND device_id=?`, id, device).Scan(&conversation); e == nil {
			s.ConversationService.NotifyConversation(ctx, conversation)
		}
	}
	return err
}
