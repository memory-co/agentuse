# session_runs

多 agent 會話中的一次邏輯執行。session 保存共享消息和附件；run 記錄本次執行者、運行時、租約和結果，不保存按 agent 的上下文或原生續接。設計見 [session-mentions](../designs/session-mentions.md)。

| 字段 | 含義 |
|---|---|
| id | sr_ 開頭的執行 id |
| session_id / message_id | 所屬 session 和主任務消息 |
| agent_id | 可空外鍵，agent 刪除後置空 |
| agent_key / agent_name / agent_user_id | 當時的 agent 身份快照和主人 |
| daemon_id / harness | 創建本次執行時的運行時快照，daemon 刪除後 id 爲空 |
| status | pending / running / waiting / succeeded / failed / cancelled |
| attempt_id / lease_until | 當前執行代次和租約；每次領取新建 ra_ 代次 |
| context_seq | 本代次開始讀取的 session 消息水位，僅用於排查 |
| result_message_id / error | 最終 assistant 消息和失敗說明 |
| created_at / updated_at | unix 秒 |

部分唯一索引保證同一 `(session_id, agent_id)` 最多一條 pending/running/waiting 記錄。一次消息寫入及其所有目標路由在同一 SQLite 寫事務中完成。

相關表：

- message_deliveries：每條消息、每個 agent 一個目標投遞；關聯 run_id，kind 爲 new_run/steer，status 爲 pending/delivered 或最終 succeeded/failed/cancelled。未送達插話可改掛後續 run，不復制 user message。prepared_attempt 表示已經進入某代次初始輸入的任務，啓動確認後不再重複推送。
- message_formats：記輸入是否爲文本 prompt，任意 JSON input 不強行拼接爲插話。
- message_authors：assistant 消息的 run 和 agent 身份快照。
- run_events：帶 run/attempt/event_seq 的事件，唯一約束去重。
- run_questions：帶 run/attempt/tool_use_id 的問題、答案和狀態，避免同一消息的多個 agent 衝突。
- session_requests：按 session/user/Idempotency-Key 保存規範化請求摘要和 message id，保證重試不重複派活。
- work_run_tokens：工作協調 run/attempt 的短期權限；終態刪除，過期代次失效。
- run_notifications：按線程消息去重的工作彙總通知。

seq 當前複用 messages.id，嚴格遞增但不保證連續。不新增 agent 參與關係或私有上下文表。sessions 沒有運行時或續接字段。
