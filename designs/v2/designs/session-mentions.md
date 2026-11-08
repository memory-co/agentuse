# Session 中 @ 多個 agent

> Session 統一使用多 agent 共享模型。所有 agent 共用消息和附件，messages 根據 @ 目標自動新建 run 或插話。舊數據庫須清空重建，不提供舊協議兼容或歷史會話升級。

## 交互

一個 session 是一段共享對話，可以不指定 agent，也可以在每條消息裏 @ 一個或多個 agent。被 @ 的 agent 各執行一次，回覆帶 agent 身份，按實際完成順序顯示。

```text
用戶：@coder @reviewer 看一下附件裏的需求，分別說說實現和風險。
reviewer：有兩個兼容性風險……
coder：建議分三步實現……
用戶：@coder 按這個方案實現，把 reviewer 提到的風險一起處理。
coder：已實現，產物見附件……
用戶：@reviewer 檢查 coder 剛上傳的產物。
```

所有 agent 幹活時都看這個 session 裏的消息、其他 agent 的回覆和附件。session 不維護 agent 名單，不需要加入、退出、啓用或重新綁定操作。@ 直接選擇一個可調用的 agent。

第一版只有用戶或有權限的工作協調者能觸發執行。agent 回覆裏的 `@coder` 是文字，不自動派活；工作裏的自動委派繼續走 [work](work.md) 機制。

## 數據模型

| 對象 | 職責 | 關鍵字段 |
|---|---|---|
| session | 所有執行者共用的對話、附件、歸屬和完成標記 | id、user_id、created_by、work_id、title、resolved_at |
| message | 一條共享消息及其作者、目標 | id、session_id、seq、role、to_agent_ids、run_id（assistant）、agent_key、agent_name、content |
| session_run | 一條用戶消息交給一個 agent 的一次邏輯執行 | id、session_id、message_id、agent_id、agent_key、agent_name、daemon_id、harness、status、attempt_id、lease_until、result_message_id |

- 一條消息 @ 三個 agent，保存一條 user message 和三個目標投遞關係；空閒目標新建 run，忙碌目標複用其未結束 run 作爲插話。消息和附件仍只有一份。
- run 記錄“這次由誰執行”，不創建 session 與 agent 的長期綁定。一條消息裏重複 @ 同一個 agent 去重；第一版每條消息最多指定 8 個 agent。
- 創建 run 時讀取 agent 當前的 daemon/harness，保存本次運行時快照。agent 切換運行時後，新 run 使用新的運行時，已經排隊或執行的 run 保持原快照；需要改派時取消原 run，再發消息。
- `agent_key`、`agent_name` 是當時的身份快照，agent 改名、刪除後仍能識別歷史作者。agent_id 可用外鍵在刪除後置空，展示快照保留。
- agent 刪除後不能再 @ 它；它的 pending run 標爲 failed，已經執行的 run 可以按原快照完成。daemon 刪除、harness 被關閉或能力降級時，僅使相關執行失敗，不影響其他 agent 的結果。
- 新模型不保存 session 或 session-agent 級的 harness_session_id，不保存每個 agent 的歷史水位、已讀集合或私有消息視圖。原生 harness 返回的會話 id 如需排查，只記在本次 run 的執行日誌裏。
- 新增 session_runs、消息目標關聯表（含 run_id、投遞方式、狀態）和必要的執行事件字段；不增加 agent 參與關係表或按 agent 劃分的上下文表。表結構見 [session_runs](../tables/session_runs.md)。

## @ 的路由規則

界面輸入 `@` 後搜索 agent，選擇後生成攜帶 agent_id 的 mention。顯示名可以重複，服務端路由始終使用全局唯一的 agent_id。

HTTP 請求使用 `to_agent_ids`，服務端不從 prompt 字符串猜目標。郵件地址、代碼塊、引用、附件和 agent 回覆裏的 `@` 不會意外派活；腳本調用 agent 必須傳結構化目標。

- 目標非空：對每個 agent 分別判斷當前 session 中是否有未結束 run。沒有則創建 run；已有 pending/running/waiting run 則把這條消息作爲插話關聯到它，不再創建第二個 run。
- 同一條消息可以同時是給 A 的插話、給 B 的新任務。只檢查這個 session 中該 agent 的執行，不會插到它在另一個 session 的任務裏。
- 目標省略或爲空數組：只記錄消息，不執行。即使之前只用過一個 agent，也不從歷史推斷默認目標。前端可以保留輸入框裏用戶選中的 @，但每次請求都顯式帶目標。
- 不需要提前邀請 agent，也不根據以前是否出現過來限制目標。
- 任一目標不存在或不可調用，整個請求返回 400。需要新建 run 的目標還須校驗運行時、harness 和協議能力；插話目標校驗已有 run 的有效性。不寫入部分消息/目標。daemon 離線但此前聲明支持協議時可排隊，已存在的 pending run 繼續收補充消息。
- 請求成功後，各 run 獨立完成或失敗；一個 agent 的執行失敗不撤銷其他 agent 的結果。

## HTTP 接口

用 `mode: "multi_agent"` 創建新模型會話：

```http
POST /api/sessions
Content-Type: application/json

{"mode":"multi_agent","title":"附件能力評審"}
```

返回 201 和 `{"session": …}`，初始 idle，不要求 agent_id，也不需要 defer_start。可以先上傳 session [附件](attachment.md)。同時傳 prompt/input 時，按發送消息的規則原子創建第一條消息及相應投遞關係。

```http
POST /api/sessions/{id}/messages
Content-Type: application/json

{
  "prompt":"看一下附件裏的需求，分別說說實現和風險。",
  "to_agent_ids":["coder","reviewer"]
}
```

返回 201，統一表示共享 message 已創建；下面示例中 coder 空閒、reviewer 正在執行：

```json
{
  "message":{"id":31,"seq":12,"role":"user","to_agent_ids":["coder","reviewer"],"content":{"prompt":"…"}},
  "runs":[
    {"id":"sr_1","message_id":31,"agent_id":"coder","status":"pending"}
  ],
  "deliveries":[
    {"agent_id":"coder","run_id":"sr_1","kind":"new_run","status":"pending"},
    {"agent_id":"reviewer","run_id":"sr_0","kind":"steer","status":"pending"}
  ]
}
```

runs 只列本次新建的執行，deliveries 列每個目標的投遞方式和當前關聯的 run。全是插話時 runs 爲 `[]`；沒有目標時兩個數組都爲空。消息及目標投遞關係始終只存一份，插話也寫進共享 messages，不另外複製 user 消息。自定義 harness 的 input 仍是原始 JSON，目標字段在外層，不混進 input。使用 `Idempotency-Key` 重試同一用戶、session 下的發送請求時返回同一條 message，不重複創建 run，deliveries 按當前投遞關係和狀態返回；相同 key 不同請求體返回 409。key、請求摘要和結果引用在同一事務保存，隨 session 刪除。

| 接口 | 行爲 |
|---|---|
| `GET /api/sessions/{id}` | 返回 session、messages、runs，以及帶 run_id 的 events/steers/questions |
| `GET /api/sessions/{id}/runs/{run_id}` | 查看本次執行的 agent、運行時、狀態、事件和結果消息 |
| `POST /api/sessions/{id}/runs/{run_id}/cancel` | 取消排隊或執行中的 run；重複取消返回同一狀態，已成功/失敗的 run 返回 409 |
| `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` | 回答指定 run 的提問，不能跨 run 回答 |

插話也只走 `POST /api/sessions/{id}/messages` 和 to_agent_ids，不單獨暴露 steers 寫接口。內部 steer 仍用於將消息送進正在運行的 harness。

不提供 session 下的 agent 加入、退出、激活或 rebind 接口。歷史上調用過哪些 agent，由消息和 run 派生展示，不成爲權限或調度依據。

run 必須屬於 URL 指定的 session，看不到會話或對象不屬於該會話返回 404。`resolve` / `reopen` 保留；有 pending/running/waiting run 時不能 resolve 或刪除。已 resolved 的會話收到新消息時重新打開，包括只有筆記、沒有 run 的消息。

## 所有 agent 使用同一份 session 內容

session 是消息和附件的唯一來源。用戶消息、所有 agent 的回覆按 session 內嚴格遞增的 seq 排序，seq 在寫事務中分配，不依賴時間戳。assistant 消息記錄 agent 身份、run_id 和觸發消息。

每次執行開始時，服務從同一個 session 讀取當時已有的完整對話和附件，交給本次執行；不會爲不同 agent 篩選“自己的歷史”，也不按 agent 保存增量上下文。沒被 @ 的歷史消息、其他 agent 的回覆和產物同樣可見。

- 每個 run/attempt 都啓動新的原生 harness 會話，傳入共享歷史和本次任務，不使用其他 run 遺留的 Claude session、Codex thread 或 resume id。
- 派活明確指出觸發本 run 的 message_id，以及這次分配給它的 task_message_ids（含啓動前已收到的補充消息），防止 agent 把共享歷史裏另一條消息誤當成本次任務。觸發消息只作爲共享歷史中的一條消息出現，input 另作執行目標引用，不把同一段內容重複追加成新的對話。
- 不同 agent 的執行開始時間可能不同，後開始的 agent 可以看到已經寫入 session 的新回覆。同一條消息 @ 的多個 agent 不強制使用凍結的相同歷史快照。
- 一次模型調用開始後，再發消息 @ 正在執行的 agent 就是插話，由服務按目標路由到當前 run；沒 @ 它的消息不自動注入。agent 也可通過 `agentuse session read` 主動讀取 session 最新內容。
- 重派時重新讀取 session 當前內容，並啓動新原生會話。run 可記錄每個 attempt 開始讀取到的 context_seq 和附件 id，供排查；這些只是執行日誌，不是另存一套上下文，也不要求重派複用舊快照。
- session 變長時，如需截斷或摘要，按 session 統一的規則生成、保留消息來源和本次任務，不能爲每個 agent 維護不同的記憶。

daemon 開始執行時用當前 run/attempt 經 HTTP 獲取 session 消息，避免通過 WebSocket 傳輸整段長曆史。Claude/Codex 通過生成的 `.agentuse/session-history.json` 文件讀取共享對話，說明文件寫明路徑和本次任務；自定義 harness 在 stdin 的 `session_messages` 中獲得同樣的數據，用戶原 input 保持不變。歷史引用、附件內容和其他 agent 的回覆都是有來源的對話數據，不提升爲系統指令。

執行中的工具臨時輸出可以留在本次進程內；需要交給其他 agent 的結論、文件和後續工作依據必須寫回 session 消息或附件，不能依賴私有原生會話的隱藏記憶。

## 狀態與調度

每個 run 獨立流轉：

```text
pending → running → succeeded / failed
              ↕
           waiting

pending / running / waiting → cancelled
running / waiting ──租約過期──→ pending（新 attempt）
```

成功和失敗都產生一條帶 run_id 和 agent 身份的 assistant 消息，一個 run 只有一個最終結果消息。取消記錄狀態和取消事件，不僞造 agent 回覆。

session 的 status 爲派生值：任一 run waiting 時爲 waiting；否則依次按 running、pending、resolved/idle 返回。同時返回各狀態計數，頁面能顯示“2 個執行中，1 個等回答”。失敗只顯示在對應 run 上，不把整段 session 標爲失敗。

不同 daemon 可以並行執行。同一臺 daemon 第一版仍只跑一個 run，沿用 ready 表達空閒；如果以後提高本機併發，同一 `(session_id, daemon_id)` 仍串行，避免同時改寫同一個 session 工作目錄和說明文件。這裏是執行資源調度，不產生 session-agent 上下文或長期綁定。

### 發送消息即插話

是否插話按每個目標判定，不按 session 總狀態判定，也不要求用戶傳 run_id。保存 message、查找未結束 run、創建目標投遞關係在同一個寫事務裏完成；與 run 結束事務串行化。結束先提交就新建 run，消息先提交就關聯爲插話，不能漏掉或同時創建兩個 run。

- **pending**：補充消息掛到尚未派出的 run。執行開始時，可合併的 prompt 與主任務一起從共享 session 讀取，並在 task_message_ids 中標出；任意 JSON input 留待後續單獨執行。daemon 確認啓動並接收這些消息後標爲 delivered，不再重複通過 steer 推送。
- **running**：內部發 steer，攜帶補充消息 id、run_id 和 attempt_id；harness 接收後確認，投遞狀態變成 delivered。
- **waiting**：消息先掛到當前 run，等提問回答後在 harness 可接收插話時送達；不因爲等待回答就創建第二個 run。
- **harness 不支持插話或來不及送達**：當前 run 成功/失敗結束時，尚未 delivered 的消息自動排到下一次執行。按 seq 排序，將連續的文本補充合併成一個後續 run，複用原有 message，不追加重複 user 消息；將這些目標投遞關係改掛到新 run。創建後續 run 時校驗 agent 當前運行時，不可用則把相應投遞明確標爲 failed。
- **租約過期**：這是重派當前邏輯 run，保留未完成的投遞關係；新 attempt 從共享 session 讀取待處理任務，舊 attempt 的確認無效。
- **顯式取消**：當前 run 和仍掛在它上面的投遞一起取消，不因爲取消而自動再啓動後續 run。用戶需要繼續時重新發消息 @。
- **自定義 input**：消息仍只保存一次。能被 harness 接收的文本 prompt 可以插話；任意 JSON input 不強行拼成 prompt，排隊等當前 run 結束後按原始 input 執行。多個此類 input 按 seq 逐個執行，不合併成一個 JSON，也不跨過它們合併前後的文本。其餘投遞保留在消息目標隊列，前一個 run 結束後才創建下一個，仍滿足每個 session-agent 最多一個未結束 run。

這些都是消息目標的投遞狀態，不是 agent 獨立上下文。已經 delivered 的插話由當前 run 的最終回覆統一回答，可以讓一條結果關聯多條用戶消息，不要求每次插話再生成一條 assistant 回覆。

取消在事務中撤銷 attempt 的執行權並移出排隊，向在線 daemon 發 cancel。daemon 停止進程後才能再次 ready；離線重連也先停止已失效 attempt 再領活。已發生的工具副作用不能撤回；執行權撤銷後拒絕舊結果和附件上傳。這樣離線目標不會一直 pending，阻止 session 完成或刪除。

## 工作目錄和附件

工作目錄繼續按 session 劃分：

```text
~/.agentuse/work/<session_id>/
```

同一臺 daemon 上，不同 agent 執行同一 session 時使用這個目錄，每次執行重寫共享歷史文件和當前 harness 所需的說明。目錄按 session 保留，不按 agent 分目錄。切換 harness 時也不續接上次原生會話。

不同機器上的目錄是 session 的本地工作副本，不是自動同步的共享文件系統。需要跨機器交付的文件上傳爲 [session attachment](attachment.md)，由其他 agent 下載使用。不能只在回覆中給出某臺機器的絕對路徑。

附件繼續只歸屬 session，每次執行開始取得當前附件列表。之後上傳的文件，後開始的執行能看到；正在執行的 agent 如需使用，可以通過 CLI 主動列出並下載。不要求按目標 agent 或觸發消息凍結附件快照。

產物用 `agentuse attachment upload` 上傳，附件增加可空的 `created_by_run_id`，agent 身份從 run 的快照取得。原 source/created_by/daemon_id 保留。daemon 上傳在新協議下必須帶 run_id、attempt_id，提交文件前再次驗證執行權；不能只憑 message_id，因爲同一消息可有多個執行。

multi_agent 會話在尚未 @ 任何 agent 時也允許用戶上傳附件。上傳不檢查“session 綁定的 daemon 是否支持附件”；選擇執行目標時才校驗該 run 的 daemon 能力。

## daemon 協議和執行權限

hello 增加 `session-runs-v1` 能力；有附件的執行另要求 `attachments-v1`。只把新模型 run 派給支持新協議的 daemon。

```json
{
  "type":"run",
  "run":{
    "id":31,"run_id":"sr_1","attempt_id":"ra_1",
    "session_id":"ss_1","agent_id":"coder","harness":"claude",
    "context_seq":12,"task_message_ids":[31],"input":{"prompt":"…"}
  }
}
```

這個格式沒有 resume。hello 上報活動 `(run_id, attempt_id)`；heartbeat/event/steer/answer 都攜帶它們，message_id 僅用於關聯消息。每次領取生成新的 attempt_id，租約掛 run。

- 續租、進度、最終結果、提問、插話確認和附件上傳必須匹配當前有效 attempt。
- event 增加 attempt 內的 event_seq，按 `(run_id, attempt_id, event_seq)` 去重。
- 一個 run 的結果和終態在同一事務寫入。已接受的最終回報重發時返回已接受結果，不重複插消息或觸發工作通知。
- 舊 attempt 不能提交結果或附件。daemon 收到 stale 錯誤就停掉對應進程，停止完才能再次 ready。
- 服務重啓不創建新的邏輯 run，租約到期才重派。重派是新的進程和當前 session 輸入，工具副作用可能重複，不能保證外部操作恰好發生一次。

給 harness 設置 `AGENTUSE_AGENT`、`AGENTUSE_SESSION`、`AGENTUSE_MESSAGE_ID`、`AGENTUSE_RUN`、`AGENTUSE_ATTEMPT`，CLI 自動帶執行身份調用 HTTP。讀取 session 當前消息、附件列表和內容時，dk_ 還須帶 run/attempt（請求頭 `X-Agentuse-Run`、`X-Agentuse-Attempt`），服務驗證該執行確實分配給這臺 daemon 且未結束、租約有效。

同一份 session 對所有獲授權的執行可讀；執行結束或取消後，不再允許用該 run 讀取。產物上傳也檢查同一執行權。不能僅憑曾經在這個 session 幹過活就永久獲得讀取權，不能借執行身份調用 session 管理接口。daemon 的讀取與上傳必須帶當前 run/attempt。

## 歸屬與權限

新 session 歸創建者所有，created_by 記錄創建者；工作內的 session 歸工作主人。@ 別人的 agent 不轉移 session 歸屬，也不自動授予 agent 主人人類 API 的讀取、回覆、刪除權限。session 主人/admin，以及範圍內的工作令牌可以管理它。

普通 session 沿用當前“可以調用其他用戶的 agent”規則；work 內仍只能選工作主人自己的 agent。每次 @ 都是本次執行的授權，所選 agent 的機器會讀取共享 session 的消息和文件，界面展示其主人和運行時。未獲得當前 run 的 daemon 無權讀取。

第一版不增加人類羣成員、邀請鏈接或一般的成員管理功能。

## 工作、插話和提問

- work 的 coordinator_id 繼續指向 session，協調角色改由 work 上的 coordinator_agent_id 指定。它是 work 的調度配置，不把該 session 綁定爲只能用這個 agent。
- 工作頁輸入框默認選中協調 agent，並顯式發送 to_agent_ids；在這個 session 中也能 @ 其他 agent。只有以該 work 的協調 agent 身份執行的 run 纔得到工作令牌。
- 工作令牌綁定當前 run/attempt，隨該執行結束失效；不能擴展到其他工作或普通 session。自動通知協調者時，服務顯式創建以 coordinator_agent_id 爲目標的消息/run，不依賴 session 的默認 agent。
- 線程的一條消息有多個目標時，等它各目標的投遞得到最終結果後向協調者發一次彙總，逐個署名列結果、失敗或取消原因。插話複用已有 run 的結果；沒送達而改掛後續 run 的目標，等後續執行結束。結束事務先處理未送達消息的改掛，再判斷是否彙總，不能把舊 run 的結束誤當成補充消息已完成。按 `(thread_session_id, message_id)` 去重；仍有離線 pending run 時繼續顯示等待，可以單獨取消。
- steer/question 通過 run_id/attempt_id 歸屬到具體執行，提問、回答和超時處理不影響其他 run。用戶統一用 messages 接口發消息並 @ agent，服務爲忙碌目標生成內部 steer，提問回答仍使用對應 run/question 的接口。

## 數據庫初始化

只支持當前 schema，`PRAGMA user_version=1`。舊數據庫拒絕啓動，部署時顯式清空數據庫及附件；不進行數據遷移，不保留升級接口。sessions 沒有 agent/runtime/resume/lease 字段，執行狀態全部記錄在 session_runs。

## 頁面、CLI 和 SDK

新建會話不要求 agent。輸入框可多選 @，明確顯示這條消息交給誰；沒有目標時提示“僅記入會話”。會話裏用過的 agent 可以作爲最近使用項，由歷史派生，不需要管理名單。

各 agent 的回覆、計劃、進度、提問和附件都有署名；同一消息下分別顯示“排隊”“作爲插話等待送達”“已送達”等目標狀態。使用同一個輸入框，不增加獨立的插話發送入口。用戶可以下一條只 @ 失敗的 agent，不必重新跑全部。取消執行不隱藏曆史。

SDK 增加 Run、目標數組和 `{message, runs, deliveries}` 響應，以及 WaitRun、WaitMessageRuns。WaitMessageRuns 按消息目標的當前投遞關係等待，跟蹤插話改掛後的 run，不能只等待初次響應裏的 runs。WaitSession 在新模式下等待全部 run 終態；如配置遇提問即返回，同時給出所有 waiting run。所有會話使用相同接口和響應。

CLI 增加 `agentuse session send <id> --agents coder,reviewer "…"`、`agentuse session read <id>`、`agentuse session runs <id>` 和指定 run 的等待/取消。read 返回這個 session 的統一消息，執行身份只決定是否有權讀取，不改變內容視圖。attachment 命令自動帶 run/attempt。work new/send 可傳多個目標，原 --agent 對應單元素目標。

## 實現和驗收

run、共享消息、目標投遞、運行協議、附件/工作權限、網頁、CLI 和三個 SDK 已落地。服務端和 daemon 需一起升級，新 daemon 通過 `session-runs-v1` 聲明支持。

驗收覆蓋：

- 先 @ A，再 @ B，B 能看到同一 session 中 A 的回覆、所有用戶消息和附件；再次 @ A 也讀取同一份歷史。
- 不創建 agent 加入關係，不保存按 agent 的記憶；每次都是新原生會話，切換 agent 或 harness 無需遷移上下文。
- 同一消息 @ A/B，各有署名結果；後開始的執行能看到已經提交的新回覆，排隊期間新增附件在開始執行時可見。
- 同機不同 agent 使用同一 session 目錄且不併發改寫，跨機器通過 session 附件交換文件。
- A 正在執行時再 @ A/B，消息只寫一次：A 收插話，空閒的 B 新建 run；pending/waiting、結束競爭和不支持插話的排隊回退都不會漏消息或重複執行。
- 只記錄消息時不隱式調用上一個 agent；刪除 agent、切換運行時只按 run 快照規則影響後續任務和相關排隊任務。
- 發送重試、結果重發不重複創建 run/消息；過期 attempt 的心跳、結果、附件和讀取請求被拒絕，取消後能完成或刪除 session。
- 跨用戶/工作/run/question 的越權被拒絕；選擇外部 agent 不擴大人類 API 權限。
