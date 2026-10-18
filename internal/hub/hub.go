// Package hub 是 hub 实例的业务:唤醒判断、会话查找、排队、签发令牌、转发写回。
// HTTP 接口在 server.go。
package hub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/services/callback"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/services/wake"
	"github.com/memory-co/agentuse/internal/store"
)

var (
	ErrUnknownPlatform = errors.New("hub: unknown platform")
	ErrForbidden       = errors.New("hub: action not allowed for this run")
	ErrRunState        = errors.New("hub: run is not in a state that allows this")
)

// Hub 把平台、会话表、队列和 daemon 接起来。
type Hub struct {
	platforms map[string]models.Platform
	store     store.Store
	tokens    *token.Issuer
	sender    callback.Sender
	now       func() time.Time
}

func New(platforms []models.Platform, st store.Store, tokens *token.Issuer, sender callback.Sender) *Hub {
	m := make(map[string]models.Platform, len(platforms))
	for _, p := range platforms {
		m[p.ID] = p
	}
	return &Hub{platforms: m, store: st, tokens: tokens, sender: sender, now: func() time.Time { return time.Now().UTC() }}
}

// Platform 找一个已配置的平台。
func (h *Hub) Platform(id string) (models.Platform, error) {
	p, ok := h.platforms[id]
	if !ok {
		return p, ErrUnknownPlatform
	}
	return p, nil
}

// Notification 是平台 notify 的正文:触发评论 + 可选的线程上下文。
type Notification struct {
	models.Comment
	Context []models.Comment `json:"context,omitempty"`
}

// NotifyResult 告诉平台这条评论有没有唤醒 agent。
type NotifyResult struct {
	Woken     bool        `json:"woken"`
	Reason    wake.Reason `json:"reason"`
	SessionID string      `json:"session_id,omitempty"`
	RunID     string      `json:"run_id,omitempty"`
}

// Notify 处理一条评论:判唤醒,找或建会话,排一次运行。
func (h *Hub) Notify(ctx context.Context, platformID string, n Notification) (NotifyResult, error) {
	p, err := h.Platform(platformID)
	if err != nil {
		return NotifyResult{}, err
	}
	ok, reason := wake.Decide(p.Policy, n.Comment)
	if !ok {
		return NotifyResult{Woken: false, Reason: reason}, nil
	}

	now := h.now()
	sess, err := h.store.SessionByThread(ctx, p.ID, n.Thread)
	if errors.Is(err, store.ErrNotFound) {
		sess = models.Session{
			ID:        models.NewID("s_"),
			Platform:  p.ID,
			Thread:    n.Thread,
			Branch:    BranchName(p.Policy.AgentName, n.Thread),
			CreatedAt: now,
		}
	} else if err != nil {
		return NotifyResult{}, err
	}
	sess.UpdatedAt = now
	if err := h.store.PutSession(ctx, sess); err != nil {
		return NotifyResult{}, err
	}

	run := models.Run{
		ID:            models.NewID("r_"),
		SessionID:     sess.ID,
		Platform:      p.ID,
		Thread:        n.Thread,
		Trigger:       n.Comment,
		Context:       materials(p.Policy, n.Comment, n.Context),
		Actions:       p.Policy.Actions,
		WorkspaceMode: p.Policy.WorkspaceMode,
		Status:        models.RunQueued,
		CreatedAt:     now,
	}
	if err := h.store.PutRun(ctx, run); err != nil {
		return NotifyResult{}, err
	}
	return NotifyResult{Woken: true, Reason: reason, SessionID: sess.ID, RunID: run.ID}, nil
}

// materials 把线程里的其它评论按策略分成可信 / 不可信材料;叫醒者自己的评论算可信。
func materials(p models.Policy, trigger models.Comment, ctx []models.Comment) []models.Material {
	out := make([]models.Material, 0, len(ctx))
	for _, c := range ctx {
		if c.ID == trigger.ID {
			continue
		}
		trusted := c.Author.ID == trigger.Author.ID || models.Has(p.TrustedRoles, c.Author.Role)
		out = append(out, models.Material{Comment: c, Trusted: trusted})
	}
	return out
}

// Claimed 是 daemon 领到的一次运行:运行本身、它的会话、以及只给这次运行的令牌。
type Claimed struct {
	Run     models.Run     `json:"run"`
	Session models.Session `json:"session"`
	Token   string         `json:"token"`
}

// Claim 让 daemon 领取下一条排队的运行;队列空时返回 store.ErrNotFound。
func (h *Hub) Claim(ctx context.Context, daemonID string) (Claimed, error) {
	run, err := h.store.ClaimRun(ctx, daemonID)
	if err != nil {
		return Claimed{}, err
	}
	sess, err := h.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return Claimed{}, err
	}
	tok, err := h.tokens.Issue(run, token.MaxTTL)
	if err != nil {
		return Claimed{}, err
	}
	return Claimed{Run: run, Session: sess, Token: tok}, nil
}

// Completion 是 daemon 交回的运行结果。
type Completion struct {
	DaemonID       string `json:"-"`
	Succeeded      bool   `json:"succeeded"`
	Error          string `json:"error,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"` // 回填到会话,下次续接
}

// Complete 结束一次运行。
func (h *Hub) Complete(ctx context.Context, runID string, c Completion) (models.Run, error) {
	run, err := h.store.GetRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if run.Status != models.RunClaimed || run.DaemonID != c.DaemonID {
		return run, ErrRunState
	}
	now := h.now()
	run.FinishedAt, run.Error = &now, c.Error
	run.Status = models.RunFailed
	if c.Succeeded {
		run.Status = models.RunSucceeded
	}
	if err := h.store.PutRun(ctx, run); err != nil {
		return run, err
	}
	if c.AgentSessionID != "" {
		sess, err := h.store.GetSession(ctx, run.SessionID)
		if err == nil {
			sess.AgentSessionID, sess.UpdatedAt = c.AgentSessionID, now
			err = h.store.PutSession(ctx, sess)
		}
		if err != nil {
			return run, err
		}
	}
	return run, nil
}

// Write 是 agent 拿运行令牌发来的一次写回:reply / status / activity。
func (h *Hub) Write(ctx context.Context, claims token.Claims, ev models.Event) error {
	switch ev.Type {
	case models.EventReply:
		if !claims.Allows(models.ActionReply) {
			return ErrForbidden
		}
	case models.EventStatus:
		if !claims.Allows(models.ActionStatus) {
			return ErrForbidden
		}
	case models.EventActivity:
		// 进度不改平台对象,运行令牌有效即可。
	default:
		return fmt.Errorf("hub: unknown event type %q", ev.Type)
	}
	run, err := h.store.GetRun(ctx, claims.RunID)
	if err != nil {
		return err
	}
	if run.Status != models.RunClaimed {
		return ErrRunState
	}
	p, err := h.Platform(run.Platform)
	if err != nil {
		return err
	}
	// 范围以令牌和运行为准,不信请求体里自带的。
	ev.Platform, ev.Thread, ev.RunID, ev.InReplyTo, ev.CreatedAt = run.Platform, run.Thread, run.ID, run.Trigger.ID, h.now()
	return h.sender.Send(ctx, p, ev)
}

// Verify 校验运行令牌。
func (h *Hub) Verify(tok string) (token.Claims, error) { return h.tokens.Verify(tok) }

// BranchName 是一个线程在用户仓库里的分支名:agent/<agent>/<线程键去掉类型前缀后的安全形式>。
func BranchName(agent, thread string) string {
	if agent == "" {
		agent = "agent"
	}
	key := thread
	for i := 0; i < len(thread); i++ {
		if thread[i] == ':' {
			key = thread[i+1:]
			break
		}
	}
	b := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b = append(b, c)
		default:
			if len(b) > 0 && b[len(b)-1] != '-' {
				b = append(b, '-')
			}
		}
	}
	return "agent/" + agent + "/" + string(trimDash(b))
}

func trimDash(b []byte) []byte {
	for len(b) > 0 && b[0] == '-' {
		b = b[1:]
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	return b
}
