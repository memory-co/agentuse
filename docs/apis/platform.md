# 平台 API

平台只做两件事:评论存库后 **notify**,在回调路由上**接收事件**。Go 平台直接用 [`pkg/sdk`](../../pkg/sdk/sdk.go):

```go
hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop", os.Getenv("AGENTUSE_SECRET"))

// 评论存库后
res, err := hub.Notify(ctx, sdk.Comment{
    ID: c.ID, Thread: "issue:MUL-42",
    Author: sdk.Author{ID: u.ID, Name: u.Name, Role: "member"},
    Text: c.Body,
})

// 回调路由
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
    switch ev.Type {
    case "reply":  return comments.Create(ev.Thread, claudeUserID, ev.Text, ev.InReplyTo)
    case "status": return issues.SetStatus(ev.Thread, ev.Status) // 可选,博客只实现 reply
    }
    return nil // activity:按需展示
}))
```

平台还要在用户表里加一个代表 agent 的应用用户,它的 id 填进策略的 `agent_user_id`。

## 签名

两个方向用同一种签名:

```
signed  = <unix 秒> + "." + <原始请求体>
sig     = "v1=" + hex(HMAC-SHA256(secret, signed))

X-AgentUse-Timestamp: <unix 秒>
X-AgentUse-Signature: <sig>
```

时间戳与接收方时钟偏差超过 5 分钟即拒绝。实现见 [`pkg/signature`](../../pkg/signature/signature.go),其它语言照此实现即可。

## POST /v1/platforms/{platform}/notify

平台 → hub,平台密钥签名。

```json
{
  "id": "c_1001",
  "thread": "issue:MUL-42",
  "author": {"id": "u_bob", "name": "bob", "role": "member"},
  "text": "@claude 退款金额算错了",
  "created_at": "2026-10-18T03:00:00Z",
  "context": [
    {"id": "c_0999", "thread": "issue:MUL-42", "author": {"id": "u_eve", "name": "eve", "role": "guest"}, "text": "…"}
  ]
}
```

`context` 可选:线程里最近的其它评论。hub 按策略把它们标成可信 / 不可信材料交给 agent,不另存。

响应:

- **202** 唤醒了:`{"woken": true, "reason": "woken", "session_id": "s_…", "run_id": "r_…"}`
- **200** 没唤醒:`{"woken": false, "reason": "no_mention" | "self_comment" | "role_denied"}`

平台可以用 202 在界面上先放一个「正在思考」的占位。

## POST {callback_url}

hub → 平台,同一平台密钥签名。正文是 [Event](../structs/models.md#event):

```json
{"type": "reply", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "in_reply_to": "c_1001",
 "text": "已修复,见分支 agent/claude/MUL-42", "created_at": "…"}
```

```json
{"type": "status", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "status": "in_review", "created_at": "…"}
```

```json
{"type": "activity", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "activity": {"type": "action", "body": "正在运行测试"}, "created_at": "…"}
```

平台返回 2xx 即成功;非 2xx 时 hub 把失败(502 `callback_failed`)交还给 agent,由 agent 决定重试。
