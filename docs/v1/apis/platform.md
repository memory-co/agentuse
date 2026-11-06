# 平臺 API

平臺只做兩件事:評論存庫後 **notify**,在回調路由上**接收事件**。Go 平臺直接用 [`pkg/sdk`](../../../pkg/sdk/sdk.go):

```go
hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop", os.Getenv("AGENTUSE_SECRET"))
hub.CallbackSecret = os.Getenv("AGENTUSE_CALLBACK_SECRET") // 平臺配了 callback_secret 時;空 = 用 secret 驗回調

// 評論存庫後
res, err := hub.Notify(ctx, sdk.Comment{
    ID: c.ID, Thread: "issue:MUL-42",
    Author: sdk.Author{ID: u.ID, Name: u.Name, Role: "member"},
    Text: c.Body,
})

// 回調路由
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
    switch ev.Type {
    case "reply":  return comments.Create(ev.Thread, claudeUserID, ev.Text, ev.InReplyTo)
    case "status": return issues.SetStatus(ev.Thread, ev.Status) // 可選,博客只實現 reply
    }
    return nil // activity:按需展示
}))
```

平臺還要在用戶表裏加一個代表 agent 的應用用戶,它的 id 填進策略的 `agent_user_id`。

## 簽名

兩個方向用同一種簽名:

```
signed  = <unix 秒> + "." + <原始請求體>
sig     = "v1=" + hex(HMAC-SHA256(secret, signed))

X-AgentUse-Timestamp: <unix 秒>
X-AgentUse-Signature: <sig>
```

時間戳與接收方時鐘偏差超過 5 分鐘即拒絕。實現見 [`pkg/signature`](../../../pkg/signature/signature.go),其它語言照此實現即可。

## POST /v1/platforms/{platform}/notify

平臺 → hub,平臺密鑰簽名。

```json
{
  "id": "c_1001",
  "thread": "issue:MUL-42",
  "author": {"id": "u_bob", "name": "bob", "role": "member"},
  "text": "@claude 退款金額算錯了",
  "created_at": "2026-10-18T03:00:00Z",
  "context": [
    {"id": "c_0999", "thread": "issue:MUL-42", "author": {"id": "u_eve", "name": "eve", "role": "guest"}, "text": "…"}
  ]
}
```

`context` 可選:線程裏最近的其它評論。hub 按策略把它們標成可信 / 不可信材料交給 agent,不另存。

響應:

- **202** 喚醒了:`{"woken": true, "reason": "woken", "session_id": "s_…", "run_id": "r_…"}`
- **200** 沒喚醒:`{"woken": false, "reason": "no_mention" | "self_comment" | "role_denied"}`

平臺可以用 202 在界面上先放一個「正在思考」的佔位。

## POST {callback_url}

hub → 平臺,用平臺的 `callback_secret` 簽名(沒配則用 `secret`)。正文是 [Event](../structs/models.md#event):

```json
{"type": "reply", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "in_reply_to": "c_1001",
 "text": "已修復,見分支 agent/claude/MUL-42", "created_at": "…"}
```

```json
{"type": "status", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "status": "in_review", "created_at": "…"}
```

```json
{"type": "activity", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "activity": {"type": "action", "body": "正在運行測試"}, "created_at": "…"}
```

平臺返回 2xx 即成功;非 2xx 時 hub 把失敗(502 `callback_failed`)交還給 agent,由 agent 決定重試。
