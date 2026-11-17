package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/access"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/database"
)

var ErrAccess = errors.New("execution access is unavailable, expired or outside its scope")

// AccessPrincipal is derived by Server, never from HTTP source assertions.
type AccessPrincipal struct {
	Claims    access.Claims
	DeviceID  string
	UserID    string
	RunID     string
	SessionID string
	ExpiresAt int64
	Preparing bool
}

func (s *Service) accessTarget(ctx context.Context, q *gorm.DB, message, agent string) (MessageTarget, error) {
	var raw string
	if err := database.Row(ctx, q, `SELECT agent_targets FROM messages WHERE id=?`, message).Scan(&raw); err != nil {
		return MessageTarget{}, err
	}
	targets := map[string]MessageTarget{}
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return MessageTarget{}, err
	}
	t, ok := targets[agent]
	if !ok || t.Dispatch == nil || t.AccessClaims == nil {
		return t, ErrAccess
	}
	return t, nil
}

func (s *Service) verifyAccessSource(ctx context.Context, q *gorm.DB, token string) (access.Claims, MessageTarget, string, error) {
	if s.AccessSigner == nil {
		return access.Claims{}, MessageTarget{}, "", ErrAccess
	}
	c, err := s.AccessSigner.Verify(token)
	if err != nil {
		return c, MessageTarget{}, "", ErrAccess
	}
	t, err := s.accessTarget(ctx, q, c.MessageID, c.AgentID)
	if err != nil || *t.AccessClaims != c || t.DeviceID != c.DeviceID || t.AgentVersionID != c.AgentVersionID || t.Dispatch.ConversationID != c.ConversationID {
		return c, t, "", ErrAccess
	}
	var generation int64
	var user string
	if err = database.Row(ctx, q, `SELECT credential_generation,user_id FROM devices WHERE id=?`, c.DeviceID).Scan(&generation, &user); err != nil || generation != c.Generation {
		return c, t, "", ErrAccess
	}
	return c, t, user, nil
}

// AccessForRequest checks accepted membership before considering preparation.
// A terminal input can never regain its initialization privileges.
func (s *Service) AccessForRequest(ctx context.Context, token string) (AccessPrincipal, error) {
	return s.AccessForRequestIn(ctx, s.DB, token)
}

func (s *Service) AccessForRequestIn(ctx context.Context, q *gorm.DB, token string) (AccessPrincipal, error) {
	c, t, user, err := s.verifyAccessSource(ctx, q, token)
	if err != nil {
		return AccessPrincipal{}, err
	}
	p := AccessPrincipal{Claims: c, DeviceID: c.DeviceID, UserID: user}
	var r database.Run
	err = q.WithContext(ctx).Table("runs").Select("runs.*").Joins("JOIN run_inputs i ON i.run_id=runs.id").Where("i.message_id=? AND runs.agent_id=?", c.MessageID, c.AgentID).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := s.Now().Unix()
		if now >= c.BindBefore || now >= c.AccessLimit || (t.Error != nil && !t.Error.Uncertain) {
			return p, ErrAccess
		}
		p.Preparing = true
		p.ExpiresAt = c.BindBefore
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if r.AccessTokenHash == nil || r.AccessExpiresAt == nil || !access.EqualHash(token, *r.AccessTokenHash) || s.Now().Unix() >= *r.AccessExpiresAt || r.Status != "running" && r.Status != "waiting" || r.DeviceID != c.DeviceID || r.ConversationID != c.ConversationID || r.AgentVersionID != c.AgentVersionID {
		return p, ErrAccess
	}
	p.RunID = r.ID
	p.SessionID = r.SessionID
	p.ExpiresAt = *r.AccessExpiresAt
	return p, nil
}

// newRunAccess supplies only initial binding fields; conflict handling leaves an
// existing run's credential and deadline untouched.
func (s *Service) newRunAccess(ctx context.Context, tx *gorm.DB, device string, envelope protocol.ClientEvent) (*string, *int64, error) {
	if s.AccessSigner == nil || envelope.AccessToken == "" {
		return nil, nil, nil
	}
	c, _, _, err := s.verifyAccessSource(ctx, tx, envelope.AccessToken)
	if err != nil {
		return nil, nil, err
	}
	if c.DeviceID != device || c.MessageID != envelope.MessageID || c.AgentID != envelope.AgentID || c.ConversationID != envelope.ConversationID || c.AgentVersionID != envelope.AgentVersionID {
		return nil, nil, ErrAccess
	}
	now := s.Now().Unix()
	if now >= c.BindBefore || now >= c.AccessLimit {
		return nil, nil, nil
	}
	window := s.AccessTimeout
	if window <= 0 {
		window = 24 * time.Hour
	}
	expires := min(now+int64(window/time.Second), c.AccessLimit)
	hash := access.Hash(envelope.AccessToken)
	return &hash, &expires, nil
}

func (s *Service) AttachAccess(ctx context.Context, p *protocol.ConversationMessage) error {
	if s.AccessSigner == nil {
		return nil
	}
	t, err := s.accessTarget(ctx, s.DB, p.MessageID, p.AgentID)
	if err != nil {
		return err
	}
	token, err := s.AccessSigner.Sign(*t.AccessClaims)
	if err != nil {
		return err
	}
	p.Access = &protocol.Access{Token: token, BindBefore: t.AccessClaims.BindBefore}
	return nil
}

func (s *Service) RecoverAccess(ctx context.Context, device, runID string) (*protocol.Access, error) {
	var r database.Run
	if err := s.DB.WithContext(ctx).Where("id=? AND device_id=?", runID, device).Take(&r).Error; err != nil {
		return nil, ErrAccess
	}
	if r.AccessTokenHash == nil || r.AccessExpiresAt == nil || s.Now().Unix() >= *r.AccessExpiresAt || r.Status != "running" && r.Status != "waiting" {
		return nil, ErrAccess
	}
	t, err := s.accessTarget(ctx, s.DB, r.FirstMessageID, r.AgentID)
	if err != nil {
		return nil, err
	}
	token, err := s.AccessSigner.Sign(*t.AccessClaims)
	if err != nil {
		return nil, err
	}
	p, err := s.AccessForRequest(ctx, token)
	if err != nil || p.RunID != r.ID || p.Preparing {
		return nil, ErrAccess
	}
	return &protocol.Access{Token: token, ExpiresAt: *r.AccessExpiresAt}, nil
}

func (s *Service) AccessForEvent(ctx context.Context, device string, e protocol.ClientEvent) (string, *protocol.Access, error) {
	var event struct {
		Type  string `json:"type"`
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(e.Event, &event); err != nil {
		return "", nil, err
	}
	if event.Type != "accepted" {
		return "", nil, nil
	}
	a, err := s.RecoverAccess(ctx, device, event.RunID)
	if errors.Is(err, ErrAccess) || errors.Is(err, sql.ErrNoRows) {
		return event.RunID, nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("recover execution access: %w", err)
	}
	return event.RunID, a, nil
}
