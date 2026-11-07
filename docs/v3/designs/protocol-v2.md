# Client ↔ Server：六種 WebSocket 訊息

> 設計草案，尚未實作，取代本檔上一稿的 HTTP pull/report 方案。現行實作見 [protocol.md](protocol.md)。Client 與本地 Agent 仍使用 harnessctl/3，不修改適配器介面。

## 一、協議只有這六件事

Client 主動建立一條 WebSocket；Server 下發輸入，Client 回傳執行事件。

| type | 方向 | 用途 | 需要 ACK |
|---|---|---|---|
| heartbeat | Client → Server | 在線狀態、執行器與可用容量 | 是 |
| task | Server → Client | 發送輸入或取消目前交互 | 是 |
| log | Client → Server | 輸入受理、正文、提問與執行過程 | 關鍵事件需要，普通日誌不需要 |
| result | Client → Server | 最終結果、錯誤及用量 | 是 |
| answer | Server → Client | 回答 Agent 的問題 | 是 |
| ack | 雙向 | 確認指定訊息已處理 | 否 |

不再增加 hello/welcome、領取請求、無任務通知、批次回報或各種專用 ACK。沒有工作時只維持 heartbeat。

所有需確認的訊息帶 `id`，ACK 使用同一個 id。這是傳輸識別，不是 run_id。task/answer 的 id 直接使用 `i:<run_inputs.id>`；事件 id 由 Client 根據本地事件來源穩定產生，重送不換 id。

## 二、heartbeat：告訴 Server 現在能接什麼

```json
{"type":"heartbeat","id":"h:1","free_sessions":1,"active":[{"conversation_id":"c1","agent_version_id":"v1"}],"harnesses":[{"name":"codex","version":"installed-version","revision":"package-revision"}]}
```

連上後立即發送，之後約每 10 秒發送；容量變化時也立即更新。每次帶完整快照，不做增量同步。

- free_sessions：還能建立多少個執行中的本地 session，是內部容量資訊，不增加使用者設定。
- active：目前仍可接收輸入的 conversation/Agent 版本綁定。容量為零時，仍可向這些綁定發送補充輸入。
- harnesses：啟動時探測並凍結的可用執行器清單。

Server 依容量主動發送 task，不再等待 Client 領取；answer/cancel 不受新 session 容量限制。Client 仍是本機及多中心總容量的最終管理者，不能因 Server 下發 task 就超額啟動。

## 三、task：首次與中途補充都一樣

```json
{"type":"task","id":"i:17","action":"send","conversation_id":"c1","agent_id":"reviewer","agent_version_id":"v1","content":"Server 已準備好的完整輸入","config":{"harness":"codex","model":"固定模型","agent_instructions":"固定指令","skills":[]}}
```

content 已包含任務、共享歷史和 Server 業務說明。config 是固定 Agent 版本的模型、指令和 skill 下載描述；同一輸入重送時內容不變。範例中的 c1、v1、s1 是既有 ID 的縮寫。

Client 選擇對應本地 session；新綁定先準備資源並執行 `harnessctl init`，之後將 content 經 stdin 交給 `harnessctl send`。版本變更使用新的 session/events 地址，共用原 conversation 的 workspace。

Server 不預先給 run_id，也不指定 start/steer/queue。中途補充仍是一條 send；適配器的 accepted 才決定它屬於原 run 還是新的 run。

取消沿用 task，明確指定已存在的 run：

```json
{"type":"task","id":"i:19","action":"cancel","session_id":"s1","run_id":"550e8400-e29b-41d4-a716-446655440000"}
```

Client 核對目標仍在執行後呼叫 `harnessctl cancel`。目標已結束則確認無需操作，不能取消同一 session 後來啟動的交互。

## 四、log：沿用 harnessctl 的事件

```json
{"type":"log","id":"e:s1:192","task_id":"i:17","session_id":"s1","event":{"type":"accepted","ts":"2026-11-07T00:00:00Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"in-1","text":"Server 已準備好的完整輸入"}}
```

外層 log 是回傳通道名稱，**不表示裡面的東西都能丟**。event 原樣沿用 [harnessctl 公共事件](../../../harnessctl/EVENT.md)：

| event.type | 意義 | 可靠性 |
|---|---|---|
| accepted | 輸入已受理，以及真正的 run_id/input_id | 必須保存並 ACK |
| message | Agent 的完整正文快照 | 必須保存並 ACK |
| request | 問題、權限請求及 pending/replied/expired 狀態 | 必須保存並 ACK |
| log | 工具過程、流式片段與診斷 | 可丟、可合併，不要求 ACK |

accepted 必須帶外層 task_id，以關聯原始輸入。尚未建立來源綁定的初始化日誌也帶 task_id；其餘事件可依 session/run 關聯，不必重複傳 conversation、Agent 和版本。

Server 只依 accepted 建立 run。問題透過 request 交給前端；讀取正文按 message_id 更新完整快照，不能把日誌片段當成唯一正文。

## 五、result：只回報實際執行結果

```json
{"type":"result","id":"e:s1:640","session_id":"s1","event":{"type":"result","ts":"2026-11-07T00:00:10Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["in-1"],"status":"completed","output":"已完成","usage":null,"cost_usd":null,"error":null}}
```

event 就是適配器的 result，包含 completed/failed/cancelled、完整輸出、錯誤、用量和費用，不再翻譯另一套結果格式。

Server 保存結果、更新 run 和 conversation 回覆，在同一交易提交後 ACK。run 可以涵蓋多條輸入，依 input_ids 核對，不按 task 數量重複建立結果。

尚未受理就準備失敗時，用該 task 的錯誤 ACK 回報；不虛構 run 或 result。

## 六、answer：回答指定問題

```json
{"type":"answer","id":"i:18","session_id":"s1","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","answer":{"q1":"Go"}}
```

Client 核對公共 request 的歸屬，將答案經 stdin 交給 `harnessctl reply`。權限請求使用 allow/deny，問題答案沿用公共格式。

answer 的 ACK 只確認本地操作；問題是否真的 replied，仍看適配器之後的 request 事件。send 等待受理時，answer/cancel 和事件回傳仍須能繼續，不能共用一個阻塞佇列。

## 七、ack：只有一種確認格式

```json
{"type":"ack","id":"i:17","status":"ok"}
```

| status | 含義 |
|---|---|
| ok | heartbeat 已更新；事件已保存；或 task/answer 的本地操作已受理、無需再操作 |
| error | 明確拒絕或能證明尚未受理，附 error 說明 |
| unknown | task/answer 可能已受理，但暫時無法確認，附 error 說明；停止自動重送執行 |

ACK 的含義由原訊息決定，但格式相同。task 的 ok 不表示模型完成，answer 的 ok 不表示問題已 replied，cancel 的 ok 不表示 run 已 cancelled。執行狀態只從公共事件取得。

Client 在本地操作受理後確認 task/answer；Server 在交易提交後確認關鍵 log/result。accepted 可能先於 task ACK 抵達，兩者不能互相等待。關鍵事件只有收到 ok 才能解除待同步保護；錯誤 ACK 不表示資料已保存。ACK 自身不需要 ACK，也不另帶一個回執 ID。

## 八、連接與實作規則

WebSocket 地址為 `/api/client/ws`，升級請求使用 device key 與 `AgentUse-Protocol: agentuse-client/2`。認證及版本在升級階段檢查，成功後直接發 heartbeat；不另做業務握手。不相容時明確要求升級，本地 harnessctl 仍為 /3。

Server 只傳業務正文和固定資源描述，不傳 shell、argv、原生 RPC 或 YAML。ZIP、附件仍走既有 HTTP 檔案 API，下載只向同一 endpoint 提交憑據。

恢復規則留在實作層，主協議不公開批次、來源遊標或同步階段：

- 需確認的訊息先保留在既有資料來源中，未確認就沿用原 id 重送；接收方核對重複內容，不能再次執行或追加結果。
- 同一 session 同時最多一個 send 在等待受理，不用等 result 才能繼續下一個 send。逾時不能證明未受理；證據不足回報 unknown，保留現場並暫停後續 send，不盲目重放。
- 事件 id 可直接由 session 與 JSONL 完整行位置生成，例如 e:s1:192；這是封裝器細節，不增加線上的 pos/after/next 欄位。較晚事件先獲 ACK，也不能丟掉前面尚未確認的關鍵事件。
- 心跳遺失、重連或設備離線，都不代表原生工作失敗。已受理工作可繼續，連回後補傳關鍵事件；普通日誌不阻止目錄回收。

runService 仍只管理 run_inputs、runs、run_events：send/cancel 對應 task，reply 對應 answer，ACK 更新傳輸狀態，log/result 保存公共事件。資料表及事務細節見 [server-run.md](server-run.md)，不再展開成其他訊息類型。

Local 與遠端共用相同的 harnessctl 執行路徑；Local 只省去 Server 通信。此設計借鑑 [Multica 的訊息信封](https://github.com/multica-ai/multica/blob/282f48eed2531f467ed4e6febc2a59b590d3e711/server/pkg/protocol/messages.go)，具體六種訊息與 ACK 規則是 AgentUse 的選擇。

[現行協議](protocol.md) · [harnessctl 介面](../../../harnessctl/README.md) · [公共事件](../../../harnessctl/EVENT.md) · [runService](server-run.md)
