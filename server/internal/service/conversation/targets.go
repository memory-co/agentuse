package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/access"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/service/run"
)

// setMessageTargets freezes recipient routes on the message itself. It does not
// allocate a run or input, or create a second delivery business object.
func (s *Service) setMessageTargets(ctx context.Context, tx *gorm.DB, conversation agentuse.Conversation, message string, agents []string) error {
	if err := s.checkExecutionCredential(ctx, tx); err != nil {
		return err
	}

	targets := map[string]run.MessageTarget{}
	for _, id := range agents {
		a, err := database.ScanAgent(database.Row(ctx, tx, database.AgentCols+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if a.UserID != conversation.UserID {
			return database.ErrNotFound
		}
		if err = s.DeviceService.CheckRuntime(ctx, tx, a.DeviceID, a.Harness); err != nil {
			return err
		}
		targets[id] = run.MessageTarget{AgentVersionID: a.CurrentVersionID, DeviceID: a.DeviceID, Harness: a.Harness, AccessLimit: access.Limit(ctx)}
	}
	return s.saveMessageTargets(ctx, tx, message, targets)
}

func (s *Service) messageTargets(ctx context.Context, tx *gorm.DB, message string) (map[string]run.MessageTarget, error) {
	var raw string
	if err := database.Row(ctx, tx, `SELECT agent_targets FROM messages WHERE id=?`, message).Scan(&raw); err != nil {
		return nil, err
	}
	targets := map[string]run.MessageTarget{}
	err := json.Unmarshal([]byte(raw), &targets)
	return targets, err
}

func (s *Service) saveMessageTargets(ctx context.Context, tx *gorm.DB, message string, targets map[string]run.MessageTarget) error {
	raw, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = database.Exec(ctx, tx, `UPDATE messages SET agent_targets=? WHERE id=?`, string(raw), message)
	return err
}

// SaveMessageDispatch freezes the first transmitted envelope for identical replay.
func (s *Service) SaveMessageDispatch(ctx context.Context, tx *gorm.DB, message, agent string, dispatch protocol.ConversationMessage) error {
	targets, err := s.messageTargets(ctx, tx, message)
	if err != nil {
		return err
	}
	target, ok := targets[agent]
	if !ok {
		return database.ErrNotFound
	}
	if target.Dispatch != nil {
		return fmt.Errorf("message dispatch is already fixed")
	}
	if s.AccessSigner != nil {
		var generation int64
		if err = database.Row(ctx, tx, `SELECT credential_generation FROM devices WHERE id=?`, target.DeviceID).Scan(&generation); err != nil {
			return err
		}
		prepare := s.PrepareTimeout
		if prepare <= 0 {
			prepare = 30 * time.Minute
		}
		window := s.AccessTimeout
		if window <= 0 {
			window = 24 * time.Hour
		}
		now := s.Now().Unix()
		limit := target.AccessLimit
		if limit == 0 {
			limit = now + int64((prepare+window)/time.Second)
		}
		claims, e := s.AccessSigner.Prepare(access.Claims{DeviceID: target.DeviceID, Generation: generation, ConversationID: dispatch.ConversationID, MessageID: message, AgentID: agent, AgentVersionID: target.AgentVersionID, BindBefore: min(now+int64(prepare/time.Second), limit), AccessLimit: limit})
		if e != nil {
			return e
		}
		target.AccessClaims = &claims
	}
	dispatch.Access = nil
	target.Dispatch = &dispatch
	targets[agent] = target
	return s.saveMessageTargets(ctx, tx, message, targets)
}

// SaveMessageError stores only a negative receipt. Successful acceptance is
// represented by run_inputs and clears any earlier uncertainty on the message.
func (s *Service) SaveMessageError(ctx context.Context, tx *gorm.DB, message, agent string, receipt *agentuse.MessageError) error {
	targets, err := s.messageTargets(ctx, tx, message)
	if err != nil {
		return err
	}
	target, ok := targets[agent]
	if !ok {
		return database.ErrNotFound
	}
	if receipt != nil && target.Error != nil && target.Error.Uncertain {
		receipt.Uncertain = true
	}
	target.Error = receipt
	targets[agent] = target
	return s.saveMessageTargets(ctx, tx, message, targets)
}

func (s *Service) RetireDeviceMessages(ctx context.Context, tx *gorm.DB, device, reason string) error {
	tq := database.MessageTargets(s.DB, "m", "t")

	rows, err := database.Rows(ctx, tx, `SELECT m.id,m.conversation_id,`+tq.Key+`,`+tq.Dispatched+` FROM `+tq.From+` WHERE `+tq.Device+`=? AND `+tq.Pending, device)
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
	tq := database.MessageTargets(s.DB, "m", "t")

	rows, err := database.Rows(ctx, s.DB, `SELECT DISTINCT m.conversation_id FROM `+tq.From+` WHERE `+tq.Device+`=?`, id)
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

func (s *Service) checkExecutionCredential(ctx context.Context, q *gorm.DB) error {
	if token := access.Credential(ctx); token != "" {
		p, err := s.RunService.AccessForRequestIn(ctx, q, token)
		if err != nil || p.Preparing {
			return run.ErrAccess
		}
	}
	return nil
}
