package hub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/hub"
	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/services/callback"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/store"
	"github.com/memory-co/agentuse/pkg/sdk"
	"github.com/memory-co/agentuse/pkg/signature"
)

const (
	platformSecret = "platform-secret"
	adminKey       = "admin-key"
)

// env 是一套跑起来的平台 + hub。
type env struct {
	hub    *hub.Hub
	hubURL string
	sdk    *sdk.Client
	mu     sync.Mutex
	events []sdk.Event
	gotOne chan struct{}
}

func shopPolicy() models.Policy {
	return models.Policy{
		WakeRoles:     []models.Role{models.RoleMember},
		TrustedRoles:  []models.Role{models.RoleMember},
		Actions:       []models.Action{models.ActionReply, models.ActionStatus},
		WorkspaceMode: models.WorkspaceReadWrite,
	}
}

type envOpts struct {
	hub            hub.Options
	callbackSecret string
	policy         *models.Policy
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	e := &env{gotOne: make(chan struct{}, 16)}
	platform := httptest.NewServer(nil)
	t.Cleanup(platform.Close)

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	policy := shopPolicy()
	if o.policy != nil {
		policy = *o.policy
	}
	cfg := models.Platform{ID: "shop", Secret: platformSecret, CallbackSecret: o.callbackSecret,
		CallbackURL: platform.URL + "/agentuse/callback", Policy: policy}
	e.hub = hub.New([]models.Platform{cfg}, st, token.NewIssuer([]byte("hub-key")), callback.NewHTTPSender(), o.hub)
	srv := httptest.NewServer(hub.NewServer(e.hub, hub.ServerConfig{AdminKey: adminKey}).Handler())
	t.Cleanup(srv.Close)
	e.hubURL = srv.URL

	// 平台用 secret 签 notify 和管理请求,用 callback secret 验 hub 的回调(没拆就是同一把)。
	e.sdk = sdk.New(srv.URL, "shop", platformSecret)
	e.sdk.CallbackSecret = o.callbackSecret
	platform.Config.Handler = e.sdk.Handler(func(_ context.Context, ev sdk.Event) error {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
		e.gotOne <- struct{}{}
		return nil
	})
	return e
}

// agent 是一个接好了的 agent:平台建好、主人在自己电脑的 dir 上兑换了接入码。
type agent struct {
	sdk.Agent
	device *daemon.Client
}

// newAgent 替 owner 建一个 agent,但不接入。
func (e *env) newAgent(t *testing.T, owner, handle, wake string) sdk.Created {
	t.Helper()
	c, err := e.sdk.CreateAgent(context.Background(), sdk.NewAgent{Owner: owner, OwnerName: owner, Handle: handle, AgentUserID: "app_" + handle, Wake: wake})
	if err != nil {
		t.Fatalf("create agent %s: %v", handle, err)
	}
	return c
}

// connect 在一台(新)电脑上兑换接入码。known 是这台电脑已有的设备身份。
func (e *env) connect(t *testing.T, code string, known ...*daemon.Client) (hub.ConnectResponse, *daemon.Client) {
	t.Helper()
	return e.connectDir(t, code, "/home/u/repo", known...)
}

func (e *env) connectDir(t *testing.T, code, dir string, known ...*daemon.Client) (hub.ConnectResponse, *daemon.Client) {
	t.Helper()
	req := hub.ConnectRequest{Code: code, DeviceName: "laptop", OS: "linux/amd64", Dir: dir}
	for _, k := range known {
		req.Devices = append(req.Devices, hub.DeviceCred{ID: k.DeviceID, Key: k.Key})
	}
	res, err := daemon.Connect(context.Background(), e.hubURL, req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	key := res.DeviceKey
	for _, k := range known {
		if k.DeviceID == res.DeviceID {
			key = k.Key
		}
	}
	return res, daemon.NewClient(e.hubURL, res.DeviceID, key)
}

// connected 建一个 agent 并接到一台新电脑上。
func (e *env) connected(t *testing.T, owner, handle, wake string) agent {
	t.Helper()
	c := e.newAgent(t, owner, handle, wake)
	_, dev := e.connect(t, c.Connect.Code)
	return agent{Agent: c.Agent, device: dev}
}

func (e *env) notify(t *testing.T, id, author, role, text string) sdk.NotifyResult {
	t.Helper()
	return e.notifyThread(t, "issue:MUL-42", id, author, role, text)
}

func (e *env) notifyThread(t *testing.T, thread, id, author, role, text string) sdk.NotifyResult {
	t.Helper()
	res, err := e.sdk.Notify(context.Background(), sdk.Comment{ID: id, Thread: thread, Author: sdk.Author{ID: author, Name: author, Role: role}, Text: text})
	if err != nil {
		t.Fatalf("notify %s: %v", id, err)
	}
	return res
}

func (e *env) snapshot() []sdk.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdk.Event(nil), e.events...)
}

func agentPost(t *testing.T, base, tok, path string, v any) int {
	t.Helper()
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// 走一遍 MUL-42:alice 建 agent、接入自己的电脑 → 在工单里 @alice-claude → hub 排队 →
// alice 的电脑领取 → agent 用运行令牌回复并改状态 → 平台收到 hub 签名的回调。
func TestCommentToReply(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{})
	alice := e.connected(t, "alice", "alice-claude", "")

	if res := e.notify(t, "c0", "eve", "guest", "@alice-claude hi"); res.Woken || res.Reason != "not_owner" || len(res.Agents) != 1 {
		t.Fatalf("stranger notify: %+v", res)
	}
	res, err := e.sdk.Notify(ctx, sdk.Comment{
		ID: "c1", Thread: "issue:MUL-42", Author: sdk.Author{ID: "alice", Name: "alice", Role: "member"}, Text: "@alice-claude 退款金额算错了",
		Context: []sdk.Comment{{ID: "c0", Author: sdk.Author{ID: "eve", Role: "guest"}, Text: "@alice-claude hi"}},
	})
	if err != nil || !res.Woken || len(res.Agents) != 1 {
		t.Fatalf("notify: %+v %v", res, err)
	}
	woke := res.Agents[0]

	if _, err := daemon.NewClient(e.hubURL, alice.device.DeviceID, "wrong").Claim(ctx); err == nil {
		t.Fatal("claim with wrong key succeeded")
	}
	claimed, err := alice.device.Claim(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Run.ID != woke.RunID || claimed.Run.Attempts != 1 || claimed.Session.Branch != "agent/alice-claude/MUL-42" {
		t.Fatalf("claimed: %+v", claimed)
	}
	if claimed.Agent.Handle != "alice-claude" || claimed.Agent.Workdir != "/home/u/repo" || claimed.Agent.BaseRef != "main" {
		t.Fatalf("claimed agent: %+v", claimed.Agent)
	}
	if len(claimed.Run.Context) != 1 || claimed.Run.Context[0].Trusted {
		t.Fatalf("guest comment should be untrusted material: %+v", claimed.Run.Context)
	}
	if _, err := alice.device.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("second claim: %v", err)
	}

	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/reply", map[string]string{"text": "已修复", "thread": "issue:OTHER"}); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/status", map[string]string{"status": "in_review"}); code != 200 {
		t.Fatalf("status: %d", code)
	}
	if err := alice.device.Heartbeat(ctx, claimed.Run.ID, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := alice.device.Complete(ctx, claimed.Run.ID, hub.Completion{Attempt: 1, Succeeded: true, AgentSessionID: "thread_9c1"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := alice.device.Complete(ctx, claimed.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != daemon.ErrLost {
		t.Fatalf("double complete: %v", err)
	}
	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/reply", map[string]string{"text": "late"}); code != http.StatusConflict {
		t.Fatalf("reply after complete: %d", code)
	}

	got := e.snapshot()
	if len(got) != 2 || got[0].Type != "reply" || got[0].Thread != "issue:MUL-42" || got[0].InReplyTo != "c1" || got[1].Status != "in_review" {
		t.Fatalf("callbacks: %+v", got)
	}

	// 同一线程再 @ 同一个 agent 续接同一会话。
	res2 := e.notify(t, "c2", "alice", "member", "@alice-claude 再补个测试")
	if res2.Agents[0].SessionID != woke.SessionID {
		t.Fatalf("follow-up session: %+v", res2)
	}
	claimed2, err := alice.device.Claim(ctx)
	if err != nil || claimed2.Session.AgentSessionID != "thread_9c1" {
		t.Fatalf("resume: %+v %v", claimed2.Session, err)
	}
}

// 多用户:alice 的运行只派给 alice 的电脑;一条评论 @ 两个 agent 各排一条,互不影响。
func TestRunsGoOnlyToOwnersDevice(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{})
	alice := e.connected(t, "alice", "alice-claude", "members")
	bob := e.connected(t, "bob", "bob-claude", "")

	// carol 是成员:alice-claude 让成员叫醒,bob-claude 只认 bob。
	res := e.notify(t, "c1", "carol", "member", "@alice-claude @bob-claude @nobody 你们俩各看一下")
	if !res.Woken || len(res.Agents) != 2 {
		t.Fatalf("notify: %+v", res)
	}
	if a, b := res.Agents[0], res.Agents[1]; a.Handle != "alice-claude" || !a.Woken || b.Handle != "bob-claude" || b.Woken || b.Reason != "not_owner" {
		t.Fatalf("per-agent results: %+v", res.Agents)
	}
	if _, err := bob.device.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("bob's device got alice's run: %v", err)
	}
	if c, err := alice.device.Claim(ctx); err != nil || c.Agent.Handle != "alice-claude" {
		t.Fatalf("alice: %+v %v", c, err)
	}

	// 同一线程里两个 agent 是两段会话,可以同时跑。
	res = e.notify(t, "c2", "bob", "member", "@alice-claude @bob-claude 再看看")
	if !res.Agents[0].Woken || !res.Agents[1].Woken || res.Agents[0].SessionID == res.Agents[1].SessionID {
		t.Fatalf("two agents, two sessions: %+v", res.Agents)
	}
	if c, err := bob.device.Claim(ctx); err != nil || c.Session.Branch != "agent/bob-claude/MUL-42" {
		t.Fatalf("bob claims while alice-claude is busy in the same thread: %+v %v", c, err)
	}
	if _, err := alice.device.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("alice-claude's second run must wait for its first: %v", err)
	}
}

func TestWakeRules(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.connected(t, "alice", "alice-claude", "members")
	for _, tc := range []struct {
		author, role, text, want string
	}{
		{"alice", "guest", "@alice-claude hi", "woken"},                    // 主人总能叫醒
		{"bob", "member", "@alice-claude hi", "woken"},                     // members
		{"eve", "guest", "@alice-claude hi", "role_denied"},                // 角色不够
		{"bob", "member", "@bob 看一下", "no_mention"},                        // @ 的是普通用户
		{"app_alice-claude", "member", "@alice-claude 你好", "self_comment"}, // agent 自己
	} {
		res := e.notify(t, "c_"+tc.author+tc.role, tc.author, tc.role, tc.text)
		if res.Reason != tc.want || res.Woken != (tc.want == "woken") {
			t.Errorf("%s/%s %q: %+v, want %s", tc.author, tc.role, tc.text, res, tc.want)
		}
	}
}

// 同一线程同一 agent 连着 @ 两次:第一条没交回之前,第二条领不到。
func TestSameThreadRunsSerially(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{})
	alice := e.connected(t, "alice", "alice-claude", "")
	e.notify(t, "c1", "alice", "member", "@alice-claude 一")
	e.notify(t, "c2", "alice", "member", "@alice-claude 二")
	e.notifyThread(t, "issue:MUL-43", "c3", "alice", "member", "@alice-claude 另一个工单")

	first, err := alice.device.Claim(ctx)
	if err != nil || first.Run.Trigger.ID != "c1" {
		t.Fatalf("first: %+v %v", first.Run, err)
	}
	if other, err := alice.device.Claim(ctx); err != nil || other.Run.Trigger.ID != "c3" {
		t.Fatalf("another thread is not blocked: %+v %v", other.Run, err)
	}
	if _, err := alice.device.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("got a run from a busy session: %v", err)
	}
	if err := alice.device.Complete(ctx, first.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != nil {
		t.Fatal(err)
	}
	second, err := alice.device.Claim(ctx)
	if err != nil || second.Run.Trigger.ID != "c2" {
		t.Fatalf("second: %+v %v", second.Run, err)
	}
}

// 设备领了不心跳(崩了 / 断网):租约过期后运行回到队列重领,旧令牌和旧交回都作废。
func TestLeaseExpiryReclaims(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{hub: hub.Options{LeaseTTL: 50 * time.Millisecond, MaxAttempts: 2}})
	alice := e.connected(t, "alice", "alice-claude", "")
	e.notify(t, "c1", "alice", "member", "@alice-claude 修一下")

	stale, err := alice.device.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if reaped, err := e.hub.Reap(ctx); err != nil || len(reaped) != 1 || reaped[0].Status != models.RunQueued {
		t.Fatalf("reap: %+v %v", reaped, err)
	}
	fresh, err := alice.device.Claim(ctx)
	if err != nil || fresh.Run.Attempts != 2 {
		t.Fatalf("reclaim: %+v %v", fresh.Run, err)
	}
	if code := agentPost(t, e.hubURL, stale.Token, "/v1/run/reply", map[string]string{"text": "stale"}); code != http.StatusConflict {
		t.Fatalf("stale token: %d", code)
	}
	if err := alice.device.Heartbeat(ctx, stale.Run.ID, 1); err != daemon.ErrLost {
		t.Fatalf("stale heartbeat: %v", err)
	}
	if err := alice.device.Complete(ctx, stale.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != daemon.ErrLost {
		t.Fatalf("stale complete: %v", err)
	}
	if code := agentPost(t, e.hubURL, fresh.Token, "/v1/run/reply", map[string]string{"text": "fresh"}); code != 200 {
		t.Fatalf("fresh token: %d", code)
	}

	// 第二次也没心跳:到上限了,判失败。
	time.Sleep(80 * time.Millisecond)
	reaped, _ := e.hub.Reap(ctx)
	if len(reaped) != 1 || reaped[0].Status != models.RunFailed {
		t.Fatalf("max attempts: %+v", reaped)
	}
}

// 接入码:一次性;同一个主人在同一台电脑上接第二个 agent 复用设备;换电脑就是把 agent 搬过去。
func TestConnectCode(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{hub: hub.Options{PublicURL: "https://agentuse.shop.example.com"}})
	c := e.newAgent(t, "alice", "alice-claude", "")
	if c.Agent.Connected() || !strings.HasPrefix(c.Connect.Code, "ac_") ||
		c.Connect.Command != "agentuse connect https://agentuse.shop.example.com "+c.Connect.Code {
		t.Fatalf("created: %+v", c)
	}
	if _, err := daemon.Connect(ctx, e.hubURL, hub.ConnectRequest{Code: "ac_nope", Dir: "/x"}); err != daemon.ErrBadCode {
		t.Fatalf("wrong code: %v", err)
	}
	res, mbp := e.connect(t, c.Connect.Code)
	if res.Platform != "shop" || res.Agent.DeviceID != res.DeviceID || !strings.HasPrefix(res.DeviceKey, "dk_") {
		t.Fatalf("connect: %+v", res)
	}
	if _, err := daemon.Connect(ctx, e.hubURL, hub.ConnectRequest{Code: c.Connect.Code, Dir: "/x"}); err != daemon.ErrBadCode {
		t.Fatalf("code reused: %v", err)
	}

	// 设置页上:绑在 laptop 上;daemon 还没起,所以不在线。
	list, err := e.sdk.Agents(ctx, "alice")
	if err != nil || len(list) != 1 || list[0].Online || list[0].Device == nil || list[0].Device.Name != "laptop" {
		t.Fatalf("agents: %+v %v", list, err)
	}
	// 领取过一次(轮询模式)就算在线。
	mbp.Claim(ctx)
	if a, _ := e.sdk.Agent(ctx, c.Agent.ID); !a.Online || !a.Device.Online {
		t.Fatalf("online after a claim: %+v", a)
	}

	// 第二个 agent,同一台电脑:复用设备,不发新密钥。
	c2 := e.newAgent(t, "alice", "alice-review", "")
	res2, same := e.connect(t, c2.Connect.Code, mbp)
	if res2.DeviceID != mbp.DeviceID || res2.DeviceKey != "" {
		t.Fatalf("same machine should reuse device: %+v", res2)
	}
	// bob 在 alice 的电脑上接自己的 agent:不能复用 alice 的设备。
	cb := e.newAgent(t, "bob", "bob-claude", "")
	resB, _ := e.connect(t, cb.Connect.Code, mbp)
	if resB.DeviceID == mbp.DeviceID || resB.DeviceKey == "" {
		t.Fatalf("another owner must get another device: %+v", resB)
	}
	info, err := same.Info(ctx)
	if err != nil || len(info.Agents) != 2 {
		t.Fatalf("device info: %+v %v", info, err)
	}

	// 换电脑:新码兑换到新设备,运行跟着走。
	e.notify(t, "c1", "alice", "member", "@alice-claude 搬家前排的")
	code, err := e.sdk.ConnectCode(ctx, c.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, mini := e.connect(t, code.Code)
	if mini.DeviceID == mbp.DeviceID {
		t.Fatal("new machine got the old device")
	}
	if _, err := mbp.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("old machine still gets the agent's runs: %v", err)
	}
	if r, err := mini.Claim(ctx); err != nil || r.Run.Trigger.ID != "c1" {
		t.Fatalf("new machine: %+v %v", r, err)
	}

	// agentuse disconnect:只能解绑绑在自己身上的。
	if err := mbp.Disconnect(ctx, "alice-claude"); err == nil {
		t.Fatal("disconnected an agent that lives on another device")
	}
	if err := mbp.Disconnect(ctx, "alice-review"); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.sdk.Agent(ctx, c2.Agent.ID); a.Connected() {
		t.Fatalf("still connected: %+v", a)
	}
}

// 吊销设备:密钥立刻作废,agent 变成未接入。
func TestRevokeDevice(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{})
	alice := e.connected(t, "alice", "alice-claude", "")
	if err := e.sdk.RevokeDevice(ctx, "dv_nope"); !isCode(err, "not_found") {
		t.Fatalf("missing device: %v", err)
	}
	if err := e.sdk.RevokeDevice(ctx, alice.device.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.device.Claim(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("revoked device claimed: %v", err)
	}
	if a, _ := e.sdk.Agent(ctx, alice.ID); a.Connected() {
		t.Fatalf("agent still bound to a revoked device: %+v", a)
	}
}

// 删 agent:排着的运行判失败,以后 @ 它就是 @ 一个普通名字。
func TestDeleteAgent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{})
	alice := e.connected(t, "alice", "alice-claude", "")
	res := e.notify(t, "c1", "alice", "member", "@alice-claude 一")
	if err := e.sdk.DeleteAgent(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.hub.Run(ctx, res.Agents[0].RunID); r.Status != models.RunFailed {
		t.Fatalf("queued run of a deleted agent: %+v", r)
	}
	if res := e.notify(t, "c2", "alice", "member", "@alice-claude 二"); res.Woken || res.Reason != "no_mention" {
		t.Fatalf("mention after delete: %+v", res)
	}
	if _, err := e.sdk.Agent(ctx, alice.ID); !isCode(err, "not_found") {
		t.Fatalf("get deleted: %v", err)
	}
	// handle 空出来了,可以再建。
	e.newAgent(t, "alice", "alice-claude", "")
}

// 设备一直不在线:排队超过 queue_ttl 判失败,并给平台回调一条 error 进度。
func TestQueueTTL(t *testing.T) {
	ctx := context.Background()
	p := shopPolicy()
	p.QueueTTLSeconds = 1
	e := newEnv(t, envOpts{policy: &p})
	c := e.newAgent(t, "alice", "alice-claude", "") // 建了但没接入
	res := e.notify(t, "c1", "alice", "member", "@alice-claude 在吗")
	if !res.Woken || res.Agents[0].Online {
		t.Fatalf("notify an offline agent: %+v", res)
	}
	if got, _ := e.hub.Expire(ctx); len(got) != 0 {
		t.Fatalf("expired too early: %+v", got)
	}
	time.Sleep(1100 * time.Millisecond)
	got, err := e.hub.Expire(ctx)
	if err != nil || len(got) != 1 || got[0].Error != hub.ErrQueueTTL {
		t.Fatalf("expire: %+v %v", got, err)
	}
	ev := e.snapshot()
	if len(ev) != 1 || ev[0].Type != "activity" || ev[0].Activity.Type != "error" || ev[0].InReplyTo != "c1" ||
		!strings.Contains(ev[0].Activity.Body, "@alice-claude") {
		t.Fatalf("offline callback: %+v", ev)
	}
	// 这时候才接上:过期的不会再跑。
	_, dev := e.connect(t, c.Connect.Code)
	if _, err := dev.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("expired run was handed out: %v", err)
	}
}

// 平台管理接口:签名不对拒绝;agent 设置被平台策略截断;handle 冲突、不合法给出原因。
func TestManageAgents(t *testing.T) {
	ctx := context.Background()
	p := shopPolicy()
	p.Actions = []models.Action{models.ActionReply}
	e := newEnv(t, envOpts{policy: &p})

	bad := sdk.New(e.hubURL, "shop", "wrong")
	if _, err := bad.Agents(ctx, ""); !isCode(err, "bad_signature") {
		t.Fatalf("wrong secret: %v", err)
	}
	c, err := e.sdk.CreateAgent(ctx, sdk.NewAgent{Owner: "alice", Handle: "Alice-Claude", AgentUserID: "app_a", Actions: []string{"reply", "status"}})
	if err != nil || c.Agent.Handle != "alice-claude" || c.Agent.Wake != "owner" || len(c.Agent.Actions) != 1 {
		t.Fatalf("create (lowercased, defaults, capped): %+v %v", c.Agent, err)
	}
	if _, err := e.sdk.CreateAgent(ctx, sdk.NewAgent{Owner: "bob", Handle: "alice-claude", AgentUserID: "app_b"}); !isCode(err, "handle_taken") {
		t.Fatalf("duplicate handle: %v", err)
	}
	if _, err := e.sdk.CreateAgent(ctx, sdk.NewAgent{Owner: "bob", Handle: "-x", AgentUserID: "app_b"}); !isCode(err, "invalid") {
		t.Fatalf("bad handle: %v", err)
	}
	members := "members"
	a, err := e.sdk.UpdateAgent(ctx, c.Agent.ID, sdk.AgentPatch{Wake: &members})
	if err != nil || a.Wake != "members" {
		t.Fatalf("patch: %+v %v", a, err)
	}
	if list, _ := e.sdk.Agents(ctx, "bob"); len(list) != 0 {
		t.Fatalf("bob has no agents: %+v", list)
	}

	// 截获的一条签名请求(列表)不能换成 DELETE 重放:签名里有方法和路径。
	req, _ := http.NewRequest(http.MethodDelete, e.hubURL+"/v1/platforms/shop/agents/"+c.Agent.ID, nil)
	signature.SetHeaders(req.Header, platformSecret, signature.Request(http.MethodGet, "/v1/platforms/shop/agents", nil), time.Now())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed signature: %d", resp.StatusCode)
	}

	// 运行令牌的动作 = agent 设置 ∩ 平台策略。
	_, dev := e.connect(t, c.Connect.Code)
	e.notify(t, "c1", "bob", "member", "@alice-claude 改个状态")
	claimed, err := dev.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/status", map[string]string{"status": "closed"}); code != http.StatusForbidden {
		t.Fatalf("status beyond platform policy: %d", code)
	}
}

func isCode(err error, code string) bool {
	var e *sdk.Error
	return errors.As(err, &e) && e.Code == code
}

// 拆开的回调密钥:hub 用 callback_secret 签回调,平台用它验。
func TestCallbackSecret(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOpts{callbackSecret: "callback-only"})
	alice := e.connected(t, "alice", "alice-claude", "")
	e.notify(t, "c1", "alice", "member", "@alice-claude hi")
	c, err := alice.device.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code := agentPost(t, e.hubURL, c.Token, "/v1/run/reply", map[string]string{"text": "ok"}); code != 200 {
		t.Fatalf("reply with split secret: %d", code)
	}
}

func TestAdminAPI(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.connected(t, "alice", "alice-claude", "")
	res := e.notify(t, "c1", "alice", "member", "@alice-claude hi").Agents[0]

	get := func(path, key string) (int, map[string]json.RawMessage) {
		req, _ := http.NewRequest(http.MethodGet, e.hubURL+path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]json.RawMessage
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := get("/v1/admin/runs", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong admin key: %d", code)
	}
	code, out := get("/v1/admin/runs?status=queued&agent="+res.AgentID, adminKey)
	if code != 200 || !strings.Contains(string(out["runs"]), res.RunID) {
		t.Fatalf("runs: %d %s", code, out["runs"])
	}
	code, out = get("/v1/admin/sessions?platform=shop", adminKey)
	if code != 200 || !strings.Contains(string(out["sessions"]), res.SessionID) {
		t.Fatalf("sessions: %d %s", code, out["sessions"])
	}
	if code, out = get("/v1/admin/runs/"+res.RunID, adminKey); code != 200 || !strings.Contains(string(out["id"]), res.RunID) {
		t.Fatalf("run: %d %v", code, out)
	}
	if code, _ := get("/v1/admin/runs/r_nope", adminKey); code != http.StatusNotFound {
		t.Fatalf("missing run: %d", code)
	}
}

func TestBranchName(t *testing.T) {
	for in, want := range map[string]string{
		"issue:MUL-42":     "agent/alice-claude/MUL-42",
		"blog:/posts/tmux": "agent/alice-claude/posts-tmux",
		"topic:8812":       "agent/alice-claude/8812",
	} {
		if got := hub.BranchName("alice-claude", in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
