// Package hub 是 hub 实例的业务:唤醒判断、会话查找、排队、签发令牌、转发写回。
// HTTP 接口在 server.go。
package hub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
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

// Options 是运行生命周期和接入的参数。
type Options struct {
	LeaseTTL    time.Duration // 一次领取 / 心跳续多久,默认 2 分钟;daemon 每 15 秒心跳一次
	MaxAttempts int           // 租约过期后最多重新排队几次(含第一次),默认 3
	PublicURL   string        // 用户电脑访问 hub 的地址,写进接入命令
}

func (o Options) withDefaults() Options {
	if o.LeaseTTL <= 0 {
		o.LeaseTTL = 2 * time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	return o
}

// OnlineWindow 是轮询模式下设备最近一次请求之后多久内还算在线。
const OnlineWindow = 45 * time.Second

// Hub 把平台、用户、agent、设备、会话表、队列接起来。
type Hub struct {
	platforms map[string]models.Platform
	store     store.Store
	tokens    *token.Issuer
	sender    callback.Sender
	opts      Options
	now       func() time.Time

	mu      sync.Mutex
	changed chan struct{}        // 队列可能有新的可领运行时关闭并换一个新的
	conns   map[string]int       // 设备 id → 当前推送连接数
	seen    map[string]time.Time // 设备 id → 最近一次请求
}

func New(platforms []models.Platform, st store.Store, tokens *token.Issuer, sender callback.Sender, opts Options) *Hub {
	m := make(map[string]models.Platform, len(platforms))
	for _, p := range platforms {
		m[p.ID] = p
	}
	return &Hub{
		platforms: m, store: st, tokens: tokens, sender: sender, opts: opts.withDefaults(),
		now:     func() time.Time { return time.Now().UTC() },
		changed: make(chan struct{}),
		conns:   map[string]int{},
		seen:    map[string]time.Time{},
	}
}

// Changed 交回一个通道:队列里可能出现新的可领运行时(新入队、会话空出来、回收、agent 换了设备)它会被关闭。
// 推送连接用它代替轮询。
func (h *Hub) Changed() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

func (h *Hub) signal() {
	h.mu.Lock()
	defer h.mu.Unlock()
	close(h.changed)
	h.changed = make(chan struct{})
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

// NotifyResult 告诉平台这条评论叫醒了哪些 agent。
type NotifyResult struct {
	Woken  bool          `json:"woken"`  // 至少叫醒了一个
	Reason wake.Reason   `json:"reason"` // 叫醒了就是 woken;否则是整条评论没叫醒的原因
	Agents []AgentResult `json:"agents"` // 评论里 @ 到的、这个平台上存在的 agent,逐个交代
}

// AgentResult 是一条评论对一个被 @ 的 agent 的结果。
type AgentResult struct {
	Handle    string      `json:"handle"`
	AgentID   string      `json:"agent_id"`
	Woken     bool        `json:"woken"`
	Reason    wake.Reason `json:"reason"`
	Online    bool        `json:"online"` // 绑定的设备现在在不在线;不在线时运行排着等它
	SessionID string      `json:"session_id,omitempty"`
	RunID     string      `json:"run_id,omitempty"`
}

// Notify 处理一条评论:找出 @ 到的 agent,逐个判唤醒,各自找或建会话、排一次运行。
func (h *Hub) Notify(ctx context.Context, platformID string, n Notification) (NotifyResult, error) {
	res := NotifyResult{Reason: wake.NoMention, Agents: []AgentResult{}}
	p, err := h.Platform(platformID)
	if err != nil {
		return res, err
	}
	if n.Author.ID != "" {
		// 任何 agent 发的评论都不唤醒 agent:防自触发,也防两个 agent 互相 @ 成环。
		self, err := h.store.ListAgents(ctx, store.AgentFilter{Platform: p.ID, AgentUserID: n.Author.ID})
		if err != nil {
			return res, err
		}
		if len(self) > 0 {
			res.Reason = wake.SelfComment
			return res, nil
		}
		h.touchUser(ctx, p.ID, n.Author)
	}

	for _, handle := range wake.Handles(n.Text) {
		a, err := h.store.AgentByHandle(ctx, p.ID, handle)
		if errors.Is(err, store.ErrNotFound) {
			continue // @ 的是平台里的普通用户
		}
		if err != nil {
			return res, err
		}
		r := AgentResult{Handle: a.Handle, AgentID: a.ID, Online: h.Online(a.DeviceID)}
		var ok bool
		ok, r.Reason = wake.Decide(p.Policy, a, n.Comment)
		if ok {
			if r.SessionID, r.RunID, err = h.enqueue(ctx, p, a, n); err != nil {
				return res, err
			}
			r.Woken = true
		}
		res.Agents = append(res.Agents, r)
	}

	for _, r := range res.Agents {
		if r.Woken {
			res.Woken, res.Reason = true, wake.Woken
			break
		}
	}
	if !res.Woken && len(res.Agents) > 0 {
		res.Reason = res.Agents[0].Reason
	}
	if res.Woken {
		h.signal()
	}
	return res, nil
}

// enqueue 给 agent a 在评论所在线程找或建会话,排一条运行。
func (h *Hub) enqueue(ctx context.Context, p models.Platform, a models.Agent, n Notification) (string, string, error) {
	now := h.now()
	sess, err := h.store.SessionByThread(ctx, p.ID, n.Thread, a.ID)
	if errors.Is(err, store.ErrNotFound) {
		sess = models.Session{
			ID:        models.NewID("s_"),
			Platform:  p.ID,
			Thread:    n.Thread,
			AgentID:   a.ID,
			Branch:    BranchName(a.Handle, n.Thread),
			CreatedAt: now,
		}
	} else if err != nil {
		return "", "", err
	}
	sess.UpdatedAt = now
	if err := h.store.PutSession(ctx, sess); err != nil {
		return "", "", err
	}

	actions, mode := a.Effective(p.Policy)
	run := models.Run{
		ID:            models.NewID("r_"),
		SessionID:     sess.ID,
		Platform:      p.ID,
		AgentID:       a.ID,
		Thread:        n.Thread,
		Trigger:       n.Comment,
		Context:       materials(p.Policy, a, n.Comment, n.Context),
		Actions:       actions,
		WorkspaceMode: mode,
		Status:        models.RunQueued,
		CreatedAt:     now,
	}
	if err := h.store.CreateRun(ctx, run); err != nil {
		return "", "", err
	}
	return sess.ID, run.ID, nil
}

// touchUser 记下(或更新显示名)评论作者。失败不影响唤醒。
func (h *Hub) touchUser(ctx context.Context, platform string, au models.Author) {
	now := h.now()
	if u, err := h.store.GetUser(ctx, platform, au.ID); err == nil && u.Name == au.Name {
		return
	}
	if err := h.store.PutUser(ctx, models.User{Platform: platform, ID: au.ID, Name: au.Name, CreatedAt: now, UpdatedAt: now}); err != nil {
		log.Printf("put user %s/%s: %v", platform, au.ID, err)
	}
}

// materials 把线程里的其它评论按策略分成可信 / 不可信材料;叫醒者自己和 agent 主人的评论算可信。
func materials(p models.Policy, a models.Agent, trigger models.Comment, ctx []models.Comment) []models.Material {
	out := make([]models.Material, 0, len(ctx))
	for _, c := range ctx {
		if c.ID == trigger.ID {
			continue
		}
		trusted := c.Author.ID == trigger.Author.ID || c.Author.ID == a.Owner || models.Has(p.TrustedRoles, c.Author.Role)
		out = append(out, models.Material{Comment: c, Trusted: trusted})
	}
	return out
}

// Claimed 是设备领到的一次运行:运行本身、它的会话和 agent(在哪个目录、用什么驱动),
// 以及只给这次运行的令牌。
type Claimed struct {
	Run     models.Run     `json:"run"`
	Session models.Session `json:"session"`
	Agent   models.Agent   `json:"agent"`
	Token   string         `json:"token"`
}

// Claim 替设备领取下一条排给它的运行;没有返回 store.ErrNotFound。
func (h *Hub) Claim(ctx context.Context, deviceID string) (Claimed, error) {
	now := h.now()
	run, err := h.store.ClaimRun(ctx, store.Claim{DeviceID: deviceID, Now: now, LeaseUntil: now.Add(h.opts.LeaseTTL)})
	if err != nil {
		return Claimed{}, err
	}
	sess, err := h.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return Claimed{}, err
	}
	a, err := h.store.GetAgent(ctx, run.AgentID)
	if err != nil {
		return Claimed{}, err
	}
	tok, err := h.tokens.Issue(run, token.MaxTTL)
	if err != nil {
		return Claimed{}, err
	}
	return Claimed{Run: run, Session: sess, Agent: a, Token: tok}, nil
}

// Heartbeat 续租:设备还在跑这次运行。
func (h *Hub) Heartbeat(ctx context.Context, runID, deviceID string, attempt int) (models.Run, error) {
	run, err := h.store.ExtendLease(ctx, runID, deviceID, attempt, h.now().Add(h.opts.LeaseTTL))
	return run, stateErr(err)
}

// Completion 是 daemon 交回的运行结果。
type Completion struct {
	DeviceID       string `json:"-"`
	Attempt        int    `json:"attempt"` // 领取时 run.attempts 的值
	Succeeded      bool   `json:"succeeded"`
	Error          string `json:"error,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"` // 回填到会话,下次续接
}

// Complete 结束一次运行。
func (h *Hub) Complete(ctx context.Context, runID string, c Completion) (models.Run, error) {
	status := models.RunFailed
	if c.Succeeded {
		status = models.RunSucceeded
	}
	now := h.now()
	run, err := h.store.FinishRun(ctx, runID, c.DeviceID, c.Attempt, status, c.Error, now)
	if err != nil {
		return run, stateErr(err)
	}
	h.signal() // 会话空出来了,同线程排着的下一条可以领了
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

// Reap 回收租约过期的运行,交回被回收的那些。
func (h *Hub) Reap(ctx context.Context) ([]models.Run, error) {
	runs, err := h.store.ReapExpired(ctx, h.now(), h.opts.MaxAttempts)
	if len(runs) > 0 {
		h.signal()
	}
	return runs, err
}

// ErrQueueTTL 是排队超时判失败的运行的 error。
const ErrQueueTTL = "queue_ttl: device offline"

// Expire 把每个平台里排队超过 queue_ttl 的运行判失败,并给平台回调一条 error 进度,交回这些运行。
func (h *Hub) Expire(ctx context.Context) ([]models.Run, error) {
	now := h.now()
	var out []models.Run
	for _, p := range h.platforms {
		runs, err := h.store.FailRuns(ctx, store.FailFilter{
			Platform: p.ID, CreatedBefore: now.Add(-p.Policy.QueueTTL()), Statuses: []models.RunStatus{models.RunQueued},
		}, ErrQueueTTL, now)
		if err != nil {
			return out, err
		}
		for _, r := range runs {
			h.notifyFailure(ctx, p, r, "的电脑一直不在线,这条没有处理。等它上线后再 @ 一次。")
		}
		out = append(out, runs...)
	}
	return out, nil
}

// notifyFailure 告诉平台一条运行没跑成(回调 type=error 的进度)。回调失败只记日志。
func (h *Hub) notifyFailure(ctx context.Context, p models.Platform, r models.Run, why string) {
	if h.sender == nil {
		return
	}
	name, handle := r.AgentID, ""
	if a, err := h.store.GetAgent(ctx, r.AgentID); err == nil {
		name, handle = "@"+a.Handle, a.Handle
	}
	ev := models.Event{
		Type: models.EventActivity, Platform: r.Platform, Thread: r.Thread, RunID: r.ID, InReplyTo: r.Trigger.ID,
		AgentID: r.AgentID, Agent: handle,
		Activity:  &models.Activity{Type: models.ActivityError, Body: name + why},
		CreatedAt: h.now(),
	}
	if err := h.sender.Send(ctx, p, ev); err != nil {
		log.Printf("callback %s (run %s failed): %v", p.ID, r.ID, err)
	}
}

// RunReaper 每隔 every 回收过期租约、清理排队超时的运行,直到 ctx 结束。
func (h *Hub) RunReaper(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runs, err := h.Reap(ctx)
			if err != nil {
				log.Printf("reap: %v", err)
			}
			for _, r := range runs {
				log.Printf("reap %s: lease expired after attempt %d → %s", r.ID, r.Attempts, r.Status)
			}
			expired, err := h.Expire(ctx)
			if err != nil {
				log.Printf("expire: %v", err)
			}
			for _, r := range expired {
				log.Printf("expire %s: queued since %s, agent %s offline", r.ID, r.CreatedAt.Format(time.RFC3339), r.AgentID)
			}
		}
	}
}

func stateErr(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return ErrRunState
	}
	return err
}

// Sessions / Runs / Run 是给实例主人的只读查询。
func (h *Hub) Sessions(ctx context.Context, platform string, limit int) ([]models.Session, error) {
	return h.store.ListSessions(ctx, platform, limit)
}

func (h *Hub) Runs(ctx context.Context, f store.RunFilter) ([]models.Run, error) {
	return h.store.ListRuns(ctx, f)
}

func (h *Hub) Run(ctx context.Context, id string) (models.Run, error) {
	return h.store.GetRun(ctx, id)
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
	if run.Status != models.RunClaimed || run.Attempts != claims.Attempt {
		return ErrRunState
	}
	p, err := h.Platform(run.Platform)
	if err != nil {
		return err
	}
	// 范围以令牌和运行为准,不信请求体里自带的。
	ev.Platform, ev.Thread, ev.RunID, ev.InReplyTo, ev.CreatedAt = run.Platform, run.Thread, run.ID, run.Trigger.ID, h.now()
	ev.AgentID, ev.Agent = run.AgentID, ""
	if a, err := h.store.GetAgent(ctx, run.AgentID); err == nil {
		ev.Agent = a.Handle
	}
	return h.sender.Send(ctx, p, ev)
}

// Verify 校验运行令牌。
func (h *Hub) Verify(tok string) (token.Claims, error) { return h.tokens.Verify(tok) }

// BranchName 是一个线程在用户仓库里的分支名:agent/<handle>/<线程键去掉类型前缀后的安全形式>。
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
