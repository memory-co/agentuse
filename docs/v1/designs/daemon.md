# daemon

> 代碼:[`client/internal/service/`](../../../client/internal/service)

daemon 是用戶電腦上的常駐進程,負責博客裏與平臺無關六件事中的三件:**連接、驅動 CLI、現場**。

## 1. 連接

daemon **主動連出** hub,所以用戶電腦在 NAT 後面也沒關係,平臺和 hub 都不需要能訪問用戶電腦。

兩種傳輸,daemon.json 的 `transport` 選:

| transport | 怎麼領 | 適合 |
|---|---|---|
| `ws`(默認) | 一條出站 WebSocket `GET /v1/daemon/connect`。空閒時發 `ready`,hub 有運行就替它領好推過來;空閒期間 hub 每 15 秒 ping。連接斷了退避 5 秒重連 | 正常使用,評論一來馬上開工 |
| `poll` | `POST /v1/daemon/claim`,有運行 200,隊列空 204,空了等 5 秒再問 | 中間有不支持 WebSocket 的代理 |

**心跳和交回總是走 HTTP**(`/v1/runs/{run}/heartbeat`、`/complete`):它們冪等、可重試,不依賴推送連接還在。推送連接只負責「有活馬上推」;如果推出去的一瞬間連接斷了,那條運行靠租約過期回收。

daemon 用 `X-AgentUse-Daemon: <id>` + `Authorization: Bearer <key>` 認證。現在 id 與 key 在 hub.json 的 `daemon_keys` 裏手工登記;改成平臺側實例之後,它們換成**設備 id + 設備密鑰**,由用戶跑安裝命令時自動登記,不再有人手寫([connect.md](connect.md))。領取時 hub 只給這臺設備上綁定的 agent 的運行([accounts.md §4](accounts.md#4-運行只派給主人的設備))。

## 2. 現場

每個會話一個 git worktree:

- 位置:`<worktree 根>/<platform>/<session id>`,根默認 `~/.agentuse/worktrees`;
- 倉庫:現在是 daemon.json 裏 `workspaces[platform].repo`,如 `~/work/shop-api`;改成平臺側實例之後,是 agent 接入時綁定的那個目錄(用戶跑安裝命令的目錄),記在 hub 的 agent 上,daemon.json 不用手寫;
- 分支:會話的 `branch`(`agent/claude/MUL-42`),第一次從 `base_ref`(默認 `main`)切出;
- 同一會話的後續運行復用同一個 worktree,現場接着上次;
- 沒配倉庫的平臺(如博客問答)給一個空目錄。

## 3. 驅動 CLI

`Driver` 接口把交互式 agent CLI 變成可被程序調用的。現在只有 Claude Code:

```
claude -p <提示詞> --output-format stream-json --verbose \
       --allowedTools 'Bash(agentuse *)' --permission-mode <plan|acceptEdits> [--resume <agent_session_id>]
```

- 環境變量注入 `AGENTUSE_URL`、`AGENTUSE_TOKEN`;
- 從 stream-json 裏讀 `session_id`,complete 時交回 hub 回填到會話;
- 提示詞的寫法見 [wake-and-trust.md §4](wake-and-trust.md#4-提示詞裏怎麼擺)。

Codex(app-server)等其它驅動按同一接口加。

## 4. 循環

```
for {
  next(ws 等推送 / poll 輪詢)
  → 準備 worktree → 驅動 agent(同時每 15 秒 heartbeat)→ complete(attempt + 成功 / 失敗 + agent_session_id)
}
```

- 一次只跑一條運行。要並行就在同一臺或多臺機器上多起幾個 daemon(不同 id);同一線程的運行由 hub 保證串行。
- 心跳收到 409(運行被回收給了別人,或已經結束)→ 立刻停掉 agent,結果不交。
- 交回失敗(網絡)→ 不重試,等租約過期 hub 回收重排;重排後的運行會 `--resume` 同一個 agent 會話、複用同一個 worktree,現場接着上次。
