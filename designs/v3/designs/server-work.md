# workService：工作、協調與線程彙總

> 本文規則已落實到 schema 7、Server API 與業務 service。使用 device/conversation 命名；session_id 只引用 Client 本地執行會話。時間為 Unix 秒，JSON 使用 TEXT，關聯由 service 在事務內校驗與清理，不聲明數據庫外鍵。

## 職責

workService 將多個 conversation 組織成一項工作。一個 work 有一個協調 conversation、一個協調 agent 和若干線程 conversation。它複用 conversationService/runService 的對話與任務模型，不引入另一種執行實體。

彙總檢查原消息的 message_agents 關係：rejected 可直接計為失敗，accepted 必須等對應 run 的實際終態；pending、sent、unknown 都不能視為完成。answer/cancel 不增加消息的目標數。

## 操作與協作

| 操作 | 行爲 |
|---|---|
| 創建 work | 通過 agentService 校驗協調 agent，通過 conversationService 創建協調 conversation；在同一事務建立關聯 |
| 創建線程 | 以工作範圍權限調用 conversationService 創建關聯 work_id 的 conversation |
| 修改配置 | 維護名稱、說明和協調 agent；忙碌時拒絕不安全的執行選擇變更 |
| 彙總結果 | 在最終回覆已由 conversationService 保存後，檢查原訊息對各目標的 send 輸入與對應 run 是否已結束；將彙總作爲消息發到協調 conversation |
| 刪除 work | 檢查相關任務空閒，再經 conversationService 刪除所屬對話及 work 數據 |

彙總使用 run_notifications 去重：記錄“該線程消息已彙報”與向協調 conversation 發彙總消息，必須在同一事務完成。此事務在原生結果與最終回覆提交之後進行；協調者暫時不可用時不標記已彙報，啟動、恢復 harness 或更新 Work 配置時掃描補償。不能只收到 run 結束事件就彙總，因爲最終回覆可能還沒消費入庫；需要等回覆落庫通知，並能通過持久化消息/message_agents 掃描補償。

## Agent 版本銜接

Work 仍選擇穩定的 coordinator_agent_id。每條協調輸入和線程輸入在入隊時固定目標 agent_version_id；發佈新 agent 或 skill 版本不改已有排隊輸入，也不自動改寫正在工作的協調原生上下文。下一條使用新版的輸入綁定新的 client session 並執行 init，workspace 和 server 共享歷史仍屬於原 conversation。

Work 說明和協調者業務規則仍由 server 隨輸入準備，不混入可變的 agent 初始化快照。Skill 依賴來自該輸入固定的 agent_version；WorkToken 僅授權對應輸入及其引用的資源，不授予任意技能庫訪問權。

## Server 工具與授權（已實現）

Work 是 Server 的業務實體，可跨多個 Client；協調 Agent 的物理執行位置不改變 Work 的歸屬。Client 只驅動輸入、原生 run 和文件讀寫，不含 Work 命令、角色判斷或工作令牌處理。

人類使用 `agentuse-server work`，通過 `/api/works/{work_id}/threads` 及其消息、讀取和 resolve 接口操作。Server 校驗工作所有者/管理員身份，並在每次請求中核對線程歸屬；管理員創建的線程仍屬於工作所有者。

Server 在協調輸入的 context 文本中提供工具使用說明。`GET /api/agent-tools/work.py` 返回 Server 維護的 Python 工具；工具在協調 Agent 所在設備執行，通過 `context status` 查詢 conversation/agent/run 身份，由 Server 按 run 關聯來源消息，再用當前設備憑據請求 `POST /api/agent-tools/work/authorize`。Client 不解析工具內容或推斷角色。

Server 根據已投遞的消息/Agent 關係、執行設備和 Work 當前協調者確認授權。工具在授權階段短暫等待 accepted 上報，再依實際 run UUID 取得來源消息；不使用啓動時的 message 環境變量猜測當前輸入。普通對話、工作線程、其他設備及已結束/拒絕的輸入不能取得協調權限。

工作令牌只存在於 Server 工具與 API 的授權交互中，不寫入 Client 環境或 WebSocket message。工具每次調用取得對應來源輸入的授權，Server 逐次驗證其工作範圍及輸入仍在 sent/accepted；輸入終態後失效。Client 的 context 輸出不返回令牌。

協調規則、工作說明、選擇 Agent、跨 Client 派發、結果彙總和去重均由 Server 維護。這些規則不混入 Agent 的不可變 init 資源，也不由 Client 判定。

`coordinator_id` 是協調 conversation 的 UUID；`conversations.work_id` 是對話歸屬。兩者僅以標識關聯，由 workService 在創建/刪除事務裏保證一致，不聲明外鍵。

## 表結構

### works

工作名稱、說明、所有者、協調 conversation 和協調 agent。協調 agent 被刪除時，由 agentService 協調 workService 清空 coordinator_agent_id；重新配置之前不能繼續派協調任務。

```sql
CREATE TABLE works (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    coordinator_id TEXT NOT NULL UNIQUE,
    coordinator_agent_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX works_user
    ON works(user_id, updated_at);
```

### work_run_tokens

令牌只存哈希，綁定 work_id、message_id、agent_id；有效性核對當前協調者與該消息/Agent 的傳輸及執行狀態，不另建 run_input 身份。

```sql
CREATE TABLE work_run_tokens (
    token_hash TEXT PRIMARY KEY,
    work_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    agent_id TEXT NOT NULL
);
```

### run_notifications

已彙總的線程 user message_id。按消息而不是 run 去重，因爲一條消息可以有多個目標，也可能經歷插話回退後的後續 run。

```sql
CREATE TABLE run_notifications (
    message_id TEXT NOT NULL PRIMARY KEY
);
```

## 相關設計

[Server 總覽](server-main.md) · [userService](server-user.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [conversationService](server-conversation.md) · [runService](server-run.md) · [fileService](server-file.md)
