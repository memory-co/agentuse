# daemon API

daemon → hub。每個請求帶:

```
X-AgentUse-Daemon: alice-mbp
Authorization: Bearer <daemon key>
```

## GET /v1/daemon/connect

WebSocket 推送連接(`transport: ws`,默認)。連上之後:

| 方向 | 消息 | 說明 |
|---|---|---|
| daemon → hub | `{"type": "ready"}` | 我空閒了。每跑完一條再發一次 |
| hub → daemon | `{"type": "run", "claimed": {run, session, token}}` | 已經替你領好的一條運行,內容同下面 claim 的 200 |
| hub → daemon | WebSocket ping | 空閒等待期間每 15 秒一次;daemon 讀的時候自動回 pong,不通即斷開 |

連接上只有這兩種消息;心跳和交回走下面的 HTTP 接口。推出去的運行如果 daemon 沒收到(連接正好斷了),租約過期後回收重排。

## POST /v1/daemon/claim

輪詢領取(`transport: poll`)。原子地領取最早排隊、且所屬線程沒有進行中運行的一條:標爲 `claimed`,`attempts + 1`,設租約。請求體爲空。

- **204** 隊列空。
- **200**:

```json
{
  "run": {
    "id": "r_…", "session_id": "s_…", "platform": "shop", "thread": "issue:MUL-42",
    "trigger": {"id": "c_1001", "author": {"id": "u_bob", "name": "bob", "role": "member"}, "text": "@claude 退款金額算錯了"},
    "context": [{"comment": {"…": "…"}, "trusted": false}],
    "actions": ["reply", "status"], "workspace_mode": "readwrite",
    "status": "claimed", "attempts": 1, "daemon_id": "alice-mbp",
    "lease_until": "…", "created_at": "…", "claimed_at": "…"
  },
  "session": {"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_session_id": "", "branch": "agent/claude/MUL-42", "…": "…"},
  "token": "aut_…"
}
```

`token` 只給這次運行,daemon 注入 `AGENTUSE_TOKEN` 交給 agent,自己不用它。

## POST /v1/runs/{run}/heartbeat

跑運行期間每 15 秒一次,把租約續到 `now + lease_seconds`。

```json
{"attempt": 1}
```

- **200** 交回更新後的運行。
- **409** `run_state`:這次領取已經不歸你了(租約過期被回收,或運行已結束)。daemon 應立刻停掉 agent。

## POST /v1/runs/{run}/complete

```json
{"attempt": 1, "succeeded": true, "agent_session_id": "thread_9c1"}
```

```json
{"attempt": 1, "succeeded": false, "error": "git worktree add: …"}
```

- **200** 交回更新後的運行。`agent_session_id` 非空時回填到會話,之後的運行用它續接;同一線程排着的下一條此時才能被領取。
- **409** `run_state`:運行不在 claimed,不是這個 daemon 領的,或 attempt 對不上(已被回收重領)。
