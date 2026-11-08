# 接口

agentuse 服務對外的全部接口。一組接口一個文件:

Server 與 Client 均通過 Huma 自動生成 API 文檔。登錄對應服務後打開 `/api/docs`，可使用現有 cookie 調試；也可通過 Authorize 提供 Bearer token。`/api/openapi.json`、`/api/openapi.yaml` 提供 OpenAPI，`/api/schemas/{schema}.json` 提供 JSON Schema。這些入口同樣需要用戶身份認證，不接受設備或工作令牌。接口的路由、請求和響應 schema 與代碼共用定義；下列文件補充業務語義。

| 文件 | 內容 | 誰調用 |
|---|---|---|
| [auth](auth.md) | 登錄、退出、當前用戶 | 網頁 / 腳本 |
| [users](users.md) | 用戶管理(僅 admin) | 網頁 / 腳本 |
| [agents](agents.md) | 用戶起名字的 agent,綁定 / 切換運行時(daemon + harness) | 網頁 / 腳本 |
| [harnesses](harnesses.md) | harness 列表、註冊自定義 harness | 網頁 / 腳本 |
| [daemons](daemons.md) | 添加 daemon、安裝命令、開關 harness | 網頁 / 腳本 |
| [works](works.md) | 工作：一組會話 + 共用說明 | 網頁 / 腳本 |
| [sessions](sessions.md) | 開會話、發消息、看消息和回報 | 網頁 / 腳本 |
| [attachments](attachments.md) | session 附件上傳、列表、下載、刪除 | 網頁 / SDK / agentuse 客戶端 |
| [setup](setup.md) | `setup.sh` 和二進制下載 | 用戶電腦上的 curl |
| [本機頁面](local.md) | 本機賬本、命令清單、token 統計（只讀） | 本機瀏覽器 |
| [daemon](daemon.md) | daemon 註冊、自檢、WebSocket 消息 | daemon |

## 約定

- 默認請求和響應都是 JSON(`Content-Type: application/json`),時間都是 unix 秒。[附件接口](attachments.md)上傳用 multipart，下載返回二進制。
- **認證**:`/api/*` 裏除了 `/api/login`、`/api/logout` 和 `/api/daemon/*`，都要帶用戶登錄令牌(`ut_` 開頭):網頁用 cookie `agentuse_token`,腳本用 `Authorization: Bearer ut_…`。見 [auth](auth.md)。[附件接口](attachments.md)另允許受限的 daemon 密鑰和工作令牌，權限範圍見其設計。
- **代爲用戶**:admin 令牌可以再帶 `X-Agentuse-User: <用戶 id>`,按那個用戶的身份調用，用戶不存在就先建;普通用戶令牌帶這個頭返回 403。嵌入網站時網站後端就這麼用，見 [designs/embedding](../designs/embedding.md)。
- **權限**:普通用戶只能看和改自己的 daemon、agent、工作、會話、自定義 harness(例外：開會話時可以用任何人的 agent,見 [sessions](sessions.md));admin 能看和改所有人的。看不到的東西一律返回 404。
- **錯誤**:非 2xx 時響應體是 `{"error": "說明"}`。

| 狀態碼 | 含義 |
|---|---|
| 400 | 參數不對(說明在 `error` 裏) |
| 401 | 沒帶令牌 / 令牌無效 |
| 403 | 需要管理員 |
| 404 | 不存在，或不屬於你 |
