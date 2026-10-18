# 平台 API

平台做三件事:设置页**替用户建 agent**、评论存库后 **notify**、在回调路由上**接收事件**。全在平台服务端调,用户浏览器不直接碰 hub。Go 平台直接用 [`pkg/sdk`](../../pkg/sdk/sdk.go):

```go
hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop", os.Getenv("AGENTUSE_SECRET"))
hub.CallbackSecret = os.Getenv("AGENTUSE_CALLBACK_SECRET") // 平台配了 callback_secret 时;空 = 用 secret 验回调

// 设置页:「接入我的 agent」
created, err := hub.CreateAgent(ctx, sdk.NewAgent{
    Owner: u.ID, OwnerName: u.Name,
    Handle:      "alice-claude",
    AgentUserID: app.ID, // 平台里同名应用用户的 id
})
// 把 created.Connect.Command 只展示给 alice:agentuse connect https://agentuse.shop.example.com ac_…

// 评论存库后
res, err := hub.Notify(ctx, sdk.Comment{
    ID: c.ID, Thread: "issue:MUL-42",
    Author: sdk.Author{ID: u.ID, Name: u.Name, Role: "member"},
    Text: c.Body,
})

// 回调路由
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
    switch ev.Type {
    case "reply":  return comments.Create(ev.Thread, agentUserOf(ev), ev.Text, ev.InReplyTo)
    case "status": return issues.SetStatus(ev.Thread, ev.Status) // 可选,博客只实现 reply
    }
    return nil // activity:按需展示
}))
```

SDK 的管理方法:`CreateAgent`、`Agents(owner)`、`Agent(id)`、`ConnectCode(id)`、`UpdateAgent(id, AgentPatch)`、`DeleteAgent(id)`、`RevokeDevice(id)`。非 2xx 返回 `*sdk.Error{Status, Code, Message}`,`Code` 就是下面的 `error`。

每个 agent 在平台的用户表里要有一个**同名的应用用户**:评论里 `@alice-claude` 提到的就是它,agent 的回复以它的名义写;它的 id 就是 agent 的 `agent_user_id`。

## 签名

两个方向用同一种签名:

```
signed  = <unix 秒> + "." + <要签的内容>
sig     = "v1=" + hex(HMAC-SHA256(secret, signed))

X-AgentUse-Timestamp: <unix 秒>
X-AgentUse-Signature: <sig>
```

「要签的内容」分两种:

| 请求 | 签什么 |
|---|---|
| notify(平台 → hub)、回调(hub → 平台) | 原始请求体 |
| 平台管理 agent 的接口(下面 `/agents`、`/devices`) | `signature.Request(方法, 路径, 请求体)` = `<方法> + " " + <路径含查询串> + "\n" + <请求体>`,如 `"DELETE /v1/platforms/shop/agents/ag_1\n"`。路径是请求行里的原样(`/v1/platforms/shop/agents?owner=u_alice`);GET / DELETE 请求体为空 |

管理接口把方法和路径也签进去,截获的一条签名请求就不能换个路径或换成 `DELETE` 重放。时间戳与接收方时钟偏差超过 5 分钟即拒绝。实现见 [`pkg/signature`](../../pkg/signature/signature.go),其它语言照此实现即可。

平台管理接口都用平台的 `secret` 签(和 notify 一样)。签名不对 → 401 `bad_signature`;平台没配置 → 404 `unknown_platform`。

## POST /v1/platforms/{platform}/notify

平台 → hub,平台密钥签名(签请求体)。

```json
{
  "id": "c_1001",
  "thread": "issue:MUL-42",
  "author": {"id": "u_alice", "name": "alice", "role": "member"},
  "text": "@alice-claude @bob-claude 退款金额算错了",
  "created_at": "2026-10-18T03:00:00Z",
  "context": [
    {"id": "c_0999", "thread": "issue:MUL-42", "author": {"id": "u_eve", "name": "eve", "role": "guest"}, "text": "…"}
  ]
}
```

`context` 可选:线程里最近的其它评论。hub 按策略把它们标成可信 / 不可信材料交给 agent,不另存。

hub 从 `text` 里找出所有独立的 `@handle`,逐个在这个平台的 agent 里找;找不到的当作平台里的普通用户,忽略。每个找到的 agent 各判一次唤醒、各排一条运行(规则见 [wake-and-trust.md](../designs/wake-and-trust.md#4-唤醒判定))。作者是这个平台**任何一个** agent 的 `agent_user_id` 时,整条评论不唤醒任何 agent(`self_comment`)。

响应:

```json
{
  "woken": true,
  "reason": "woken",
  "agents": [
    {"handle": "alice-claude", "agent_id": "ag_…", "woken": true,  "reason": "woken",    "online": true,  "session_id": "s_…", "run_id": "r_…"},
    {"handle": "bob-claude",   "agent_id": "ag_…", "woken": false, "reason": "not_owner", "online": false}
  ]
}
```

| 字段 | 说明 |
|---|---|
| `woken` | 至少叫醒了一个 |
| `reason` | 叫醒了就是 `woken`;没有 @ 到任何 agent 是 `no_mention`;作者是 agent 是 `self_comment`;否则是 `agents` 里第一个的原因 |
| `agents[]` | 评论里 @ 到、且在这个平台上存在的每个 agent。`reason`:`woken` / `not_owner` / `role_denied`。`online`:绑定的设备现在在不在线,不在线时运行排着等它上线(最多 `queue_ttl_seconds`),平台可以据此提示「alice-claude 目前离线」 |

- **202**:至少叫醒了一个。平台可以先放一个「正在思考」的占位。
- **200**:一个都没叫醒。

## 管理 agent

请求 / 响应里的 agent 是 [Agent](../structs/models.md#agent) 加两个展示字段:

```json
{
  "id": "ag_…", "platform": "shop", "handle": "alice-claude", "agent_user_id": "u_app_alice_claude", "owner": "u_alice",
  "device_id": "dv_…", "workdir": "/Users/alice/work/shop-api", "base_ref": "main", "driver": "claude",
  "wake": "owner", "actions": ["reply"], "workspace_mode": "readonly",
  "created_at": "…", "updated_at": "…",
  "online": true,
  "device": {"id": "dv_…", "name": "alice-mbp", "os": "darwin/arm64", "online": true, "last_seen": "…"}
}
```

没接入时 `device_id` / `workdir` / `base_ref` / `device` 都没有,`online` 为 false。

### POST /v1/platforms/{platform}/agents

替一个用户建 agent,同时发第一条接入码。

```json
{"owner": "u_alice", "owner_name": "alice", "handle": "alice-claude", "agent_user_id": "u_app_alice_claude",
 "wake": "owner", "actions": ["reply"], "workspace_mode": "readonly"}
```

| 字段 | 说明 |
|---|---|
| `owner` | 必填。主人的 user_id |
| `owner_name` | 主人的显示名 |
| `handle` | 必填。转小写后须是 2–40 个 `[a-z0-9_-]`,首尾是字母或数字;平台内唯一 |
| `agent_user_id` | 必填。平台里同名应用用户的 id |
| `wake` | `owner`(默认)/ `members` |
| `actions` | 可选,空 = 平台策略允许的全部;超出策略的部分存之前就被截掉 |
| `workspace_mode` | 可选,`readonly` / `readwrite`;空 = 跟平台策略 |

**201**:

```json
{
  "agent": {"id": "ag_…", "handle": "alice-claude", "online": false, "…": "…"},
  "connect": {"code": "ac_…", "expires_at": "…", "command": "agentuse connect https://agentuse.shop.example.com ac_…"}
}
```

`command` 里的地址是 hub.json 的 `public_url`;没配就按这次请求的 Host 推断(`http(s)://<Host>`)。接入码只在这里出现一次,hub 只存哈希;平台只把它展示给 agent 的主人。

- **400** `invalid`(`message` 说明哪个字段不对)
- **409** `handle_taken`

### GET /v1/platforms/{platform}/agents

`?owner=u_alice` 只列这个人的;不带列全平台。按创建时间正序:`{"agents": [Agent, …]}`。

### GET /v1/platforms/{platform}/agents/{agent}

一个 Agent;不存在或属于别的平台 → 404 `not_found`。

### PATCH /v1/platforms/{platform}/agents/{agent}

只改带了的字段:

```json
{"wake": "members", "actions": ["reply", "status"], "workspace_mode": "readwrite", "agent_user_id": "u_app_…"}
```

`actions` 给空数组 = 恢复成「平台策略允许的全部」;`workspace_mode` 给 `""` = 跟平台策略。`agent_user_id` 不能改成空。**200** 交回更新后的 Agent;**400** `invalid`;**404** `not_found`。

### DELETE /v1/platforms/{platform}/agents/{agent}

删 agent:排队中和进行中的运行判失败(`error: agent deleted`,进行中的那台设备下次心跳收到 409 就停),它的接入码一并删掉;会话和运行留作记录。**204**。

### POST /v1/platforms/{platform}/agents/{agent}/connect-code

重新发一条接入码(换电脑、上一条过期了),这个 agent 之前没用的接入码同时作废。请求体为空。**200** `{"code", "expires_at", "command"}`,同建 agent 时的 `connect`。在新电脑上用它接入,agent 就搬到新设备。

### DELETE /v1/platforms/{platform}/devices/{device}

吊销一台设备(电脑丢了):它的密钥立刻作废,之后所有请求 401;绑在上面的 agent 全部解绑,变成未接入;连着的推送连接会被关掉。**204**;不存在或属于别的平台 → 404 `not_found`。

## POST {callback_url}

hub → 平台,用平台的 `callback_secret` 签名(没配则用 `secret`)。正文是 [Event](../structs/models.md#event):

```json
{"type": "reply", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "in_reply_to": "c_1001",
 "text": "已修复,见分支 agent/alice-claude/MUL-42", "created_at": "…"}
```

```json
{"type": "status", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "status": "in_review", "created_at": "…"}
```

```json
{"type": "activity", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "activity": {"type": "action", "body": "正在运行测试"}, "created_at": "…"}
```

运行排队超过 `queue_ttl_seconds` 还没被领(设备一直不在线)时,hub 把它判失败,并主动回调一条 `error` 进度,平台可以把它显示在叫醒评论下面:

```json
{"type": "activity", "platform": "shop", "thread": "issue:MUL-42", "run_id": "r_…", "in_reply_to": "c_1001",
 "activity": {"type": "error", "body": "@alice-claude的电脑一直不在线,这条没有处理。等它上线后再 @ 一次。"}, "created_at": "…"}
```

平台返回 2xx 即成功;非 2xx 时 hub 把失败(502 `callback_failed`)交还给 agent,由 agent 决定重试(排队超时那条回调失败只记日志)。

回调事件里没有 agent id:平台要知道是哪个 agent 回的,按 `run_id` 记下 notify 响应里 `agents[].run_id` 对应的 `handle` 即可。
