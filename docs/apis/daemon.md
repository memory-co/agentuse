# daemon API

用户电脑 → hub。分两段:

- **接入**:`POST /v1/connect`,接入码本身就是凭据,不带别的认证。
- **之后的所有请求**带设备身份:

```
X-AgentUse-Device: dv_…
Authorization: Bearer dk_…
```

设备 id 不存在、密钥不对、设备已被吊销 → 401 `unauthorized`。设备 id 和密钥由 `agentuse connect` 写进 [daemon.json](../structs/filesystem.md#daemonjson);hub 只存密钥的 sha256。

**在线**:设备有一条活着的推送连接(`/v1/daemon/connect`),或 45 秒内用设备身份请求过任何接口(轮询模式),就算在线。只兑换接入码不算。

## POST /v1/connect

`agentuse connect` 兑换接入码:把码对应的 agent 绑到这台电脑和目录上。

```json
{
  "code": "ac_7Hq2mX9pLwQ…",
  "device_name": "alice-mbp",
  "os": "darwin/arm64",
  "dir": "/Users/alice/work/shop-api",
  "base_ref": "main",
  "devices": [{"id": "dv_…", "key": "dk_…"}]
}
```

| 字段 | 说明 |
|---|---|
| `code` | 接入码,一次性,15 分钟有效 |
| `device_name` / `os` | 只用于展示 |
| `dir` | 必填。agent 的工作目录(git 仓库根) |
| `base_ref` | worktree 从哪个分支切,默认 `main` |
| `devices` | 可选。这台电脑在这个 hub 上已有的设备身份;其中有**同平台、同主人**的就复用,不发新密钥。认不出来的(密钥不对、已吊销)忽略 |

hub 先认 `devices`,再原子地取走接入码(取走即作废)。复用不到设备就新建一台。agent 原来绑在别的设备上的,就此搬过来;`workdir` / `base_ref` 换成这次的。

**200**:

```json
{
  "platform": "shop",
  "agent": {"id": "ag_…", "handle": "alice-claude", "device_id": "dv_…", "workdir": "/Users/alice/work/shop-api", "base_ref": "main", "…": "…"},
  "device_id": "dv_…",
  "device_key": "dk_…"
}
```

`device_key` **只在新建设备时出现,且只出现这一次**;复用设备时没有这个字段,沿用已有的密钥。

- **401** `bad_code`:接入码不存在、已用过、已过期,或它的 agent 已被删。
- **400** `invalid`:缺 `dir`。

## GET /v1/device

这台设备自己看:

```json
{
  "platform": "shop",
  "device": {"id": "dv_…", "name": "alice-mbp", "os": "darwin/arm64", "online": true, "last_seen": "…"},
  "agents": [{"id": "ag_…", "handle": "alice-claude", "workdir": "/Users/alice/work/shop-api", "wake": "owner", "…": "…"}]
}
```

`agents` 是绑在这台设备上的 [Agent](../structs/models.md#agent)。`agentuse agents` 用它。

## POST /v1/device/disconnect

```json
{"agent": "alice-claude"}
```

把 handle 对应的 agent 从这台设备上解绑(清空 `device_id` / `workdir` / `base_ref`),agent 本身保留,变成未接入。

- **204** 解绑了。
- **404** `not_found`:没有这个 agent,或它没绑在这台设备上。
- **400** `bad_body`:缺 `agent`。

## GET /v1/daemon/connect

WebSocket 推送连接(`transport: ws`,默认)。连上之后:

| 方向 | 消息 | 说明 |
|---|---|---|
| daemon → hub | `{"type": "ready"}` | 我空闲了。每跑完一条再发一次 |
| hub → daemon | `{"type": "run", "claimed": {run, session, agent, token}}` | 已经替你领好的一条运行,内容同下面 claim 的 200 |
| hub → daemon | WebSocket ping | 空闲等待期间每 15 秒一次;daemon 读的时候自动回 pong,不通即断开 |

连接上只有这两种消息;心跳和交回走下面的 HTTP 接口。推出去的运行如果 daemon 没收到(连接正好断了),租约过期后回收重排。设备在连着的时候被吊销,hub 下一次检查时以 policy violation 关掉连接,之后重连是 401。

## POST /v1/daemon/claim

轮询领取(`transport: poll`)。原子地领取最早排队、**所属 agent 绑定在这台设备上**、且所属会话没有进行中运行的一条:标为 `claimed`,`attempts + 1`,设租约。请求体为空。

- **204** 没有可领的。
- **200**:

```json
{
  "run": {
    "id": "r_…", "session_id": "s_…", "platform": "shop", "agent_id": "ag_…", "thread": "issue:MUL-42",
    "trigger": {"id": "c_1001", "author": {"id": "u_alice", "name": "alice", "role": "member"}, "text": "@alice-claude 退款金额算错了"},
    "context": [{"comment": {"…": "…"}, "trusted": false}],
    "actions": ["reply", "status"], "workspace_mode": "readwrite",
    "status": "claimed", "attempts": 1, "device_id": "dv_…",
    "lease_until": "…", "created_at": "…", "claimed_at": "…"
  },
  "session": {"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_id": "ag_…", "agent_session_id": "", "branch": "agent/alice-claude/MUL-42", "…": "…"},
  "agent": {"id": "ag_…", "handle": "alice-claude", "workdir": "/Users/alice/work/shop-api", "base_ref": "main", "driver": "claude", "…": "…"},
  "token": "aut_…"
}
```

`agent` 告诉 daemon 在哪个目录、从哪个分支切 worktree、用什么驱动,所以 daemon.json 里不用配仓库。`token` 只给这次运行,daemon 注入 `AGENTUSE_TOKEN` 交给 agent,自己不用它。

## POST /v1/runs/{run}/heartbeat

跑运行期间每 15 秒一次,把租约续到 `now + lease_seconds`。

```json
{"attempt": 1}
```

- **200** 交回更新后的运行。
- **409** `run_state`:这次领取已经不归你了(租约过期被回收,运行已结束,或 agent 被删后判了失败)。daemon 应立刻停掉 agent。

## POST /v1/runs/{run}/complete

```json
{"attempt": 1, "succeeded": true, "agent_session_id": "thread_9c1"}
```

```json
{"attempt": 1, "succeeded": false, "error": "git worktree add: …"}
```

- **200** 交回更新后的运行。`agent_session_id` 非空时回填到会话,之后的运行用它续接;同一会话排着的下一条此时才能被领取。
- **409** `run_state`:运行不在 claimed,不是这台设备领的,或 attempt 对不上(已被回收重领)。
