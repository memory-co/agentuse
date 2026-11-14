# runService：message/Agent 關係與實際執行

> 嵌套 conversation 已實作；本稿描述 runService 的職責邊界，消息回傳歸 conversationService，見 [父子 conversation 設計](server-conversation.md#父子對話)。

Server 使用 [agentuse-client/2](protocol.md)，直接投遞既有 conversation message。沒有 task、run_input、prompt ID 或預分配的 run UUID。只有 Client 上報的 accepted 才建立實際 run。

## 消息到執行的關係

保存一條消息時，在同一事務中寫入 messages 和每個目標 Agent 的 message_agents 關係。消息正文只保存一次；關係以 `(message_id, agent_id)` 為主鍵，固定 agent_version_id、device、harness，以及投遞和受理狀態，沒有自己的自增業務 ID。

```text
M1 → coder    → accepted(R1, A1)
M1 → reviewer → accepted(R2, A2)
M2 → coder    → accepted(R1 或 R3, A3)
```

最後一行由適配器確認一個實際結果。message 和 run 的 N:M 關係直接記錄在消息/Agent 關係上。Run 的第一條 accepted 消息記為 first_message_id，後續輸入可以綁定相同 run。

## 派發與固定版本

Server 根據 heartbeat 的空閒容量和活躍綁定主動選取 pending 消息，不等待 claim。每個綁定同時只有一條已派發但未確認受理的消息；unknown 也阻止後續消息，避免不確定輸入被跳過。

首次派發在事務內保存 message 報文快照並標記 sent，之後重送沿用相同內容和版本。正文是原消息，context 只包含固定的業務分工和本次附件說明。共享歷史在 Client 選定實際 session 後，通過消息 context HTTP 接口按 accepted 邊界增量補齊，不存入派發快照。報文不包含模型、Agent 指令或技能包清單。Client 通過固定版本 API 按需獲取配置和資源，Server 不維護 init 階段。

資源讀取權限依據已派發的消息/Agent 關係，在 accepted 和 run 建立之前即可使用。新 Agent 或 Skill 版本不改已有消息的引用。

| 關係狀態 | 含義 |
|---|---|
| pending | 尚未派發 |
| sent | 已固定投遞快照，等待 accepted |
| unknown | 可能受理，阻塞該綁定後續輸入；重連可重發相同報文補查收據，Client 不得重做不確定的 send |
| rejected | 已證明尚未受理，不建立 run |
| accepted | 已關聯適配器的 run_id/input_id |
| completed / failed / cancelled | 實際 result 的查詢投影 |

## 公共事件與控制

event 信封統一承載 accepted、message、request、result、log；原始公共 JSON 保存在 run_events。穩定的外層 id 由 Client session 和完整 JSONL 行位置構成，線上不另傳 seq/ack_seq。

Server 按認證設備、Client session 和源位置去重，並核對原始事件、消息關聯和固定路由。同一來源不能改綁 conversation、Agent、版本或消息。accepted 的 message_id/agent_id 必須與已派發關係相符；後續事件可以從 session/run 補全路由。

保存關鍵事件、更新 run 和消息關係、寫入最終 conversation 回覆在同一事務完成，提交後才 ACK。result.input_ids 必須準確覆蓋該 run 的已受理輸入；每個 run 只結算一次。回覆指向 first_message_id，result_message_id 防止重複新增回覆；頂層 cancelled 可以沒有最終回覆；子對話仍保存取消說明。子對話在所有輸入與執行收束後，由 conversationService 提交輪次結束並自動通知父側。普通 log 只作診斷，不要求 ACK。

answer 和 cancel 直接指定既有 run/session。控制 id 只用於傳輸去重；控制 ACK 不表示 request 已 replied 或 run 已 cancelled，實際狀態仍來自公共事件。已終態的 run 不接受新的執行事實覆蓋。

## 持久化歸屬

| 表 | 作用 |
|---|---|
| message_agents | 消息的目標 Agent、固定版本、派發快照及 run/input 關係，複合主鍵，沒有另一個輸入 ID |
| runs | 適配器確認的實際 run 和狀態、結果投影 |
| run_events | 公共事件及源位置，作為去重與恢復事實 |
| run_requests | request 事件的問題查詢投影，不是新的執行對象 |
| run_controls | answer/cancel 的傳輸收據，防止不確定控制被自動重做 |

完整 SQL 見下方「表結構」；本節是五張執行表的唯一設計定義。只支持 schema 9，messages.id 及消息引用直接使用 UUID，messages.seq 保存 conversation 內順序。舊資料庫啟動時明確拒絕，必須使用新資料庫；不提供舊資料保留、遷移或 ID 轉換。

Client 只讀取新版 message/Agent 收據，不轉換舊 inputs 或控制收據。升級時清空本地工作目錄並重新註冊設備。網路協議不向舊版 hello/claim/prompt 降級，Server 與 Client 必須一起更新。

[協議](protocol.md) · [conversationService](server-conversation.md) · [Agent 版本](server-agent.md) · [Client runService](client-run.md)

## 邊界與原生會話

Server 僅記錄 Client 產生的 session_id，同一值用於消息/Agent 受理關聯、run、事件、請求和控制；不存在另一個 client_session_id。conversation_id 永遠指共享對話。公共事件原樣保存，消息正文不再包裝執行結果；終態 run 拒絕新的 request/message 等執行事實，重放相同來源事件則確認已有記錄。

run 投影向 executionEventConsumer 發送事實，conversation 編排器在原事務保存作者、回覆與狀態；回覆失敗則事件和結果一起回滾。父子關係不改變這個提交邊界。runService 不查詢父子對話、不向主對話生成 Work 摘要，也不維護 Work 的授權或通知表。

## 表結構

以下 SQL 對應 [run_schema.go](../../server/internal/store/run_schema.go)。時間為 Unix 秒，公共事件及 answer 等 JSON 使用 TEXT。message_agents 的主鍵是消息/Agent 複合鍵，run 的身份來自 Client accepted；各表關聯由所屬 service 校驗，沒有數據庫外鍵。

```sql
-- A message target has no independent task/input identity.
CREATE TABLE message_agents (
    conversation_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    dispatch TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    run_id TEXT NOT NULL DEFAULT '',
    input_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(message_id,agent_id)
);
CREATE INDEX message_agents_dispatch ON message_agents(device_id,status,created_at,message_id);
CREATE UNIQUE INDEX message_agents_input ON message_agents(device_id,session_id,input_id) WHERE input_id<>'';
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    first_message_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    result_message_id TEXT,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    result TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX runs_conversation ON runs(conversation_id,created_at,id);
CREATE TABLE run_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    event_seq INTEGER NOT NULL,
    message_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(device_id,session_id,event_seq)
);
CREATE TABLE run_requests (
    request_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    state TEXT NOT NULL,
    answer TEXT,
    answered_by TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE run_controls (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    answer TEXT,
    state TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

event_seq 保存 Client session 內完整 JSONL 行的源位置，唯一鍵為 (device_id,session_id,event_seq)。它不是 Server 另外生成的一套執行序號。run_requests 是公共 request 的查詢投影，run_controls 保存 answer/cancel 的傳輸狀態，控制送達不代表執行已進入終態。
