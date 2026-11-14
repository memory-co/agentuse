# Client ↔ Server：直接投遞 conversation message

> Client/Server 使用 agentuse-client/2，直接投遞 conversation message；本地適配器繼續使用 harnessctl/3。舊版 hello/claim/prompt 協議不再兼容。

## 一、業務只有 message、Agent 和實際 run

Server 保存 conversation message，決定哪些 Agent 要處理它，直接把 message 發給對應 Client。Client 負責找到或準備本地 session，再把消息交給 Agent；適配器受理後返回真正的 run_id，Server 才記錄執行關係。

```text
conversation message + 目標 Agent
    → Client 準備或複用本地 session
    → harnessctl send
    → accepted 回傳 run_id
    → Server 關聯 message、Agent 和 run
```

不再建立 task，也不要求先把 message 包裝成另一個 run_input 才能投遞。`(message_id, agent_id)` 就是一次消息投遞的業務身份；一條消息發給兩個 Agent，是同一條 message 的兩個目標。

- message_id：Server 已保存的 conversation message，正式值為 UUID。
- agent_id / agent_version_id：目標 Agent 和此次消息固定使用的不可變版本。
- session_id：Client 本地會話地址的標識，由 Client 管理，可承載多個 run。
- run_id / input_id：適配器在 accepted 中確認的實際交互和輸入標識，Server 不預先分配。

同一 Agent 的後續 message 仍直接投遞；它屬於當前 run 還是下一個 run，由適配器決定。一個 run 可以受理多條 message，一條 message 也可以因多個目標 Agent 關聯多個 run。

## 二、通信分工

WebSocket 傳消息、公共事件和控制；HTTP 讀取固定版本配置及下載資源。Client 主動連接 Server，但消息的投遞由 Server 決定，資源的按需獲取由 Client 決定。

| type | 方向 | 用途 | 確認方式 |
|---|---|---|---|
| heartbeat | Client → Server | 在線、執行器、可用容量及活躍綁定 | Server 回 ack |
| message | Server → Client | 指定 Agent 處理既有 conversation message | Client 回 accepted 事件；未受理的失敗或不確定情況回 ack |
| event | Client → Server | 原樣上報 harnessctl 公共事件 | 關鍵事件由 Server 回 ack；普通 log 不要求確認 |
| answer | Server → Client | 回答已存在 run 的指定問題 | Client 回 ack；實際狀態看 request 事件 |
| cancel | Server → Client | 取消已存在的指定 run | Client 回 ack；實際終態看 result 事件 |
| ack | 雙向 | 確認對應消息已處理，或報告拒絕、不確定 | 不再確認 ack |

`event` 統一承載 accepted、message、request、result、log，內層沿用 [harnessctl 公共事件](../../harnessctl/EVENT.md)。外層 `type=message` 是 Server 投遞消息，內層 `event.type=message` 是 Agent 正文快照，按方向和層次區分。

報文 id 只用於傳輸去重，不新增業務對象：

- message 的 id 穩定取自 message_id 和 agent_id，例如 `m:m1:reviewer`；重送不換 id。
- event 的 id 由 Client 根據 session 和本地完整事件行位置穩定生成，例如 `e:s1:192`。
- answer、cancel 使用各自穩定的控制 id，例如 `a:<uuid>`、`c:<uuid>`；重送沿用原 id。
- heartbeat 每份新快照使用新的 id，重連時也不能與舊快照衝突；ack 沿用被確認報文的 id。

下例的 c1、m1、v1、s1 為 ID 縮寫，正式傳輸使用完整標識。

## 三、heartbeat：宣告現在能接什麼

```json
{"type":"heartbeat","id":"h:1","free_sessions":1,"active":[{"conversation_id":"c1","agent_id":"reviewer","agent_version_id":"v1"}],"harnesses":[{"name":"codex","version":"installed-version","revision":"package-revision"}]}
```

連上後立即發送，之後約每 10 秒發送；容量變化時也立即更新。每次是完整快照：

- free_sessions：還能佔用多少個 session 執行名額；資源準備和 init 期間也預留名額，避免並發超額啟動。
- active：目前仍可接收補充消息的 conversation/Agent/版本綁定。容量為零時，Server 仍可向這些綁定投遞消息。
- harnesses：Client 啟動時探測並凍結的可用執行器及包 revision，不表示某個 Agent 已完成 init。

Server 按容量派發，並計入上份快照之後自己已發出、尚未取得受理結果的新綁定預留。Client 是本機及多中心總容量的最終管理者；容量不足且尚未提交輸入時，可以明確拒絕。answer/cancel 不受新 session 容量限制。

## 四、message：哪個 Agent 處理哪條消息

```json
{"type":"message","id":"m:m1:reviewer","conversation_id":"c1","message_id":"m1","agent_id":"reviewer","agent_version_id":"v1","content":"請評審這次修改","attachments":[]}
```

Server 在保存消息及目標 Agent 關係時，固定 agent_version_id 和目標 device；投遞的內容不隨重連、Agent 更新而變化。

- content 是 conversation message 的正文，attachments 沿用該消息的檔案引用。
- `context` 只承載來源 conversation 的必要業務說明及本次文件說明，不夾帶整份共享歷史。Server 固定派發快照，Client 不自行推斷業務角色。
- Client 選定實際本地 session 後，以設備憑據讀取 `GET /api/conversations/{id}/messages/{message_id}/agents/{agent_id}/context?session_id=...`，取得 `complete_messages`。Server 只返回該 session 最後受理輸入之後、本次之前尚需補齊的共享消息，排除該 session 自己的回覆；新 session 補齊先前歷史。每條只保留作者、正文、收件人和附件。
- Client 將 `complete_messages` 作為背景補充，與業務說明、本次正文組成一次 send；空列表不生成補充區塊。本次消息不出現在補充列表，舊任務不重複執行。此讀取沿用已投遞的 message/Agent 權限，不增加 WebSocket 類型、資料表或 harnessctl 命令。
- 報文不攜帶 model、instructions、skills 或下載清單；這些由 Client 按固定 agent_version_id 讀取。
- 同一 `(message_id, agent_id)` 不得重複提交已受理或受理結果不確定的 send；相同 id 的重送內容、版本和目標必須一致，衝突直接拒絕。

首次消息和中途補充使用同一格式，Server 不指定 start/steer/queue，也不預填 run_id。不要求額外的領取請求、無任務通知或消息成功 ACK；accepted 本身就是這條消息的受理回執。

Server 需要持久保存每個 message/Agent 目標的固定版本、設備、投遞內容及受理關係，以便恢復。這是既有消息的目標關係與投遞元數據，不另分配 task_id 或 run_input_id。

## 五、Client 內部完成配置獲取、skills 和 init

收到 message 後，Client 按 endpoint/conversation/agent/agent_version/harness revision 選擇本地 session：有效的相同綁定直接複用；缺少綁定時才準備資源並 init。Agent 版本變更使用新的 session/events 地址，仍可共用 conversation 的 workspace。

### 按固定版本獲取資源

Client 缺少配置時，讀取已有 Agent 版本 API；按其 skills 引用讀取固定 Skill 版本，再下載本地缺少的包：

| HTTP | 取得什麼 |
|---|---|
| GET /api/agents/{agent_id}/versions/{agent_version_id} | harness、model、instructions 和有序 skill_id/skill_version_id 引用 |
| GET /api/skills/{skill_id}/versions/{skill_version_id} | name、size_bytes、sha256、download_path 等不可變包描述 |
| GET /api/skills/{skill_id}/versions/{skill_version_id}/package | 固定版本 ZIP |

配置與包可以按不可變版本緩存；已有可驗證資源或有效的 session 快照時，不必每條消息重新下載。Client 始終讀取 message 指定的版本，不能改讀 Agent 的 current/latest。相同名稱或本地存在某個 skill，不足以證明它就是所需版本。

配置和 Skill 版本描述的 API 支持按消息分配範圍讀取：device key 只能讀取已投遞給本設備的 message 所固定的 Agent 版本及其 Skill 依賴。Server 必須在發送 message 前提交這項關係，不能等 accepted/run 建立後才允許下載，否則首次 init 無法完成。設備憑據不能枚舉 Agent/Skill 清單，也不能冒用用戶身份。

權限沿用對應消息/執行的有效範圍，重連不丟失；軟刪除不破壞已有消息的固定版本引用。下載地址只使用同一 endpoint 的 API 路徑，不向其他域傳送 device key。附件仍走既有 conversation 檔案 API。

### 本地執行順序

```text
持久記錄正在處理的 message/Agent，預留容量
    → 讀取缺少的固定版本配置與資源
    → 準備本機 context 描述文件與命令入口；附件只傳元資料，不提前下載
    → 校驗 ZIP 的版本、大小、SHA-256，安全解包到 workspace 外
    → harnessctl init，固定模型、指令及技能快照
    → init 成功後清理臨時資源
    → 按實際本地 session 讀取 complete_messages，組裝必要補充與本次正文
    → 將消息經 stdin 交給 harnessctl send
    → 從 accepted 取得 run_id/input_id，回傳 Server
```

`harnessctl init` 成功表示配置和資源快照已持久化；它不啟動原生 Agent、不調用模型、不分配 run。原生上下文在首次 send 時建立；已有 session 的恢復也由適配器負責。

AI 查詢執行身份、狀態和讀寫文件的 `context` 工具同樣由 Client 準備；輸入由 harnessctl send 投遞，context 不另行提供輸入模型，不增加 WebSocket 階段或執行身份。見 [執行上下文設計](client-context.md)。

**Server 不派發 init，不接收 initializing/initialized 等階段事件，也不維護下載、解包、初始化的狀態機。** 這些都是 Client 處理消息的內部步驟；普通診斷可以上報，但不是 Server 推進流程的依據。Server 只等待該消息的受理回執或錯誤。

準備或 init 失敗且能證明尚未提交輸入時，Client 回該 message 的錯誤 ACK，不調用 send、不虛構 run/result。init 成功後 send 失敗，也不把已提交的 session 綁定抹掉；是否受理依 send/事件證據判定。

## 六、accepted：直接回答這條消息的 run_id

```json
{"type":"event","id":"e:s1:192","message_id":"m1","agent_id":"reviewer","session_id":"s1","event":{"type":"accepted","ts":"2026-11-07T00:00:00Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"in-1","text":"請評審這次修改"}}
```

accepted 必須帶外層 message_id、agent_id 和 session_id。Client 在提交輸入前持久保存消息和本地待受理 send 的關係；適配器事件保持原樣，Client 在網路封裝中補上來源消息，不能靠正文相同猜測歸屬。

Server 校驗此消息確實投遞給該設備及 Agent 版本，在同一事務中：

1. 保存 accepted 原始事件。
2. 按 accepted.run_id 建立或核對 run，固定 device/session/conversation/Agent/版本歸屬。
3. 將 `(message_id, agent_id)` 關聯到 run_id 和 input_id。
4. 提交後 ACK 該 event。

```json
{"type":"ack","id":"e:s1:192","status":"ok"}
```

這就完成「Client 告訴 Server：這個 Agent 處理這條 message 的 run_id 是什麼」。不再發另一份 message 成功 ACK，也沒有 task ACK。

```text
M1 → coder    → accepted(run_id=R1, input_id=A1)
M1 → reviewer → accepted(run_id=R2, input_id=A2)
M2 → coder    → accepted(run_id=R1 或 R3, input_id=A3)
```

最後一行只有一個實際結果；適配器確認後不再改綁。accepted 表示輸入已受理，不保證模型已開始處理或執行成功。

## 七、event：正文、問題、過程和結果

| event.type | Server 的處理 | 可靠性 |
|---|---|---|
| accepted | 關聯消息和實際 run/input | 必須保存並 ACK |
| message | 按 run/message_id 更新 Agent 完整正文快照 | 必須保存並 ACK |
| request | 保存問題、權限請求及 pending/replied/expired 狀態 | 必須保存並 ACK |
| result | 保存 completed/failed/cancelled、完整輸出、錯誤、用量及費用 | 必須保存並 ACK |
| log | 展示工具過程、片段與診斷 | 可丟、可合併，不要求 ACK |

只有 accepted 必須逐條關聯來源 message/Agent；其餘事件可根據 session/run 找到固定歸屬。初始化期間尚未建立來源綁定的普通診斷也要帶 message_id、agent_id，但不形成初始化階段契約。

result 和其他公共事件使用同一外層：

```json
{"type":"event","id":"e:s1:640","session_id":"s1","event":{"type":"result","ts":"2026-11-07T00:00:10Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["in-1"],"status":"completed","output":"已完成","usage":null,"cost_usd":null,"error":null}}
```

Server 在同一事務保存關鍵事件、更新 run 投影及必要的 conversation 回覆，提交後才 ACK。result.input_ids 核對該 run 的已受理消息；一個 run 只結算一次，不因受理多條 message 重複寫最終回覆。回覆仍指向該 run 第一條受理的 conversation message，取消可以沒有最終回覆。

內層 message_id 是適配器的正文快照標識，與外層來源 conversation message_id 不同。讀取正文按完整快照更新，不能用可丟的 log 片段作為唯一正文。

## 八、answer 和 cancel：直接控制既有 run

```json
{"type":"answer","id":"a:18","session_id":"s1","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","answer":{"q1":"Go"}}
```

Client 核對公共 request 歸屬及 pending 狀態，經 stdin 交給 `harnessctl reply`。權限請求使用 allow/deny。answer 的 ok 只確認本地操作受理；問題是否真的 replied，仍看後續 request 事件。Server 在已有答案仍待確認時，不再接受另一份答案；明確拒絕後才可修正再答，重送原答案沿用同一 id。

```json
{"type":"cancel","id":"c:19","session_id":"s1","run_id":"550e8400-e29b-41d4-a716-446655440000"}
```

Client 核對指定 run 仍是該 session 的取消目標，再調用 `harnessctl cancel`。核對和執行須與該 session 的 send 串行協調；目標已結束則確認無需操作，無法確認目標時回 unknown，不能拿過期控制去取消後來的交互。harnessctl cancel 的作用域仍是該 session 當前及排隊工作，不新增單個 run 的原生取消接口；受影響的 run 各自回報 result。cancel 的 ok 不表示已 cancelled，終態看 result。

兩者不建立聊天 message，也不建立新 run。傳輸去重與恢復所需的控制 id 和回執仍要持久保存，但不把它們包裝成 task 或統一的 run_input。尚未 accepted 的消息沒有 run_id，不能用這個 cancel 偽造一個目標；消息撤回不在本稿的 run 控制範圍。

Client 等待 send 受理或下載資源時，answer/cancel 和事件回傳仍須能繼續，不能讓所有 session 共用一個阻塞佇列。

## 九、ACK 與失敗邊界

```json
{"type":"ack","id":"m:m1:reviewer","status":"error","error":"固定版本的技能包校驗失敗，消息尚未提交"}
```

| status | 含義 |
|---|---|
| ok | heartbeat 已更新、關鍵 event 已保存，或 answer/cancel 的本地操作已受理/無需再操作 |
| error | 明確拒絕或能證明尚未受理；附 error 說明 |
| unknown | message/answer/cancel 可能已受理，但暫時無法確認；附 error 說明並停止自動重送執行 |

message 的成功以 accepted 回覆，不能只回一個不含 run_id 的 ok。錯誤 ACK 不建立 run；Server 只在該消息/Agent 目標上記錄失敗或不確定，不把它寫成執行結果。

普通消息重送不表示重新執行：已受理就補傳原 accepted，仍在處理就等待同一次操作，unknown 則等待確定證據。對 error 且能證明未受理的消息，Server 可按策略退避後使用原 id 重試；Client 串行恢復該消息的準備流程，不改內容和版本，也不增加另一個業務身份。已受理消息需要再次執行時，發送新的 conversation message。

Client 可在尚未提交 send 時重試配置讀取和下載，但不能把同一 send 當成可任意重試的 HTTP 讀取。相同規則也適用於控制：已受理或不確定的 answer/cancel 不再次執行，只補傳既有回執；能證明未受理才可重試。

Server 的錯誤 ACK 不表示事件已保存；Client 只有收到 ok 才解除該關鍵事件的待同步保護。不確定狀態後若取得可唯一關聯的 accepted 等確定證據，仍可補傳並修正消息受理關係。

## 十、重連、重複與本地恢復

- 發送前持久保存消息或事件及其穩定 id。相同 id 重送要核對內容；相同 `(message_id, agent_id)` 即使被換一個外層 id 重送，也不能繞過已受理或 unknown 的收據再次執行。
- Client 在 send 前保存該消息、session 和待受理位置；同一 session 同時最多一個 send 等待 accepted，取得受理證據後可繼續下一條，不用等 result。Server 按 conversation 消息順序投遞同一綁定，Client 保持該順序，不並發初始化同一綁定。
- send 逾時、程序崩潰或斷線，都不能證明未受理。先查本地收據與公共事件；能唯一關聯就補傳，證據不足就回 unknown、保留現場並暫停該綁定的後續 send。不能盲目重放或換設備再執行。
- 同一來源的關鍵事件按本地順序補傳。Server 尚未建立 session/run 綁定時，先確認 accepted，再同步該 run 後續事件；這只限制上報順序，不阻塞本地 Agent 執行及控制。較晚事件先獲 ACK，也不能丟掉前面未確認的其他關鍵事件。
- 關鍵事件的穩定 id 可直接使用 session 和 JSONL 完整行位置；不增加線上的批次、pos/after/next 或同步階段。末尾半行不能作為事件上報。
- 已受理工作在心跳遺失或離線時可以繼續；重連後補傳關鍵事件，斷線不合成 failed/cancelled。普通 log 不阻止目錄回收，未確認關鍵事件、未決消息/控制及準備中的預留必須保護目錄和收據。
- Server 保留去重事實及消息/Agent 的受理關係；Client 保留仍可能重送的本地收據；endpoint 級的消息摘要及提交標識不隨 conversation 工作目錄一起回收。刪除本地會話後，同一消息不能當作新消息執行；發生無法判定的歷史缺失時按 unknown 處理。

本協議不宣稱跨 Server、Client 和原生 Agent 的恰好一次事務。可以可靠重試的是傳輸及資源讀取；原生 send 只能在能證明未受理時再提交。

## 十一、連接與實作邊界

WebSocket 地址為 `/api/client/ws`，升級請求使用 device key 與 `AgentUse-Protocol: agentuse-client/2`。認證及版本在升級階段檢查，成功後直接發 heartbeat，不另做業務握手；不相容時明確要求升級。不兼容舊版 task、prompt 以及 log/result 外層設計。

Server 發送 message、回答和取消控制，管理固定版本與消息/run 關係；不傳 shell、argv、原生 RPC 或 YAML，不指揮本地 init。Client 的 sessionService/harnessService 負責資源準備、快照、init、send 和恢復，適配器負責原生上下文及交互歸屬。

Local 與遠端共用相同的資源校驗、session 綁定和 harnessctl 執行路徑。Local 的固定配置和資源來自本地輸入，不經 Server HTTP/WebSocket。

[Agent 版本 API](server-agent.md) · [Skill 版本與下載](server-skill.md) · [Client session](client-session.md) · [harnessctl 介面](../../harnessctl/README.md) · [公共事件](../../harnessctl/EVENT.md)

## 升級與持久化

只支持 schema 7：消息及消息引用直接使用 UUID，messages.seq 保存順序；message_agents 以消息/Agent 複合鍵保存投遞和受理關係。舊版資料庫、inputs 收據和控制收據不讀取、不轉換，也不保留升級兼容邏輯。升級時重建 Server 資料庫、清空 Client 本地工作目錄並重新初始化和註冊設備。

公開 HTTP 入口使用 /api/conversations，設備標識使用 device_id，不保留舊入口或字段別名。WebSocket 只支持本文的 /api/client/ws 和 agentuse-client/2，兩端需一起升級。

## 嵌套 conversation 的業務邊界

嵌套對話的目標是 conversation 的父子組織，關係仍待 Server 實作。主、子對話都可以有多個 Agent，均通過相同 message、accepted 和事件鏈路執行；message 的 conversation_id 始終是本次目標對話。父對話引用只保存在 Server 的對話模型中，無需進入 WebSocket。Client 不判斷主子角色或提供 Work 子命令；Server 工具復用 conversation HTTP API，不增加 Work 握手、憑據交換或執行身份。詳見 [父子 conversation 設計](server-conversation.md#父子對話)。

## 當前執行的文件交互

Client 的 `context attachment list/download` 直接讀取 Server 的 conversation 附件，不按 run 篩選，無須活躍 run。message 只攜帶本次所選附件元資料，移除額外本地 path；relative_path 統一為 conversation 相對路徑。Client 不提前下載，download 才保存到 workspace/<relative_path>。upload 保留 workspace 相對路徑；若 Server 改名則同步保存在返回路徑。設備發布仍提供 X-Agentuse-Run，Server 校驗活動 run 歸屬。提示詞要求 AI 上傳產物並引用 download_path，不以本地路徑作為交付連結。不新增文件 WebSocket 事件。

送入模型的 `complete_messages` 使用可讀的對話文字，按順序標示作者、@收件人、引用的消息正文及附件名稱／ID／路徑；不把 API JSON、消息 UUID、序號、雜湊或執行資料直接拼入提示詞。API 維持結構化資料，Client 只負責排版。程式產生的標題、工具說明與提示詞統一使用英文，使用者和 Agent 的原始正文保留原文；既有 native session 的初始化快照不改寫。
