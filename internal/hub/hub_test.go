package hub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/hub"
	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/services/callback"
	"github.com/memory-co/agentuse/internal/services/token"
	"github.com/memory-co/agentuse/internal/store"
	"github.com/memory-co/agentuse/pkg/sdk"
)

// 走一遍博客里 MUL-42 的流程:bob 在工单里 @claude → hub 排队 → daemon 领取 →
// agent 用运行令牌回复并改状态 → 平台收到 hub 签名的回调。
func TestCommentToReply(t *testing.T) {
	ctx := context.Background()
	const secret = "platform-secret"

	var mu sync.Mutex
	var got []sdk.Event
	platform := httptest.NewServer(nil)
	defer platform.Close()

	cfg := models.Platform{
		ID:          "shop",
		Secret:      secret,
		CallbackURL: platform.URL + "/agentuse/callback",
		Policy: models.Policy{
			AgentName:     "claude",
			AgentUserID:   "u_claude",
			WakeRoles:     []models.Role{models.RoleMember},
			TrustedRoles:  []models.Role{models.RoleMember},
			Actions:       []models.Action{models.ActionReply, models.ActionStatus},
			WorkspaceMode: models.WorkspaceReadWrite,
		},
	}
	h := hub.New([]models.Platform{cfg}, store.NewMemory(), token.NewIssuer([]byte("hub-key")), callback.NewHTTPSender())
	hubSrv := httptest.NewServer(hub.NewServer(h, map[string]string{"alice-mbp": "daemon-key"}).Handler())
	defer hubSrv.Close()

	client := sdk.New(hubSrv.URL, "shop", secret)
	platform.Config.Handler = client.Handler(func(_ context.Context, ev sdk.Event) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
		return nil
	})

	// 访客不能唤醒。
	res, err := client.Notify(ctx, sdk.Comment{ID: "c0", Thread: "issue:MUL-42", Author: sdk.Author{ID: "eve", Role: "guest"}, Text: "@claude hi"})
	if err != nil || res.Woken || res.Reason != "role_denied" {
		t.Fatalf("guest notify: %+v %v", res, err)
	}

	res, err = client.Notify(ctx, sdk.Comment{
		ID: "c1", Thread: "issue:MUL-42", Author: sdk.Author{ID: "bob", Name: "bob", Role: "member"}, Text: "@claude 退款金额算错了",
		Context: []sdk.Comment{{ID: "c0", Author: sdk.Author{ID: "eve", Role: "guest"}, Text: "@claude hi"}},
	})
	if err != nil || !res.Woken {
		t.Fatalf("notify: %+v %v", res, err)
	}

	d := daemon.NewClient(hubSrv.URL, "alice-mbp", "daemon-key")
	if _, err := daemon.NewClient(hubSrv.URL, "alice-mbp", "wrong").Claim(ctx); err == nil {
		t.Fatal("claim with wrong key succeeded")
	}
	claimed, err := d.Claim(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Run.ID != res.RunID || claimed.Session.Branch != "agent/claude/MUL-42" {
		t.Fatalf("claimed: %+v", claimed)
	}
	if len(claimed.Run.Context) != 1 || claimed.Run.Context[0].Trusted {
		t.Fatalf("guest comment should be untrusted material: %+v", claimed.Run.Context)
	}
	if _, err := d.Claim(ctx); err != daemon.ErrNoRun {
		t.Fatalf("second claim: %v", err)
	}

	agent := func(path string, v any) int {
		b, _ := json.Marshal(v)
		req, _ := http.NewRequest(http.MethodPost, hubSrv.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+claimed.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := agent("/v1/run/reply", map[string]string{"text": "已修复,见分支 agent/claude/MUL-42", "thread": "issue:OTHER"}); code != 200 {
		t.Fatalf("reply: %d", code)
	}
	if code := agent("/v1/run/status", map[string]string{"status": "in_review"}); code != 200 {
		t.Fatalf("status: %d", code)
	}

	if err := d.Complete(ctx, claimed.Run.ID, hub.Completion{Succeeded: true, AgentSessionID: "thread_9c1"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// 运行结束后令牌不能再写。
	if code := agent("/v1/run/reply", map[string]string{"text": "late"}); code != http.StatusConflict {
		t.Fatalf("reply after complete: %d", code)
	}

	mu.Lock()
	if len(got) != 2 || got[0].Type != "reply" || got[0].Thread != "issue:MUL-42" || got[0].InReplyTo != "c1" || got[1].Status != "in_review" {
		t.Fatalf("callbacks: %+v", got)
	}
	mu.Unlock()

	// 同一线程的下一条评论续接同一会话。
	res2, err := client.Notify(ctx, sdk.Comment{ID: "c2", Thread: "issue:MUL-42", Author: sdk.Author{ID: "bob", Role: "member"}, Text: "@claude 再补个测试"})
	if err != nil || res2.SessionID != res.SessionID {
		t.Fatalf("follow-up: %+v %v", res2, err)
	}
	claimed2, err := d.Claim(ctx)
	if err != nil || claimed2.Session.AgentSessionID != "thread_9c1" {
		t.Fatalf("resume: %+v %v", claimed2.Session, err)
	}
}

func TestStatusForbiddenByPolicy(t *testing.T) {
	ctx := context.Background()
	cfg := models.Platform{ID: "blog", Secret: "s", Policy: models.Policy{
		AgentName: "claude", WakeRoles: []models.Role{models.RoleOwner}, Actions: []models.Action{models.ActionReply},
	}}
	iss := token.NewIssuer([]byte("k"))
	h := hub.New([]models.Platform{cfg}, store.NewMemory(), iss, nil)
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
