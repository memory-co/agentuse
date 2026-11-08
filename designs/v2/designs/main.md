# agentuse v2 總體設計

用戶電腦上的 daemon 主動通過 WebSocket 連接服務，領取執行、運行本地 harness 並彙報進度和結果。

1. Agent 是可命名的執行者，綁定可更換的 daemon + harness 運行時。
2. Session 屬於創建者，保存所有 agent 共用的消息和附件，不綁定執行者。
3. 用戶發送消息並用 to_agent_ids @ 一個或多個 agent；空閒目標創建 run，忙碌目標收到插話，無目標時保存筆記。
4. Run 固定本次執行的運行時快照，以 attempt 和租約控制執行權。daemon 讀取共享歷史後啓動新的原生會話，完成時寫入帶署名的 assistant 消息。
5. 工作包含協調者 session、線程 session 和共用說明；協調者 agent 配置屬於工作，執行期間使用作用域受限的工作令牌。

SQLite 保存用戶、機器、agent、工作、session、消息、執行和附件元數據；文件內容走 HTTP。舊數據庫須清空重建，服務不提供舊 session 模型兼容。

詳細說明：[共享 session](session-mentions.md) · [附件](attachment.md) · [接口](../apis/README.md) · [session 表](../tables/sessions.md) · [run 表](../tables/session_runs.md)。
