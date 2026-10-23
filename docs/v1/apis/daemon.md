# daemon API

daemon → hub。每个请求带:

```
X-AgentUse-Daemon: alice-mbp
Authorization: Bearer <daemon key>
```

## GET /v1/daemon/connect

WebSocket 推送连接(`transport: ws`,默认)。连上之后:

| 方向 | 消息 | 说明 |
|---|---|---|
| daemon → hub | `{"type": "ready"}` | 我空闲了。每跑完一条再发一次 |
| hub → daemon | `{"type": "run", "claimed": {run, session, token}}` | 已经替你领好的一条运行,内容同下面 claim 的 200 |
| hub → daemon | WebSocket ping | 空闲等待期间每 15 秒一次;daemon 读的时候自动回 pong,不通即断开 |

连接上只有这两种消息;心跳和交回走下面的 HTTP 接口。推出去的运行如果 daemon 没收到(连接正好断了),租约过期后回收重排。

## POST /v1/daemon/claim

轮询领取(`transport: poll`)。原子地领取最早排队、且所属线程没有进行中运行的一条:标为 `claimed`,`attempts + 1`,设租约。请求体为空。

- **204** 队列空。
- **200**:

```json
{
  "run": {
    "id": "r_…", "session_id": "s_…", "platform": "shop", "thread": "issue:MUL-42",
    "trigger": {"id": "c_1001", "author": {"id": "u_bob", "name": "bob", "role": "member"}, "text": "@claude 退款金额算错了"},
    "context": [{"comment": {"…": "…"}, "trusted": false}],
    "actions": ["reply", "status"], "workspace_mode": "readwrite",
    "status": "claimed", "attempts": 1, "daemon_id": "alice-mbp",
    "lease_until": "…", "created_at": "…", "claimed_at": "…"
  },
  "session": {"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_session_id": "", "branch": "agent/claude/MUL-42", "…": "…"},
  "token": "aut_…"
}
```

`token` 只给这次运行,daemon 注入 `AGENTUSE_TOKEN` 交给 agent,自己不用它。

## POST /v1/runs/{run}/heartbeat

跑运行期间每 15 秒一次,把租约续到 `now + lease_seconds`。

```json
{"attempt": 1}
```

- **200** 交回更新后的运行。
- **409** `run_state`:这次领取已经不归你了(租约过期被回收,或运行已结束)。daemon 应立刻停掉 agent。

## POST /v1/runs/{run}/complete

```json
{"attempt": 1, "succeeded": true, "agent_session_id": "thread_9c1"}
```

```json
{"attempt": 1, "succeeded": false, "error": "git worktree add: …"}
```

- **200** 交回更新后的运行。`agent_session_id` 非空时回填到会话,之后的运行用它续接;同一线程排着的下一条此时才能被领取。
- **409** `run_state`:运行不在 claimed,不是这个 daemon 领的,或 attempt 对不上(已被回收重领)。
