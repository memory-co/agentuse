# runService：實際執行與已受理輸入

> 本稿對應 遷移基線 `0001_initial`。嵌套 conversation 的消息回傳歸 conversationService，見 [父子 conversation 設計](server-conversation.md#父子對話)。

> [执行期授权](client-server-security.md)已实现：init 使用投递时的 HMAC 准备票据，accepted 后在 runs 保存 access_token_hash 与 access_expires_at，不新增独立 grant 表。来源已受理后不能退回准备期权限；终态、超时和设备重置均阻断访问。

Server 使用 [agentuse-client/3](protocol.md)，直接投遞既有 conversation message。只有 Client 上報的 accepted 才建立 runs 和 run_inputs；不預分配 run、input 或 task 身份。

## 消息到執行的關係

保存消息時只寫 messages：正文、收件人及其固定路由元數據。此時 runs、run_inputs 均不增加記錄。Client 受理後回傳 message_id、agent_id、run_id 和 input_id；Server 在同一事務內核對或建立 run，再插入已受理輸入。

```text
M1 → coder    → accepted(R1, I1) → run_inputs(R1, I1, M1)
M1 → reviewer → accepted(R2, I2) → run_inputs(R2, I2, M1)
M2 → coder    → accepted(R1, I3) → run_inputs(R1, I3, M2)
```

一條消息可被多個 Agent 受理；同一 run 可受理多條消息。首次輸入還記入 runs.first_message_id；完整輸入關係以 run_inputs 為準。Agent、conversation、device、session 和版本通過 runs 取得，不在 run_inputs 複製。run_inputs 沒有 status、error 或獨立的 Server ID，終態只保存於 runs。

## 派發與固定版本

conversationService 在消息的 agent_targets JSON 中保存每個目標的固定版本、device 和 harness。首次派發時保存不可變的 dispatch 報文快照；否定回執保存為 error。這些字段不保存 run/input ID 或執行狀態，不建立獨立的投遞表。

待確認目標由消息目標減去 run_inputs 的已受理關聯推導；明確拒絕的目標不再派發。Server 根據 heartbeat 的空閒容量和活躍綁定主動發送；同一綁定每次只允許一條已發出且尚未確認的消息，結果不確定時阻止後續輸入。

重連重發保存的相同 dispatch 和消息身份，Client 以持久收據去重，補報原 accepted。Server 沒有 run 記錄只代表尚未確認，不證明 Client 沒有執行。初始化等確定發生於受理前的失敗，保存於原消息的 agent_errors；不虛構 failed run。不確定錯誤帶 uncertain=true，必須繼續等待證據，不能直接結束輪次或重新執行。accepted 清除該目標此前的不確定錯誤。

報文的 context 只包含固定業務分工和本次附件說明。共享歷史在 Client 選定實際 session 後，按 run_inputs 的 accepted 邊界增量補齊，不存入 dispatch。配置、skills 和附件的讀取權限來自已派發消息的固定路由，在 run 建立之前即可使用；Server 不維護 init 階段。

## 公共事件與控制

event 信封統一承載 accepted、message、request、result、log；原始公共 JSON 保存在 run_events。穩定的外層 id 由 Client session 和完整 JSONL 行位置構成。

Server 核對認證設備、來源消息、Agent 固定版本及 session 歸屬。同一消息/Agent 只能受理一次，不得改綁其他 run/input；同一 input 不能綁定其他消息。重複的相同來源事件冪等；新的補充輸入不能加入已終態 run。

accepted、run、run_inputs 和 conversation 狀態在同一事務提交，提交後才 ACK。run_inputs 在 accepted 時寫入，不等 result。result.input_ids 必須恰好覆蓋該 run 的所有受理輸入；結果與最終回覆仍原子提交，每個 run 只結算一次。終態不修改 run_inputs，也不複製回消息元數據。

Run.model 是本次原生主執行實際確認的模型，直接從已持久保存的 runs.result.model 提取。用戶不聲明 model 時，同樣記錄 harness 回報的實際默認模型；聲明別名時則記錄原生解析或重路由後的模型。運行中尚未取得 result、或原生未回報時為 null，不從 Agent 配置或 session 初始化參數補填。成功、失敗和取消都保留 result 已確認的值；不匯總子 Agent 模型。不為 model 額外增加 SQL 列。

Run.usage 同樣從 runs.result.usage 提取，保留單次執行的 input_tokens、output_tokens、cache_read_tokens、cache_write_tokens。整體未回報時為 null，部分計量未回報時該字段為 null；明確的 0 必須保留，不能用零補齊未知值。計量為非負整數，非法 result 在提交前拒絕，不留下無法讀取的 run。失敗及取消也展示已回報的用量。

input_tokens 已包含緩存部分，cache_read_tokens 和 cache_write_tokens 是輸入的細分，不再額外累加。Server 不按 run_inputs 數量乘算、不加入其他 run 或子對話、不解析私有日志推測計量。完整 result JSON 保持不變，不新增計量 SQL 列或獨立統計表。

回覆指向 first_message_id，result_message_id 防止重複回覆。頂層 cancelled 可以沒有最終回覆；子對話保存取消說明。子對話在所有目標有受理結果或明確拒絕、所有實際執行結束後，關閉本地輪次並自動通知父側。普通 log 只作診斷，不要求 ACK。

answer/cancel 指定既有 run/session，控制 ACK 不等於 request 已 replied 或 run 已 cancelled；實際狀態仍由公共事件確認。run_controls 只保存控制的傳輸收據。

## 讀取與展示

ConversationDetail 返回 messages、runs、run_inputs、rounds、questions 和 events；新消息的響應中 runs、run_inputs 都是空數組。消息受理前的否定回執以 Message.agent_errors 返回，不再提供 deliveries。

GET run 返回 run、inputs 和 events。前端對每個真實 run 只保留一個明確可點擊的入口；受理輸入列表、原消息、input_id 和診斷事件集中在 Run 詳情，不在每條消息下面重複顯示 completed 標籤。子會話通知維持默認折疊。

Run 入口直接展示 harness、實際 model、輸入、輸出、緩存讀取及緩存寫入 token，窄屏自動換行。未回報計量顯示「—」，詳情的四項計量顯示「未回報」，零值顯示 0；詳情同時說明輸入已含緩存，不將緩存重複計入。

原始事件默認展示緊湊時間線，每條保留原類型、topic、status/state/level 和時間，按公共事件類型配置圖標。標題依次取 payload 的 `$.data.title`、`$.title`、`$.message`、`$.text`、`$.output`、`$.data.reason`、`$.reason`、`$.topic`、`$.type` 中第一個非空字符串，最後回退到 event.kind；長文本僅截短展示。這只是已有字段的優先級，不把日志映射為「運行命令」等動作，不解析原生私有幀推測意義，也不合併或重排事件。點擊單條後才渲染完整原始 JSON，原始內容保持不變。

SDK 等待一條消息時逐一檢查它的 Agent 目標：明確拒絕視為收束；否則必須找到 run_inputs 關聯且對應 run 終態。空 runs/run_inputs 不代表 Agent 消息已完成。等待整個 conversation 仍使用對話狀態。

## 持久化歸屬

| 表 | 所屬與用途 |
|---|---|
| messages | conversationService：正文、收件人及固定路由、派發快照、否定回執 |
| runs | runService：Client 確認的實際執行、狀態和結果 |
| run_inputs | runService：accepted 確認的原生輸入與消息關聯 |
| run_events | runService：公共事件及來源位置，用於去重及恢復 |
| run_requests | runService：request 事件的問題查詢投影 |
| run_controls | runService：answer/cancel 的傳輸收據 |

runService 讀取消息路由，由 conversationService 在共享事務內更新消息元數據。run 事件通過 ExecutionEventConsumer 交給 conversationService 保存回覆與輪次；任何一步失敗都整體回滾。

Server 以 GORM/gormigrate 管理遷移基線 `0001_initial` 及後續版本；此前未版本化資料庫明確拒絕，不提供舊資料轉換或兼容字段。兩端與 SDK 一起更新，使用新 Server 資料庫重新註冊設備。Client session_id 是唯一的本地會話標識，Server 不生成另一個 session ID。

[協議](protocol.md) · [conversationService](server-conversation.md) · [Agent 版本](server-agent.md) · [Client runService](client-run.md)

## 表結構

以下是 SQLite 表達的邏輯結構；各庫實際字段、索引與約束由 GORM/gormigrate 管理，對應 [models.go](../../server/internal/database/models.go)，是五張執行表的唯一設計定義。時間為 Unix 秒，各表關聯由 service 在事務內校驗，不聲明數據庫外鍵。

```sql
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE runs (
    access_token_hash TEXT,
    access_expires_at INTEGER,
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, first_message_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, device_id TEXT NOT NULL, session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id TEXT,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX runs_conversation ON runs(conversation_id,created_at,id);
-- Accepted inputs only: never preallocated while a message is waiting for Client.
CREATE TABLE run_inputs (
 run_id TEXT NOT NULL, input_id TEXT NOT NULL, message_id TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 PRIMARY KEY(run_id,input_id), UNIQUE(run_id,message_id)
);
CREATE INDEX run_inputs_message ON run_inputs(message_id);
CREATE TABLE run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL,
 session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, device_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, message_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, payload TEXT NOT NULL,
 created_at INTEGER NOT NULL, UNIQUE(device_id,session_id,event_seq)
);
CREATE TABLE run_requests (
 request_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, run_id TEXT NOT NULL,
 session_id TEXT NOT NULL, device_id TEXT NOT NULL,
 payload TEXT NOT NULL, state TEXT NOT NULL, answer TEXT,
 answered_by TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE run_controls (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 conversation_id TEXT NOT NULL, session_id TEXT NOT NULL,
 run_id TEXT NOT NULL, device_id TEXT NOT NULL, kind TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
 answer TEXT, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
```

run_controls.seq 是資料庫生成的內部排序號，用於同秒控制記錄的穩定先後順序，取代 SQLite 隱式 rowid；公開控制仍使用 id。

event_seq 保存 Client session 內完整 JSONL 行的來源位置，不是 Server 另外分配的執行序號。

授权字段由 `0003_device_security` 增加，旧 run 保持 NULL；哈希有唯一索引。补充输入加入同一 run 时保留首次令牌及期限，accepted ACK 通过安全 WS 返回原 run 的令牌；Client 不将令牌写入公开事件或本地收据。
