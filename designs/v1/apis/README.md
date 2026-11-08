# API Reference

hub 的 HTTP 接口全部掛在 `/v1/` 下,請求 / 響應 JSON。起服務 `agentuse hub start`,默認 `http://127.0.0.1:7700`。

- 機制 / 設計決策見 [`../designs/`](../designs/README.md)
- 數據結構見 [`../structs/`](../structs/README.md)

三類調用方,三種認證:

| 調用方 | 認證 | 頁面 |
|---|---|---|
| 平臺 | 請求體 HMAC 簽名(平臺密鑰) | [platform.md](platform.md) |
| daemon | `X-AgentUse-Daemon` + `Authorization: Bearer <daemon key>` | [daemon.md](daemon.md) |
| agent | `Authorization: Bearer <運行令牌>` | [agent.md](agent.md) |
| 實例主人 | `Authorization: Bearer <admin key>` | [admin.md](admin.md) |

| 方法 | 端點 | 調用方 | 說明 |
|---|---|---|---|
| `GET` | `/v1/health` | 任何 | 健康檢查 |
| `POST` | `/v1/platforms/{platform}/notify` | 平臺 | 一條評論存庫了;hub 判喚醒、找會話、排運行 |
| `POST` | `{callback_url}` | hub → 平臺 | hub 簽名的事件:reply / status / activity |
| `GET` | `/v1/daemon/connect` | daemon | WebSocket 推送連接:發 ready,hub 推一條領好的運行 |
| `POST` | `/v1/daemon/claim` | daemon | 輪詢領取下一條運行 + 運行令牌;隊列空 204 |
| `POST` | `/v1/runs/{run}/heartbeat` | daemon | 續租 |
| `POST` | `/v1/runs/{run}/complete` | daemon | 交回結果,回填 agent 會話 id |
| `GET` | `/v1/run` | agent | 看這枚令牌的範圍 |
| `POST` | `/v1/run/reply` | agent | 回覆評論 |
| `POST` | `/v1/run/status` | agent | 改線程對象的狀態 |
| `POST` | `/v1/run/activity` | agent | 報進度 |
| `GET` | `/v1/admin/sessions` | 實例主人 | 列會話 |
| `GET` | `/v1/admin/runs` | 實例主人 | 列運行(按平臺 / 會話 / 狀態過濾) |
| `GET` | `/v1/admin/runs/{run}` | 實例主人 | 讀一條運行 |

命令行:[cli.md](cli.md)。

## 錯誤

統一 `{"error": "<code>"}`:

| 狀態 | error | 什麼時候 |
|---|---|---|
| 400 | `bad_body` / `bad_notification` | 請求體不是合法 JSON,或 notify 缺 `id` / `thread` |
| 401 | `bad_signature` | 平臺簽名缺失、不對或時間戳超出 5 分鐘 |
| 401 | `unauthorized` | daemon id 未登記或密鑰不對;管理密鑰不對或管理接口未開 |
| 401 | `bad_token` | 運行令牌格式不對、簽名不對或已過期 |
| 403 | `forbidden` | 令牌的動作範圍不含這個動作(如博客上 `status`) |
| 404 | `unknown_platform` / `not_found` | 平臺沒配置 / 運行不存在 |
| 409 | `run_state` | 運行不在 claimed 狀態(已結束),不是這個 daemon 領的,或已被回收重領(attempt 對不上) |
| 502 | `callback_failed` | 回調平臺失敗(平臺返回非 2xx 或連不上) |
