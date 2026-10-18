# daemon API

daemon → hub。每个请求带:

```
X-AgentUse-Daemon: alice-mbp
Authorization: Bearer <daemon key>
```

## POST /v1/daemon/claim

领取最早排队的一条运行,原子地把它标为 `claimed`。请求体为空。

- **204** 队列空。
- **200**:

```json
{
  "run": {
    "id": "r_…", "session_id": "s_…", "platform": "shop", "thread": "issue:MUL-42",
    "trigger": {"id": "c_1001", "author": {"id": "u_bob", "name": "bob", "role": "member"}, "text": "@claude 退款金额算错了"},
    "context": [{"comment": {"…": "…"}, "trusted": false}],
    "actions": ["reply", "status"], "workspace_mode": "readwrite",
    "status": "claimed", "daemon_id": "alice-mbp", "created_at": "…", "claimed_at": "…"
  },
  "session": {"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_session_id": "", "branch": "agent/claude/MUL-42", "…": "…"},
  "token": "aut_…"
}
```

`token` 只给这次运行,daemon 注入 `AGENTUSE_TOKEN` 交给 agent,自己不用它。

## POST /v1/runs/{run}/complete

```json
{"succeeded": true, "agent_session_id": "thread_9c1"}
```

```json
{"succeeded": false, "error": "git worktree add: …"}
```

- **200** 交回更新后的运行。`agent_session_id` 非空时回填到会话,之后的运行用它续接。
- **409** `run_state`:运行不在 claimed,或不是这个 daemon 领的。

## 传输(规划)

现在是轮询。之后改成出站 WebSocket `GET /v1/daemon/connect`:

| 方向 | 消息 | 等价于 |
|---|---|---|
| hub → daemon | `{"type": "run", "claimed": {run, session, token}}` | claim 的 200 |
| daemon → hub | `{"type": "complete", "run_id": "…", …Completion}` | complete |
| 双向 | `{"type": "ping"}` / `{"type": "pong"}`,每 15 秒 | 心跳 |

语义不变,只是从「问」变成「推」。
