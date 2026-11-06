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

local 和遠端調用完全相同的 sessionService、harnessService 與 runService；都直接展示適配器 UUID。Server 不再有另一套執行 run，輸入分配 ID 僅用於關聯 server 消息。

每個遠端 session 額外保存傳輸收據：

```text
.server/
  inputs/<prompt_id>.json  輸入提交狀態、源起點及 accepted 的 run_id/input_id
  stream.json             server 已提交確認的 events.jsonl 字節偏移
  controls/<control_id>.json  reply/cancel 送達收據
```

這些文件不復制執行輸出或結果。源 JSONL 是唯一事件 outbox：發送完整行，收到 ACK 後原子推進遊標。ACK 丟失則重傳同一源位置，server 去重；重連收到同一輸入 ID 時讀取收據，不再次 send。活動 session 在連接短暫斷開時繼續運行。

啓動先確認遺留前臺工作停止，再恢復輸入收據：從提交前保存的源位置讀取 accepted，補回實際 UUID；存在歧義則報錯，不重放輸入。只讀完整 JSONL 行。尚未確認的失敗通知、未確定受理狀態和未確認上報的事件保護 conversation 目錄，不因 quota 丟掉待交付事實。

控制命令以 control_id 去重；cancel 攜帶目標 run_id，目標已經終態時不能取消後續交互。request 的狀態和最終結果仍以適配器事件爲準，傳輸 ACK 不代表業務狀態已改變。

## 實現歸屬

native_runs.go 管引用與查詢，views.go 定義公開視圖，reconcile.go 管啓動恢復，server_connection.go 管中心連接、輸入收據、事件遊標和控制傳輸。沒有 store、執行 attempt、結果副本或舊協議兼容層。sessionService 管目錄與會話元數據，fileutil 僅提供通用文件操作。

## Server Agent 版本銜接（已實現）

遠端輸入攜帶 agent_version_id、model、agent_instructions 和固定 skill 包版本清單。runService 按已分配輸入下載資源、校驗 SHA-256/大小並安全解包，調用 sessionService 選擇同版本會話，再由 harnessService 調用 init/send。下載只使用當前 endpoint 的相對 API 路徑，不把設備密鑰發到其他域。

活動輸入匹配加入 agent_version_id，不能把新版消息塞入舊版驅動窗口；同版本補充輸入仍由適配器裁定 run 歸屬。ClientEvent 信封和本地 run 引用記錄該版本關聯，公共 accepted/message/request/result/log 的 Schema 不變。詳情見 [client-session.md](client-session.md) 與 [版本化通信字段](protocol.md#agent-versioning)。
