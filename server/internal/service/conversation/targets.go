package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/run"
	"github.com/memory-co/agentuse/server/internal/store"
)

// setMessageTargets freezes recipient routes on the message itself. It does not
// allocate a run or input, or create a second delivery business object.
func (s *Service) setMessageTargets(ctx context.Context, tx *sql.Tx, conversation agentuse.Conversation, message string, agents []string) error {
	targets := map[string]run.MessageTarget{}
	for _, id := range agents {
		a, err := store.ScanAgent(tx.QueryRowContext(ctx, store.AgentCols+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if a.UserID != conversation.UserID {
			return store.ErrNotFound
		}
		if err = s.DeviceService.CheckRuntime(ctx, tx, a.DeviceID, a.Harness); err != nil {
			return err
		}
		targets[id] = run.MessageTarget{AgentVersionID: a.CurrentVersionID, DeviceID: a.DeviceID, Harness: a.Harness}
	}
	return s.saveMessageTargets(ctx, tx, message, targets)
}

func (s *Service) messageTargets(ctx context.Context, tx *sql.Tx, message string) (map[string]run.MessageTarget, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT agent_targets FROM messages WHERE id=?`, message).Scan(&raw); err != nil {
		return nil, err
	}
	targets := map[string]run.MessageTarget{}
	err := json.Unmarshal([]byte(raw), &targets)
	return targets, err
}

func (s *Service) saveMessageTargets(ctx context.Context, tx *sql.Tx, message string, targets map[string]run.MessageTarget) error {
	raw, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE messages SET agent_targets=? WHERE id=?`, string(raw), message)
	return err
}

// SaveMessageDispatch freezes the first transmitted envelope for identical replay.
func (s *Service) SaveMessageDispatch(ctx context.Context, tx *sql.Tx, message, agent string, dispatch protocol.ConversationMessage) error {
	targets, err := s.messageTargets(ctx, tx, message)
	if err != nil {
		return err
	}
	target, ok := targets[agent]
	if !ok {
		return store.ErrNotFound
	}
	if target.Dispatch != nil {
		return fmt.Errorf("message dispatch is already fixed")
	}
	target.Dispatch = &dispatch
	targets[agent] = target
	return s.saveMessageTargets(ctx, tx, message, targets)
}

// SaveMessageError stores only a negative receipt. Successful acceptance is
// represented by run_inputs and clears any earlier uncertainty on the message.
func (s *Service) SaveMessageError(ctx context.Context, tx *sql.Tx, message, agent string, receipt *agentuse.MessageError) error {
	targets, err := s.messageTargets(ctx, tx, message)
	if err != nil {
		return err
	}
	target, ok := targets[agent]
	if !ok {
		return store.ErrNotFound
	}
	if receipt != nil && target.Error != nil && target.Error.Uncertain {
		receipt.Uncertain = true
	}
	target.Error = receipt
	targets[agent] = target
	return s.saveMessageTargets(ctx, tx, message, targets)
}

func (s *Service) RetireDeviceMessages(ctx context.Context, tx *sql.Tx, device, reason string) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.conversation_id,t.key,`+store.MessageTargetDispatched+` FROM `+store.MessageTargetsFrom+` WHERE json_extract(t.value,'$.device_id')=? AND `+store.MessageTargetPending, device)
	if err != nil {
		return err
	}
	type target struct {
		message, conversation, agent string
		dispatched                   bool
	}
	var affected []target
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.message, &item.conversation, &item.agent, &item.dispatched); err != nil {
			rows.Close()
			return err
		}
		affected = append(affected, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	conversations := map[string]bool{}
	for _, item := range affected {
		if err = s.SaveMessageError(ctx, tx, item.message, item.agent, &agentuse.MessageError{Error: reason, Uncertain: item.dispatched}); err != nil {
			return err
		}
		conversations[item.conversation] = true
	}
	for id := range conversations {
		if err = s.ProjectStatus(ctx, tx, id, s.Now().Unix()); err != nil {
			return err
		}
	}
	return nil
}

// NotifyDeviceConversations resolves execution ownership before publishing hints.
func (s *Service) NotifyDeviceConversations(ctx context.Context, id string) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT m.conversation_id FROM `+store.MessageTargetsFrom+` WHERE json_extract(t.value,'$.device_id')=?`, id)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var conversation string
		if rows.Scan(&conversation) == nil {
			ids = append(ids, conversation)
		}
	}
	rows.Close()
	for _, conversation := range ids {
		s.NotifyConversation(ctx, conversation)
	}
}
