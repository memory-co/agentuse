# runService：輸入分配與執行事實

完整 WebSocket 報文、字段和恢復規則見 [protocol.md](protocol.md)。

Server 分配輸入，client 執行並報告事實。**run_id 只由適配器的 accepted 事件產生**。Server 不生成執行 UUID，不創建 attempt、租約或原生執行狀態，也不推斷一次輸入應該 start、steer 還是 queue。

## 消息、輸入與 Run

一條消息 @ 兩個 agent，先寫兩條 run_prompts，此時 run_id 爲空，HTTP 返回 deliveries，runs 可以爲空。領取成功表示輸入已交付，不等於適配器已接受。

```text
M1 @coder @reviewer → P1、P2（pending）
client 領取 P1 → P1.sent
accepted(run_id=R1,input_id=I1) → 建 R1，P1 綁定 R1/I1
M2 @coder → P3 → client 再次 send
accepted(run_id=R1,input_id=I2) → P3 也綁定 R1：真實插話
或 accepted(run_id=R3,input_id=I2) → 建 R3：真實排隊
```

一個 message 對多個 run，一個 run 可以消費多條 message。關係以 accepted 爲準，不根據“上一個 run 尚未結束”提前合併。first_message_id 來自該 UUID 第一條 accepted 對應的消息。輸入分配 ID 只用於傳輸關聯，不是另一個執行身份。

## Agent 版本關聯

當前實現及 SQL 見後文；部署需 server/client 同步升級。版本定義見 [agentService](server-agent.md)。

| 數據 | 固定關聯 | 固定時機 |
|---|---|---|
| run_prompts | agent_version_id | 保存消息併爲目標建立輸入時，與消息在同一事務固定當前版本 |
| runs | agent_version_id | 第一條 accepted 從對應 prompt 複製；同 run 後續輸入只能來自相同版本 |

兩列均爲非空版本 UUID，不存可變化的 latest，不冗餘名稱或版本配置。Agent 版本指向固定 device/harness/model/指令；agentService 擁有關聯，skillService 提供其固定 skill_versions 的元數據和下載授權。顯示版本號時關聯 agent_versions 查詢。

Claim 使用固定版本生成 dispatch，不在領取時再查當前 head。重連仍重發原 dispatch。下行增加 agent_version_id、model、agent_instructions 和 skill 包版本清單，詳情見 [protocol.md 的版本字段](protocol.md#agent-versioning)。Server 只准備版本化業務資源，不生成 harness 命令。

Client 活動流必須按 conversation/agent/agent_version 匹配；容量不足時，新版本不能因爲舊 agent 仍在執行就被當成可插話輸入。新版使用新 session/events 並 init，舊版本可以繼續完成。同一個 native run UUID 不能接收跨 agent_version 的輸入；server 校驗業務版本歸屬，不替適配器決定實際 run UUID。

最終 agent 回覆同時記錄 author_agent_id 與 author_agent_version_id，仍通過 reply_to_message_id 回覆首條 message，messages 不增加 run_id。具體目標消息結構見 [conversationService](server-conversation.md)。

## 與其他 Service 的關係

conversationService 保存共享歷史並建立輸入分配；agentService 提供執行設備和 harness 標籤；skillService 管獨立的技能包版本；deviceService 管連接和上報能力。runService 在領取時準備共享歷史、附件引用和業務指令。workService 提供工作說明、協調規則和授權。所有這些都是 server 業務，client 不推斷角色。

Client 的 harnessService 管適配器 YAML、進程、原生協議、hook、skill、自動授權及插話策略。Server 不導入這些實現，不保存全局 harness 配置。設備上報的名稱、版本、revision 用作選擇和歷史展示。

## WebSocket 協議

版本爲 harnessctl-events-v2，舊協議不能接入。hello 報告可用 harness；claim 帶空閒 session 數和仍可接收輸入的 conversation/agent，server 返回 prompt 或 no_task。

下行 prompt 包含分配 ID、conversation、message、agent、harness 標籤、準備好的文本、共享上下文及附件引用；不含原生命令或 attempt。補充輸入仍是 prompt。reply 攜帶 request_id 與不透明 JSON answer；cancel 針對 client session，並攜帶目標 run_id 防止延遲取消作用於後續交互。

上行 event 包含 conversation、agent、client session、實際 harness/revision、源 JSONL 行結束字節偏移，以及完整公共事件。accepted 額外關聯 prompt_id。Server 原樣保存公共事件，不把原生工具幀轉換成業務類型。

| 公共類型 | Server 行爲 |
|---|---|
| accepted | 驗證輸入分配歸屬，按適配器 UUID 建 run，綁定 input_id |
| message | 保存正文事件，供執行詳情讀取 |
| request | 保存完整請求快照及 pending/replied/expired 狀態；不解釋原生權限工具 |
| result | 保存 completed/failed/cancelled、input_ids、輸出、錯誤、用量和費用原文；投影 run 與輸入終態 |
| log | 保存可觀測事件；允許在 accepted 之前出現，不據日誌猜測狀態 |

請求答案由 client/適配器解釋。控制命令送達確認不等於 request 已 replied；必須等待 client 的 request 事件。取消 HTTP 成功只表示已排入控制隊列，run 保持原狀，直到 client 報告 result。

Server 校驗身份、路由、事件格式、UUID、輸入集合及重複記錄一致性；不接受跨設備、跨 agent 或重新綁定輸入。結果的 input_ids 必須覆蓋該 run 已接受的輸入。執行輸出及終態以 client 爲準。

## 斷線、恢復與回覆

Client 以 events.jsonl 爲持久化 outbox，一次發送完整行，只有 server 提交後收到 event_ack 才推進源字節遊標。Server 以 device/client_session/event_seq 唯一鍵處理重放；同偏移不同內容拒絕。結果重放不會重複生成 assistant 消息或 Work 彙報。

斷線只改變設備在線狀態，不補造 failed/cancelled，不重新分配正在執行的輸入。首次領取保存準備好的 dispatch 快照，重連不重新組織上下文或簽發令牌。重連重送相同分配 ID 與未確認控制命令；client 使用輸入收據去重，絕不因重連重新 send。未被適配器接受的失敗以 prompt_error 報告，只失敗該輸入，不僞造 run。失敗通知同樣等待 server 提交確認；確認前保留輸入收據和所屬目錄。

原生 result 與 assistant 回覆在同一事務內保存。回覆通過 reply_to_message_id 指向 first_message_id，messages 不以 run_id 建關係。run.result_message_id 只是內部去重關聯。cancelled 不新增 assistant 回覆。

## 表結構

以下爲當前執行層實際 SQL。物理 session_id 指 server conversation，daemon_id 指 device；公共 API 的 /sessions、/daemons 及舊元數據命名尚未整體更名。client_session_id 纔是本地原生會話。message_id 目前仍爲 INTEGER；消息 UUID 和舊附件鏈路遷移屬於獨立的元數據設計，不能把本文當作已經完成它們。新執行表沒有數據庫外鍵。

```sql
-- A prompt is a server-side message delivery, not an execution.
CREATE TABLE IF NOT EXISTS run_prompts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL, message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL, harness TEXT NOT NULL,
 content TEXT NOT NULL, dispatch TEXT, status TEXT NOT NULL DEFAULT 'pending',
 run_id TEXT NOT NULL DEFAULT '', input_id TEXT NOT NULL DEFAULT '',
 client_session_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(message_id,agent_id)
);
CREATE INDEX IF NOT EXISTS prompts_dispatch ON run_prompts(daemon_id,status,id);
CREATE INDEX IF NOT EXISTS prompts_run ON run_prompts(run_id,input_id);
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, first_message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id INTEGER,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_conversation ON runs(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
 created_at INTEGER NOT NULL, UNIQUE(daemon_id,client_session_id,event_seq)
);
CREATE TABLE IF NOT EXISTS run_requests (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, run_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 payload TEXT NOT NULL, state TEXT NOT NULL, answer TEXT,
 answered_by TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_controls (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 run_id TEXT NOT NULL, daemon_id TEXT NOT NULL, kind TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
 answer TEXT, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS work_run_tokens (
 token_hash TEXT PRIMARY KEY, work_id TEXT NOT NULL, prompt_id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_notifications (message_id INTEGER PRIMARY KEY);
```

run_prompts 狀態爲 pending/sent/accepted/completed/failed/cancelled；runs 只有真實執行的 running/waiting/completed/failed/cancelled。run_events.payload 和 runs.result 保存公共事件原文。run_requests 保存最新公共請求快照；run_controls 是待傳輸控制命令，不是執行事件。

work_run_tokens 綁定輸入分配，而不是尚不存在的 run；有效性由輸入狀態檢查。run_notifications 保證同一線程消息只彙報一次。

## 升級

數據庫 schema 版本爲 3。舊執行表和舊 WebSocket 協議不兼容；現有數據庫會被拒絕啓動，需按部署流程備份後使用新數據庫，並同步更新 client。代碼不會在啓動時自動清空歷史或重放舊輸入。
