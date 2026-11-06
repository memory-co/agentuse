# workService：工作、協調與線程彙總

> v3 目標設計，尚未據此修改代碼或數據庫。統一使用 device 命名；本文 SQL 表示目標結構，不是線上遷移腳本。時間字段除另行說明外，均爲 Unix 秒，布爾值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之間只保留關聯字段，不聲明數據庫外鍵；引用校驗和關聯清理由 service 在事務內完成。

## 職責

workService 將多個 conversation 組織成一項工作。一個 work 有一個協調 conversation、一個協調 agent 和若干線程 conversation。它複用 conversationService/runService 的對話與任務模型，不引入另一種執行實體。

彙總只考慮 kind=send 的 run_inputs：rejected 可直接計為失敗，applied 須等對應 run 的原生終態；pending、sent、unknown 或尚無 run 歸屬的 applied 都不能視為完成。reply/cancel 不增加訊息的執行目標數。

## 操作與協作

| 操作 | 行爲 |
|---|---|
| 創建 work | 通過 agentService 校驗協調 agent，通過 conversationService 創建協調 conversation；在同一事務建立關聯 |
| 創建線程 | 以工作範圍權限調用 conversationService 創建關聯 work_id 的 conversation |
| 修改配置 | 維護名稱、說明和協調 agent；忙碌時拒絕不安全的執行選擇變更 |
| 彙總結果 | 在最終回覆已由 conversationService 保存後，檢查原訊息對各目標的 send 輸入與對應 run 是否已結束；將彙總作爲消息發到協調 conversation |
| 刪除 work | 檢查相關任務空閒，再經 conversationService 刪除所屬對話及 work 數據 |

彙總使用 run_notifications 去重：記錄“該線程消息已彙報”與向協調 conversation 發彙總消息，必須在同一事務完成。不能只收到 run 結束事件就彙總，因爲最終回覆可能還沒消費入庫；需要等回覆落庫通知，並能通過持久化消息/run_inputs 掃描補償。

## Agent 版本銜接（目標）

Work 仍選擇穩定的 coordinator_agent_id。每條協調輸入和線程輸入在入隊時固定目標 agent_version_id；發佈新 agent 或 skill 版本不改已有排隊輸入，也不自動改寫正在工作的協調原生上下文。下一條使用新版的輸入綁定新的 client session 並執行 init，workspace 和 server 共享歷史仍屬於原 conversation。

Work 說明和協調者業務規則仍由 server 隨輸入準備，不混入可變的 agent 初始化快照。Skill 依賴來自該輸入固定的 agent_version；WorkToken 僅授權對應輸入及其引用的資源，不授予任意技能庫訪問權。

## 工作令牌

領取輸入時簽發限定 work/input 的令牌；此時實際 run 可能還不存在。每次使用檢查輸入為 kind=send、傳輸狀態為 sent/applied、對應 run 尚未終止，以及 work 範圍。回報 accepted 前可用固定輸入授權；對應 run 結束或輸入被拒絕後授權失效，不把令牌當作長期用戶身份。

協調者職責、派活規則及工作說明由 server 在領取輸入時組織到 Instructions，再隨任務下發。只有協調 conversation 當前指定的協調 agent 獲得協調者指令；線程和其他 agent 只收到共享工作說明。client 不根據 WorkToken 推斷角色或生成協調策略，只透傳憑據和已準備的指令。

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

令牌只存哈希，綁定 work 和輸入分配 run_input_id；有效性結合 send 輸入的傳輸狀態及對應 runs.status 檢查，不能把 applied 當作執行完成。

```sql
CREATE TABLE work_run_tokens (
    token_hash TEXT PRIMARY KEY,
    work_id TEXT NOT NULL,
    run_input_id INTEGER NOT NULL
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
