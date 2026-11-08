# Client runService：只承認適配器生成的 Run

runService 接收輸入、驅動 session 並提供實際執行的視圖。**run_id 唯一來源是 harnessctl 寫入的 accepted.run_id。** Client 不創建另一種執行 run，也不創建 attempt，不規定一次 send 一定得到新 run。

## 輸入與控制

1. 根據輸入來源確定 endpoint/conversation，選擇空閒 session 或向活動 session 繼續 send。
2. 通過 sessionService 登記佔用，準備文本及必要的上下文資源。
3. harnessService 用 UTF-8 stdin 調用 send，監聽該 session 的 events.jsonl。
4. 看到 accepted 後登記其 UUID，再向調用者返回該 run；accepted 前的啓動錯誤直接返回錯誤。
5. 後續輸入仍是 send，由適配器決定加入當前交互還是排隊產生新 UUID。
6. request 按 request_id reply；cancel 針對 session，不製造 run 級取消能力。
7. 當前驅動窗口內所有已受理輸入都有結果後，釋放 session 佔用；這只是進程內生命週期，不是另一個 run 身份。

一個驅動窗口可以觀察到多個真實 run；頁面和 API 分別展示每個 UUID，不能將它們聚合成一個 client run。一條 run 可以包含多個 accepted 輸入，例如 Codex 確認的插話。

## 持久化與查詢

```text
sessions/<session_id>/
  session.yaml
  context.json
  bin/context -> agentuse
  events.jsonl
  events.jsonl.d/
  runs/<accepted.run_id>.jsonl
```

run 引用僅定位實際的適配器 UUID：

```json
{"type":"reference","at":1793443200000,"run_id":"550e8400-e29b-41d4-a716-446655440000","endpoint_id":"local","conversation_id":"conversation-1","session_id":"session-1","agent_id":"local-codex","harness":"codex","harness_revision":"revision"}
```

正文、結果、完整輸入、請求、用量、費用和執行日誌不復制到引用中。runService 按同一個 UUID 從 events.jsonl 讀取五種公共事件，返回 completed/failed/cancelled 結果；未結束時爲 running，仍有 pending request 時爲 waiting。計量按一個真實 result 計數，不把輸入數當作執行次數，也不累計原生 session 的累計計量。

input_id 和 request_id 來自適配器，不能以客戶端臨時 ID 替換。無論 local 還是遠端，Run DTO 都沒有 attempt_id、server_confirmed、harness_run_ids 或第二套任務 ID。

## 恢復和目錄保護

accepted 事件是執行歸屬的事實。若適配器寫入 accepted 後 client 來不及創建引用就退出，啓動時掃描該 session 的公共事件即可重建引用；不需要預分配 ID、按時間猜測或恢復執行嘗試。

引用已存在時不重新寫執行結果。讀取只接受完整 JSONL 行，忽略末尾尚未寫完的一行；完整記錄損壞則明確報錯，不從日誌猜測結果。適配器私有 .d/ 不由 client 解釋。

活動、等待答案或清理未確認的 session 都保護整個 conversation 工作目錄。取消失敗不能假裝已經空閒。聲明 quota 後只回收完整的空閒受管目錄，不能單獨刪正在使用的 run 文件。

## 遠端傳輸邊界

local 和遠端調用完全相同的 sessionService、harnessService 與 runService。遠端直接接收 conversation message 和目標 Agent；不再有獨立輸入分配 ID，accepted 回傳來源 message_id/agent_id 與實際 run_id/input_id。

```text
<endpoint>/<conversation>/
  .server/
    messages/<message-agent-key-hash>.json  原消息、準備/提交收據、源起點與受理關係
    agents/<agent_version_id>.json        不可變配置緩存
    agents/skills/<skill_version_id>.json 技能版本描述緩存
    skills/<skill_version_id>.zip         已校驗的技能包緩存
  sessions/<session_id>/.server/
    stream.json                          Server 已逐條確認的源字節位置
    controls/<control-id-hash>.json       answer/cancel 的原內容、狀態和收據
```

Endpoint 根目錄的 .server/messages 另外保留很小的提交收據（消息摘要及受理標識），不隨 conversation quota 回收，防止延遲重複消息重新執行。它不包含正文、輸出或另一個業務 ID。

消息收據在讀取配置之前保存。有效的既有 session 直接複用資源快照；缺少資源時按固定版本 API 讀取，ZIP 必須校驗摘要和大小，緩存損壞時重新下載相同版本。Server 不需要知道下載或 init 的階段。

關鍵事件來自原始 JSONL，外層 id 穩定取自 session 和完整行位置，收到對應 ok 後才推進本地確認位置。普通 log 可略過，不保護目錄。ACK 丟失則補傳原事件，消息重送則補回既有 accepted，不再次 send；連接短暫斷開時本地工作繼續。

啟動時只恢復新版消息收據，不讀取或轉換舊版 inputs 和控制收據；升級前清空舊工作目錄。尚未跨過 send 邊界的準備可以重做；可能已提交的輸入只從源位置及唯一未關聯 accepted 恢復，證據缺失或衝突則保留 unknown，不重放。未知消息/控制、準備預留和未確認關鍵事件保護 conversation 目錄。

控制在調用原生命令前持久記錄不確定狀態，崩潰後不能再次執行相同 id。cancel 攜帶 run_id 作為過期控制檢查，實際作用域仍是 session 當前及排隊工作；request/result 才確認實際狀態。

## 實現歸屬

native_runs.go 管引用與查詢，views.go 定義公開視圖，reconcile.go 管啓動恢復，server_connection.go 管中心連接、輸入收據、事件遊標和控制傳輸。沒有 store、執行 attempt、結果副本或舊資料兼容層。sessionService 管目錄與會話元數據，fileutil 僅提供通用文件操作。

## Server Agent 版本銜接（已實現）

遠端 message 只固定 agent_version_id；Client 通過 Agent 版本及 Skill 版本 API 讀取模型、指令和依賴。配置、ZIP 校驗、安全解包與 init 都內含於消息處理。缺少配置不能改讀 latest，資源已在有效快照中則直接複用。

活動輸入必須匹配 agent_version_id，不能將新版消息放進舊版原生上下文。同版本補充輸入由適配器裁定 run 歸屬。公共 accepted/message/request/result/log Schema 不變，網路封裝與授權見 [通信協議](protocol.md)。

## 執行上下文工具（已實現）

runService 為 harness 提供裸 `context input` 和 `context attachment list/read/upload`，人類通過 `agentuse context` 選擇或進入同一執行上下文。描述文件只固定 session 和來源；當前 run 由公共 status 或顯式選擇確定，仍以 accepted 及來源消息收據關聯輸入。附件採用收到 message 時的固定快照，不重新列舉最新會話文件。圖片返回經校驗的本地路徑，由 AI 原生看圖工具讀取；輸出上傳自動關聯當前 run，發布及查詢以 Server 記錄為準。

Client 選定新建或續用的本地 session 後，才向 Server 讀取 `complete_messages`。Server 根據該 session 已受理的輸入決定補齊範圍；Client 不讀取整份 conversation 自行推算，也不向 harnessctl 增加補歷史命令。實際組裝的 context 保存在既有消息收據中，`context input` 顯示這份內容。

完整邊界、目錄與回收規則見 [執行上下文設計](client-context.md)；操作方法見 [使用手冊](../../../docs/client/context.md)。

送入模型的 `complete_messages` 使用可讀的對話文字，按順序標示作者、@收件人、引用的消息正文及附件名稱／ID／路徑；不把 API JSON、消息 UUID、序號、雜湊或執行資料直接拼入提示詞。API 維持結構化資料，Client 只負責排版。程式產生的標題、工具說明與提示詞統一使用英文，使用者和 Agent 的原始正文保留原文；既有 native session 的初始化快照不改寫。
