package hub_test

import (
	"bytes"
	"context"
	"encoding/json"
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
)

const (
	platformSecret = "platform-secret"
	daemonKey      = "daemon-key"
	adminKey       = "admin-key"
)

// env 是一套跑起来的平台 + hub。
type env struct {
	hub     *hub.Hub
	hubURL  string
	sdk     *sdk.Client
	mu      sync.Mutex
	events  []sdk.Event
	gotOne  chan struct{}
	cleanup []func()
}

func shopPolicy() models.Policy {
	return models.Policy{
		AgentName:     "claude",
		AgentUserID:   "u_claude",
		WakeRoles:     []models.Role{models.RoleMember},
		TrustedRoles:  []models.Role{models.RoleMember},
		Actions:       []models.Action{models.ActionReply, models.ActionStatus},
		WorkspaceMode: models.WorkspaceReadWrite,
	}
}

func newEnv(t *testing.T, opts hub.Options, callbackSecret string) *env {
	t.Helper()
	e := &env{gotOne: make(chan struct{}, 16)}
	platform := httptest.NewServer(nil)
	t.Cleanup(platform.Close)

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := models.Platform{ID: "shop", Secret: platformSecret, CallbackSecret: callbackSecret,
		CallbackURL: platform.URL + "/agentuse/callback", Policy: shopPolicy()}
	e.hub = hub.New([]models.Platform{cfg}, st, token.NewIssuer([]byte("hub-key")), callback.NewHTTPSender(), opts)
	srv := httptest.NewServer(hub.NewServer(e.hub, hub.ServerConfig{
		DaemonKeys: map[string]string{"alice-mbp": daemonKey, "bob-mbp": daemonKey},
		AdminKey:   adminKey,
	}).Handler())
	t.Cleanup(srv.Close)
	e.hubURL = srv.URL

	// 平台用 secret 签 notify,用 callback secret 验 hub 的回调(没拆就是同一把)。
	e.sdk = sdk.New(srv.URL, "shop", platformSecret)
	e.sdk.CallbackSecret = callbackSecret
	platform.Config.Handler = e.sdk.Handler(func(_ context.Context, ev sdk.Event) error {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
		e.gotOne <- struct{}{}
		return nil
	})
	return e
}

func (e *env) notify(t *testing.T, id, author, role, text string) sdk.NotifyResult {
	t.Helper()
	res, err := e.sdk.Notify(context.Background(), sdk.Comment{ID: id, Thread: "issue:MUL-42", Author: sdk.Author{ID: author, Name: author, Role: role}, Text: text})
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

// 走一遍博客里 MUL-42 的流程:bob 在工单里 @claude → hub 排队 → daemon 领取 →
// agent 用运行令牌回复并改状态 → 平台收到 hub 签名的回调。
func TestCommentToReply(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, hub.Options{}, "")

	if res := e.notify(t, "c0", "eve", "guest", "@claude hi"); res.Woken || res.Reason != "role_denied" {
		t.Fatalf("guest notify: %+v", res)
	}
	res, err := e.sdk.Notify(ctx, sdk.Comment{
		ID: "c1", Thread: "issue:MUL-42", Author: sdk.Author{ID: "bob", Name: "bob", Role: "member"}, Text: "@claude 退款金额算错了",
		Context: []sdk.Comment{{ID: "c0", Author: sdk.Author{ID: "eve", Role: "guest"}, Text: "@claude hi"}},
	})
	if err != nil || !res.Woken {
		t.Fatalf("notify: %+v %v", res, err)
	}

	d := daemon.NewClient(e.hubURL, "alice-mbp", daemonKey)
	if _, err := daemon.NewClient(e.hubURL, "alice-mbp", "wrong").Claim(ctx); err == nil {
		t.Fatal("claim with wrong key succeeded")
	}
	claimed, err := d.Claim(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Run.ID != res.RunID || claimed.Run.Attempts != 1 || claimed.Session.Branch != "agent/claude/MUL-42" {
		t.Fatalf("claimed: %+v", claimed)
	}
	if len(claimed.Run.Context) != 1 || claimed.Run.Context[0].Trusted {
		t.Fatalf("guest comment should be untrusted material: %+v", claimed.Run.Context)
	}
	if _, err := d.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("second claim: %v", err)
	}

	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/reply", map[string]string{"text": "已修复,见分支 agent/claude/MUL-42", "thread": "issue:OTHER"}); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/status", map[string]string{"status": "in_review"}); code != 200 {
		t.Fatalf("status: %d", code)
	}
	if err := d.Heartbeat(ctx, claimed.Run.ID, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := d.Complete(ctx, claimed.Run.ID, hub.Completion{Attempt: 1, Succeeded: true, AgentSessionID: "thread_9c1"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := d.Complete(ctx, claimed.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != daemon.ErrLost {
		t.Fatalf("double complete: %v", err)
	}
	if code := agentPost(t, e.hubURL, claimed.Token, "/v1/run/reply", map[string]string{"text": "late"}); code != http.StatusConflict {
		t.Fatalf("reply after complete: %d", code)
	}

	got := e.snapshot()
	if len(got) != 2 || got[0].Type != "reply" || got[0].Thread != "issue:MUL-42" || got[0].InReplyTo != "c1" || got[1].Status != "in_review" {
		t.Fatalf("callbacks: %+v", got)
	}

	// 同一线程的下一条评论续接同一会话。
	res2 := e.notify(t, "c2", "bob", "member", "@claude 再补个测试")
	if res2.SessionID != res.SessionID {
		t.Fatalf("follow-up session: %+v", res2)
	}
	claimed2, err := d.Claim(ctx)
	if err != nil || claimed2.Session.AgentSessionID != "thread_9c1" {
		t.Fatalf("resume: %+v %v", claimed2.Session, err)
	}
}

// 同一线程连着 @ 两次:第一条没交回之前,第二条谁也领不到。
func TestSameThreadRunsSerially(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, hub.Options{}, "")
	e.notify(t, "c1", "bob", "member", "@claude 一")
	e.notify(t, "c2", "bob", "member", "@claude 二")

	alice := daemon.NewClient(e.hubURL, "alice-mbp", daemonKey)
	bob := daemon.NewClient(e.hubURL, "bob-mbp", daemonKey)
	first, err := alice.Claim(ctx)
	if err != nil || first.Run.Trigger.ID != "c1" {
		t.Fatalf("first: %+v %v", first.Run, err)
	}
	if _, err := bob.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("second daemon got a run from a busy session: %v", err)
	}
	if err := alice.Complete(ctx, first.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != nil {
		t.Fatal(err)
	}
	second, err := bob.Claim(ctx)
	if err != nil || second.Run.Trigger.ID != "c2" {
		t.Fatalf("second: %+v %v", second.Run, err)
	}
}

// daemon 领了不心跳(崩了 / 断网):租约过期后运行回到队列,别的 daemon 重领,旧令牌和旧交回都作废。
func TestLeaseExpiryReclaims(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, hub.Options{LeaseTTL: 50 * time.Millisecond, MaxAttempts: 2}, "")
	e.notify(t, "c1", "bob", "member", "@claude 修一下")

	alice := daemon.NewClient(e.hubURL, "alice-mbp", daemonKey)
	bob := daemon.NewClient(e.hubURL, "bob-mbp", daemonKey)
	stale, err := alice.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if reaped, err := e.hub.Reap(ctx); err != nil || len(reaped) != 1 || reaped[0].Status != models.RunQueued {
		t.Fatalf("reap: %+v %v", reaped, err)
	}
	fresh, err := bob.Claim(ctx)
	if err != nil || fresh.Run.Attempts != 2 {
		t.Fatalf("reclaim: %+v %v", fresh.Run, err)
	}
	if code := agentPost(t, e.hubURL, stale.Token, "/v1/run/reply", map[string]string{"text": "stale"}); code != http.StatusConflict {
		t.Fatalf("stale token: %d", code)
	}
	if err := alice.Heartbeat(ctx, stale.Run.ID, 1); err != daemon.ErrLost {
		t.Fatalf("stale heartbeat: %v", err)
	}
	if err := alice.Complete(ctx, stale.Run.ID, hub.Completion{Attempt: 1, Succeeded: true}); err != daemon.ErrLost {
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

// 拆开的回调密钥:hub 用 callback_secret 签回调,平台用它验。
func TestCallbackSecret(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, hub.Options{}, "callback-only")
	e.notify(t, "c1", "bob", "member", "@claude hi")
	c, err := daemon.NewClient(e.hubURL, "alice-mbp", daemonKey).Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code := agentPost(t, e.hubURL, c.Token, "/v1/run/reply", map[string]string{"text": "ok"}); code != 200 {
		t.Fatalf("reply with split secret: %d", code)
	}
}

func TestStatusForbiddenByPolicy(t *testing.T) {
	ctx := context.Background()
	cfg := models.Platform{ID: "blog", Secret: "s", Policy: models.Policy{
		AgentName: "claude", WakeRoles: []models.Role{models.RoleOwner}, Actions: []models.Action{models.ActionReply},
	}}
	iss := token.NewIssuer([]byte("k"))
	h := hub.New([]models.Platform{cfg}, store.NewMemory(), iss, nil, hub.Options{})
	res, err := h.Notify(ctx, "blog", hub.Notification{Comment: models.Comment{ID: "c1", Thread: "blog:/posts/tmux", Author: models.Author{ID: "alice", Role: models.RoleOwner}, Text: "@claude ?"}})
	if err != nil || !res.Woken {
		t.Fatalf("notify: %+v %v", res, err)
	}
	c, err := h.Claim(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := iss.Verify(c.Token)
	if err := h.Write(ctx, claims, models.Event{Type: models.EventStatus, Status: "closed"}); err != hub.ErrForbidden {
		t.Fatalf("status on blog: %v", err)
	}
}

func TestAdminAPI(t *testing.T) {
	e := newEnv(t, hub.Options{}, "")
	res := e.notify(t, "c1", "bob", "member", "@claude hi")

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
	code, out := get("/v1/admin/runs?status=queued", adminKey)
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
		"issue:MUL-42":     "agent/claude/MUL-42",
		"blog:/posts/tmux": "agent/claude/posts-tmux",
		"topic:8812":       "agent/claude/8812",
	} {
		if got := hub.BranchName("claude", in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
