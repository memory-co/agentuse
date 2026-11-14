# conversationService：共享對話與消息編排

> 執行鏈路使用 [agentuse-client/2](protocol.md)：直接投遞 message，消息/Agent 關係見 [server-run.md](server-run.md)，不再有 task 或獨立輸入 ID。

> 本稿對應 schema 8 的嵌套 conversation 實作。關聯不聲明數據庫外鍵，由 service 校驗與清理；不讀取或遷移舊數據庫。

## 職責

Conversation 是原共享對話概念的新名稱，不新增另一層 conversationService；原有消息、任務和文件歸屬隨之統一改名。session 是 client 本地的原生執行會話，見 [client-session.md](client-session.md)。

conversationService 管理 conversation、message 和共享歷史，並編排消息分配。一個 conversation 包含若干 message 和若干 run；message 與 run 的任務輸入關係是 **N:M**，由 runService 中 message_agents 連接。

- 創建/讀取/刪除對話，維護標題、歸屬、父子關係和各對話自身的共享歷史。
- 接收消息，保存 to_agent_ids 和 to_user_ids；按 to_agent_ids 調用 runService，對話原文只保存一次，每個目標 agent 保存一條 message_agents 關係，等待 accepted 關聯實際 run。
- 消費執行結果事件，把最終回覆寫爲 agent message，並記錄作者和被回覆的 message。
- 讀取對話時組合對話、run、message_agents、事件和問題的統一快照。

對話業務由獨立包 `service/conversation.Service` 實現，狀態變更和投遞上下文亦歸此對象，包含父子關係及可靠回傳。附件元數據、發佈與刪除編排、重名策略和消息附件快照都屬於 conversationService，源碼統一歸入 `service/conversation/`；消息上下文屬於本 service 的 `context.go`。文件字節通過獨立的 `service/file/` 本地/S3 後端存取；完整的源碼分工見 [fileService 實現分工](server-file.md#實現分工) 與 [Server 當前源碼組織](server-main.md#四當前源碼組織)。

![Conversation、消息、投遞與執行的關係](../images/conversation-run.svg)

## 發送和回覆

發送鏈路是 `controller → conversationService → runService`：先檢查發送權限及兩組收件人的有效性，在一個事務中寫一條 message 和 to_agent_ids 中每個 Agent 的 message_agents，提交後按 heartbeat 容量主動發送 message。任一收件人校驗失敗則整體回滾。

`to_user_ids` 表示消息發給哪些用戶，不建立 message_agents 或 run。只有用戶收件人時保存發給用戶的消息；兩組都為空時保存普通對話備註。省略的數組統一保存為 `[]`，不默認調用上次 Agent，也不從正文中的 @ 名稱推斷執行目標。

Web 輸入框默認沿用當前用戶在本 conversation 最後一次發送消息的 to_agent_ids，包含空選擇與多選，不等待 Agent 回覆；Agent 作者及其他用戶的消息不參與預選。手動改選或清空後，後台快照刷新不能覆蓋當前選擇。會話或代用戶身份切換時按各自記錄重新初始化，頁面刷新也由已保存消息恢復。這是 Web 的交互行為，每次發送仍明確填寫收件人，不改變 API 空數組語義。

此時只分配輸入，run_id 爲空。這一事務內爲每條 message_agents 固定 agent_version_id，後續派發不追隨 latest。Server 不能根據某個 agent 仍在執行，就認定新消息一定屬於它當前的 run。

```text
M1 @coder @reviewer → (M1,coder)、(M1,reviewer)
client accepted (M1,coder)：run_id=R1，input_id=I1
client accepted (M1,reviewer)：run_id=R2，input_id=I2
M2 @coder → (M2,coder)
client accepted (M2,coder)：
  run_id=R1 → 同一次交互的補充輸入
  run_id=R3 → 後續一次交互
```

(M2,coder) 最終關聯哪個 run，由適配器決定，並通過 accepted 上報。message 與 run 的 N:M 關係由此建立，消息/Agent 複合鍵直接關聯消息，不另分配執行身份。conversation 不綁定 agent，不設置參與者表或每個 agent 獨立的共享歷史。

收到 client result 後，在**同一事務**保存原始事件、更新 run 與輸入狀態、寫入 agent message、綁定 runs.result_message_id，並重算對話狀態。最終回覆的 reply_to_message_id 指向 first_message_id；messages 不保存 run_id。事務提交後才確認源事件位置，重複上報直接確認已有記錄，不重複寫回復。嵌套對話視圖只讀取對話，不參與執行結果消費。

message、request、log 是執行記錄，不能都變成普通聊天消息。cancel 入隊和送達只表示控制傳輸，斷線也只改變設備連接狀態；這些操作都不生成執行結果。只有 client result 決定 completed/failed/cancelled，cancelled 可以沒有最終回覆。

## 狀態和刪除

Conversation 狀態是實際 run 與未完成輸入的聚合投影，優先 waiting、running、pending，再到 idle；resolved 是用戶的完成標記。投影由 conversationService 通過 runService 查詢當前狀態後更新，run 狀態變化和最終結果事件都觸發重算；重算讀取當前實際 run 和未完成輸入集合，不用舊事件直接覆蓋投影。idle 不表示歷史任務全部成功。

有活躍 run 或待領取/待受理輸入時拒絕刪除，檢查與刪除共用事務；回覆消費前也要重新檢查對話是否存在，已刪除的對話不恢復創建。刪除對話由 conversationService 協調相關 service，在同一事務內清理消息、run、message_agents、事件、問題及輔助記錄；本地目錄或 S3 對象前綴由 fileService 清理。父子節點的刪除規則統一見下文「父子對話」。

## 父子對話

父子 conversation 是對話自身的組織與協作能力。每個節點都是普通 conversation，均支持多 Agent 消息；頂層對話也可以沒有子對話。嵌套對話視圖直接使用這個能力，不增加獨立業務身份、表、service 或執行生命週期。本節與下面的表結構是父子模型的唯一規範；[協作場景](server-conversation-nest.md)僅提供使用示例。

### 關係與歸屬

創建時接受 mode=single（默認）或 collaborative，創建後不可更改。single 自己執行，不能創建其他 conversation；collaborative 負責分析、拆分、協調及匯總，不自己執行業務任務。頂層可選兩種模式；只有 collaborative 頂層允許創建子對話。子對話必須 mode=single，不能繼續拆分。所有模式均支持每條消息指定多個 Agent；single 不表示只能有一個 Agent。

parent_conversation_id 為空表示頂層對話；非空時表示直接執行任務的內層對話，其父節點必須是 collaborative 頂層。整體只允許兩層，內層不能繼續創建 conversation。每個對話最多有一個父對話，僅在創建時指定；不提供掛接、移動或重新指定父對話，因而不會生成自引用或環。Server 在創建事務中校驗來源分工及父節點為頂層，違反層級限制返回 403，對所有入口及管理員均一致。

子對話繼承父對話的 user_id，created_by 按普通創建規則記錄。HTTP 入口復用通用身份校驗，本 service 校驗父對話的訪問與創建權限；讀取、發消息及文件操作仍按目標 conversation 校驗。Client 使用當前 endpoint 的設備身份及 execution context；設備請求必須有可核對的來源，缺失時直接拒絕，不降級成整個設備所有者的權限。Server 用已上報的實際 run/session、設備及 Agent 關係核對來源，再按真實父子關係限定允許的操作。Agent 清單也僅返回該來源可用的角色。協作模式頂層只能操作自己的直接子對話；內層僅執行自身任務及向直接父對話反饋，不能利用普通創建 API、省略父 ID 或代填頂層 ID 繞過禁止再委派的限制。這些操作均須由通用接口支持，不把憑據交換或協調角色判定加入組織視圖。

主、子對話各自保留消息歷史、文件樹和狀態。各節點的消息仍以 to_agent_ids / to_user_ids 明確選擇收件人，不繼承父對話上一次的收件人，也不綁定固定協調 Agent。工作目標及約束由消息表達，名稱和所有者使用 conversation 的既有字段。子對話用 parent_conversation_id 和 parent_agent_id 記錄「由哪個父對話中的哪個 Agent 創建」。Server 從已校驗的 context conversation create 來源寫入這對標識，調用方不能任意指定或覆蓋 parent_agent_id。它是創建關係，不依賴某條消息的收件人、message_id、run_id 或原生 session。頂層兩個字段均為空，內層均非空；父對話可以有多個 Agent，每個子對話各自記錄實際發起者。

### API

創建、讀取和發送都沿用 /api/conversations。界面與 Client 的 context conversation 命令組直接調用這些接口。CLI 的可用性與雙向協作流程見 [嵌套對話設計](server-conversation-nest.md)。

| 方法 | 接口 | 行為 |
|---|---|---|
| POST | /api/conversations | 創建對話；mode 為 single/collaborative，可選 parent_conversation_id 表示父對話 |
| GET | /api/conversations | 列出當前身份可見的頂層對話 |
| GET | /api/conversations?parent_conversation_id=P | 列出 P 的直接子對話，包含空對話 |
| POST | /api/conversations/{id}/parent/messages | 在來源子對話保存反饋，固定回傳給 parent_conversation_id 中的 parent_agent_id，不接受回信收件人參數 |
| GET | /api/conversations/{id} | 讀取普通對話快照，含 parent_conversation_id |
| POST | /api/conversations/{id}/messages | 明確選擇收件人及附件，發送普通消息 |
| POST | /api/conversations/{id}/resolve、reopen | 操作該對話自身的完成標記 |
| DELETE | /api/conversations/{id} | 刪除無子對話且可刪除的對話 |
| DELETE | /api/conversations/{id}?recursive=true | 顯式刪除該對話及整個子樹 |

創建主對話：

```json
{"mode":"collaborative","title":"改進搜索體驗","prompt":"請一起規劃並分工","to_agent_ids":["planner","reviewer"]}
```

在該主對話下創建子對話：

```json
{"mode":"single","parent_conversation_id":"parent-conversation-uuid","title":"搜索接口","prompt":"實現接口並驗證邊界條件","to_agent_ids":["coder","tester"]}
```

省略 prompt 和收件人即可先創建空子對話。之後發消息，使用同一套 conversation API。AI 只上傳自己的輸出；父側輸入文件由子 Agent 顯式讀取父文件樹，不跨對話復用附件 ID。返回值仍為 conversation、message、runs、deliveries，不新增 Work 響應包裝。

子對話必須由已校驗的 Agent context 創建；人類或管理員可創建頂層對話，再向頂層 Agent 提出委派要求，不能在普通用戶請求中代填 parent_agent_id 建立子對話。創建子對話及其首條消息共用普通創建事務，任一收件人或內容校驗失敗時整體回滾。指定父對話的創建或列表請求先校驗父對話存在且可訪問，否則返回 404；不將不存在或無權訪問的父對話當作空列表。

### 消息、反饋與生命週期

創建子對話時，調用方在首條消息中提供子問題及必要背景，不隱式複製父對話的全部歷史、初始化指令或附件。reply_to_message_id 仍只引用同一 conversation 的既有消息。

子對話的問題和進展由 context conversation send --parent 提交；本地產生的最終 Agent 回覆在保存時自動請求回傳。源消息可靠落庫後，由本 service 將正文、固定作者及來源引用送入直接父對話。回傳目標只由子對話的 parent_conversation_id / parent_agent_id 決定。父消息生成時設 to_agent_ids=[parent_agent_id]、to_user_ids=[]，再在父對話建立普通 message_agents 分配。源子消息維持自身的消息語義，不保存父側收件人快照，也不從源消息或父消息的收件人數組推算創建關係。

父消息是普通消息，會按已記錄的發起 Agent 入隊。parent_agent_id 使用穩定的 Agent 身份；具體版本在這條父消息入隊時按普通規則固定，不綁定創建子對話時的 run 或 Agent 版本。父 Agent 執行中時，沿既有輸入鏈路交給適配器決定如何受理；已結束時啟動後續執行；設備離線時保留待派發輸入。父 Agent 回答子問題時，向原子對話發普通消息並明確選擇子 Agent。整條通路不依賴某個父 run 持續存活，也不引入新的問題或任務身份。

回傳須符合以下規則：

- 源子消息保存 parent_delivery_requested，只有本地最終回覆或顯式反饋才能置為 1。父對話發下來的任務和轉發副本不設這個標記，避免自動回聲。
- 父消息保存 source_conversation_id / source_message_id，保留源消息的作者、固定版本及正文。reply_to_message_id 仍只引用同一對話，不能冒用源子消息作跨對話回覆 ID。
- 相同 source_message_id 向同一目標 conversation 最多落一條消息，父消息和 message_agents 在同一事務提交。轉發副本的 parent_delivery_requested 為 0，不再逐級自動轉發。
- 子對話結果和源消息先提交。父接收者暫時不可用時不回滾子結果；按已提交且標記回傳的源消息，以及缺失的父消息來源引用補送，覆蓋通知丟失、進程重啟和臨時錯誤。不掃描 run 作為 Work 結算。
- 回傳狀態由源記錄及父消息引用投影。反饋接口返回源 message、parent_conversation_id、可缺省的 parent_message_id，以及 pending / delivered / failed；消息讀取 DTO 也提供同樣的只讀回傳狀態投影。delivered 只表示父消息已保存，不表示父 Agent 已處理。發起 Agent 被刪除等不可恢復錯誤須顯示 failed 和原因，保留源反饋及創建關係，不自動清空或改寫 parent_agent_id，也不改成回給用戶或其他 Agent。
- 取消、投遞拒絕及準備失敗等沒有最終正文的結束情況，也須在源對話保存有明確對象的狀態反饋並回傳；以已確認的結果或拒絕事實為依據，不從設備離線推斷執行失敗或捏造 run。
- 附件留在來源對話；父消息保留來源引用，文件讀取再次校驗來源範圍，不直接把子附件 ID 當成父對話附件。

需要同一輪等待時，調用方仍可使用普通 conversation 的等待／讀取能力；自動回傳也允許父 Agent 先處理其他輸入，之後再接收結果。父子關係及回傳均屬於 conversation 消息協作，不建立獨立 Work 結算狀態。

pending/running/waiting/idle 與 resolved 都只描述所選對話。界面可組合展示子對話的既有狀態，不另存組織視圖的總狀態。普通刪除遇到子對話時返回 409；只有顯式 recursive=true 才在同一事務確定頂層及其全部直接子對話、校驗全部節點的權限與可刪除狀態並執行清理，任一節點忙碌則整體拒絕。有待回傳而目標父對話不在刪除範圍內的源消息時，也拒絕刪除，避免丟掉仍需送出的反饋。文件前綴在數據庫提交後按既有方式清理。刪除一個子對話不影響父對話及兄弟對話。

### 待替換的舊 Work 結構

| 現有結構 | 目標 |
|---|---|
| works 及獨立工作 UUID | 移除，直接使用主 conversation 的身份和元數據 |
| conversations.work_id、coordinator_id、work_role | 用 parent_conversation_id 表達對話關係 |
| coordinator_agent_id | 移除，每條消息明確指定零個、一個或多個 Agent |
| work_run_tokens、工作授權兌換接口 | 移除；Work 不擁有憑據或身份校驗邏輯 |
| run_notifications、Work 結算與補償掃描 | 移除；改用 conversation 源消息及父消息引用完成回傳與去重 |
| /api/works 及獨立 threads API | 移除，使用本節的 conversation 創建、查詢和消息接口 |
| Work 專用工具及返回包裝 | 收斂為 conversation API 的調用入口，不另行管理角色或執行身份 |

schema 8 已移除 Work 表、工作令牌、通知結算、專用 API/腳本/CLI/SDK 及前端頁面。更新時重建數據庫，同步更新 Client、Server 和 SDK，不保留舊入口。

對話分工的提示詞由 Server 根據 mode 及真實 parent_conversation_id 生成，透過每次派發的 message.context 明確要求 collaborative 頂層協調、single 頂層直接執行、內層直接執行。完整模板及 CLI 能力表見 [嵌套對話設計](server-conversation-nest.md#兩層分工與提示詞)。該約束不改變 Agent 版本或原生執行身份。

## 標識和消息順序

conversations.id 和 messages.id 均爲 server 生成的 UUID，SQLite 中使用 TEXT 保存。所有 conversation_id、message_id、reply_to_message_id 等關聯字段使用相同的 UUID 字符串。

UUID 只用於標識，不承擔消息排序。messages.seq 保存 conversation 內的遞增順序號。message 派發快照只包含本次正文、附件和必要業務說明，不把整份共享歷史放進 context。

Client 選定實際本地 session 後，向 Server 讀取 `complete_messages`：以該設備、Agent 固定版本及本地 session 最後一條已受理輸入為界，只補其後、本次消息之前的共享消息，並排除該 session 自己產生的回覆。新 session 尚無受理記錄時，補齊本次之前的歷史。消息保留作者、收件人、正文和附件，不攜帶舊 result、用量或執行日誌；本次正文只傳一次。

邊界直接來自既有 message_agents 的 accepted 關聯，不增加游標表或 harnessctl 命令。未受理的派發不推進邊界，未來消息不提前注入。Client 保存實際組裝的 context，供本機執行上下文查閱。

發送請求不做去重：每次成功發送創建新的 message UUID，不需要請求 key 或內容摘要字段。

## 已實現的消息模型與本次組織調整

本文 SQL 對應當前 schema 8。共享對話使用 conversation_id，原生執行會話使用 Client 產生的 session_id；兩者保持獨立。消息正文仍為原始字符串，收件人、作者、附件及執行關係沿用既有模型。mode 是 conversation 創建參數，不是舊的消息執行包裝；不接受 input/defer_start 或其他舊別名。父子協作不增加執行身份、表或 service。

## 表結構

### conversations

共享對話主表。`user_id` 是資源所有者，`created_by` 是創建者。parent_conversation_id 只可指向同一所有者的頂層對話，僅在創建時指定；所有 conversation 都支持多 Agent，不綁定固定協調 Agent，用 mode 選擇單體執行或協作拆分。嵌套對話視圖直接使用主對話 ID。狀態只投影本對話的消息與執行，不聚合子樹。parent_agent_id 是在父對話中發起創建的 Agent；它與 parent_conversation_id 一起在創建時由 Server 固定。兩者不表示消息收件人配置，也不隨消息、執行狀態或後續 Agent 版本變動。

```sql
CREATE TABLE conversations (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL,
    created_by TEXT NOT NULL,
    parent_conversation_id TEXT,
    parent_agent_id TEXT,
    mode TEXT NOT NULL DEFAULT 'single' CHECK(mode IN ('single','collaborative')),
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'idle',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (parent_conversation_id IS NULL OR mode='single'),
    CHECK ((parent_conversation_id IS NULL AND parent_agent_id IS NULL)
        OR (parent_conversation_id IS NOT NULL AND parent_agent_id IS NOT NULL))
);
CREATE INDEX conversations_parent
    ON conversations(parent_conversation_id,updated_at,id);
CREATE INDEX conversations_status
    ON conversations(status, updated_at);
CREATE INDEX conversations_user
    ON conversations(user_id, updated_at);
CREATE INDEX conversations_created_by
    ON conversations(created_by, updated_at);
```

### messages

共享對話的原始消息。`content` 統一保存對話正文；`author_role` 爲 user/agent；`author_user_id`、`author_agent_id` 按作者類型二選一。Agent 回覆還記錄 `author_agent_version_id`，關聯具體不可變版本讀取當時名稱；用戶名稱關聯 users，不保存 agent_key、agent_name 字符串快照。

`to_agent_ids` 和 `to_user_ids` 保存消息的收件人，兩者均為 JSON 字符串數組，可同時非空，用戶消息和 Agent 消息使用相同規則：

- `to_agent_ids`：穩定的 agents.id 數組，表示哪些 Agent 要處理這條消息；具體版本和實際執行歸屬保存在 message_agents 與 runs。
- `to_user_ids`：users.id 數組，表示消息發給哪些用戶；這些收件人不觸發 Agent 執行。

service 校驗元素為非空 ID 字符串，分別去重，檢查 Agent 可在該 conversation 使用、用戶存在且有權讀取該 conversation。收件人字段不授予對話訪問權；消息仍屬於共享歷史，由 conversation 權限決定可見性，不因定向收件而變成私信。

兩組收件人在消息首次入庫時固定，即使後續派發失敗、Agent 改名或 run 結束，也保留原始收件人。每條新回覆獨立指定收件人，不自動繼承父消息的收件人數組。重複 ID 在保留首次出現順序的前提下去重；空 ID 或帶首尾空白的 ID 拒絕寫入。

發送請求用 `attachment_ids` 選擇本條消息的附件。例如：

```json
{"prompt":"請查看這兩個文件","to_agent_ids":["coder"],"to_user_ids":["user-alice"],"attachment_ids":["at_input","at_report"]}
```

文件先通過附件 API 上傳，再在消息中引用；省略或空數組表示本條消息沒有附件，不繼承會話的其他文件。service 在寫入消息的同一事務內校驗附件屬於當前 conversation，將完整元數據快照保存到 `messages.attachments` JSON 數組，包含 `id`、`relative_path`、文件名、大小和摘要等字段。字節內容按 conversation_id/relative_path 存儲，`attachments.relative_path` 定義會話內的文件樹；用戶上傳位於 `user_input/`，AI 輸出可以使用多級相對路徑。最終 Agent 回覆保存本次 run 上傳的輸出文件快照。路徑和刪除規則見 [conversation-files.md](conversation-files.md)。

service 寫入時保證 user 消息只填 author_user_id，agent 消息填寫 author_agent_id 和 author_agent_version_id，並檢查版本屬於該 agent。讀取作者用 LEFT JOIN；角色選擇器展示當前版本名稱，歷史 agent 回覆按作者版本展示，不受後來改名影響。軟刪除不清空消息作者或已發佈版本。

message 永遠回覆另一條 message，不回覆 run。發送請求可顯式填寫 `reply_to_message_id`；service 在同一事務檢查它是同一 conversation 的既有消息，跨對話引用整體回滾。`reply_to_message_id` 表示被回覆的消息，字段保存被回覆消息的 UUID，開頭的根消息爲空；後續用戶消息、agent 回覆都可以引用同 conversation 中已有的消息。messages 不保存 run_id，也不以 run 爲對話層級。

`author_*` 表示誰發送，`to_*` 表示發給誰，`reply_to_message_id` 表示回覆哪條消息，三者獨立。寫入 Agent 最終回覆時，Server 明確填寫收件人；例如 user-alice 發給 coder 的消息，其 Agent 最終回覆設 `to_user_ids=["user-alice"]`、`to_agent_ids=[]`。僅引用 reply_to_message_id 不會再次喚起父消息中的 Agent；要讓另一個 Agent 繼續處理，須在新消息的 to_agent_ids 中明確指定。

```text
M1：@coder @reviewer 修復並評審
  ├─ M2：coder 的回覆，reply_to_message_id=M1
  ├─ M3：reviewer 的回覆，reply_to_message_id=M1
  └─ M4：用戶追問，reply_to_message_id=M1 或選中的具體回覆
```

一個 run 消費多條輸入時，最終回覆默認指向其主任務 message；補充輸入仍通過 message_agents 記錄。service 檢查父消息已存在、屬於同一 conversation，避免跨對話引用和循環回覆。

執行結果的內部關聯留在 runs.result_message_id，允許 runService 追蹤及去重，但不會出現在 messages 的字段或回覆語義中。conversationService 在同一事務調用 runService 檢查結果是否已綁定，未綁定才新增回覆並綁定；重複結果直接返回已有回覆。取消可以沒有回覆。

```sql
CREATE TABLE messages (
    id TEXT NOT NULL PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    author_role TEXT NOT NULL CHECK(author_role IN ('user','agent')),
    content TEXT NOT NULL,
    attachments TEXT NOT NULL DEFAULT '[]',
    author_user_id TEXT,
    author_agent_id TEXT,
    author_agent_version_id TEXT,
    to_agent_ids TEXT NOT NULL DEFAULT '[]',
    to_user_ids TEXT NOT NULL DEFAULT '[]',
    reply_to_message_id TEXT,
    parent_delivery_requested INTEGER NOT NULL DEFAULT 0 CHECK(parent_delivery_requested IN (0,1)),
    source_conversation_id TEXT,
    source_message_id TEXT,
    created_at INTEGER NOT NULL,
    CHECK ((source_conversation_id IS NULL AND source_message_id IS NULL)
        OR (source_conversation_id IS NOT NULL AND source_message_id IS NOT NULL))
);
CREATE UNIQUE INDEX messages_conversation_seq ON messages(conversation_id,seq);
CREATE INDEX messages_replies ON messages(conversation_id,reply_to_message_id,seq);
CREATE UNIQUE INDEX messages_source ON messages(conversation_id,source_message_id)
    WHERE source_message_id IS NOT NULL;
```

parent_delivery_requested 是源消息的回傳意圖，source_conversation_id / source_message_id 是回傳副本的來源；兩個來源字段同時填寫，service 檢查真實父子關係及源消息存在。這些字段均由 Server 編排維護，不接受調用方任意偽造。它們不改變執行身份，表結構也不在嵌套場景稿重複定義。

### 合併掉的輔助表

| 原表 | v3 處理 |
|---|---|
| message_formats | 不再建表，也不保留 format 字段；消息與執行副本均保存正文 |
| message_authors | 不再建表，作者保存在 messages.author_role、author_user_id、author_agent_id、author_agent_version_id；名稱關聯用戶或固定 agent 版本讀取；回覆關係使用 reply_to_message_id，執行關聯留在 runs |

這些信息仍有作用，但都是一條消息自身的屬性，不需要再各拆一張 1:1 的表。

## 相關設計

[Server 總覽](server-main.md) · [userService](server-user.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [runService](server-run.md) · [fileService](server-file.md) · [嵌套對話視圖](server-conversation-nest.md)

消息附件採用顯式 attachment_ids 選擇，messages.attachments 保存元數據快照；省略時為空，不繼承會話文件。每個 conversation 的文件樹與目錄操作 TODO 見 [conversation-files.md](conversation-files.md)。

送入模型的 `complete_messages` 使用可讀的對話文字，按順序標示作者、@收件人、引用的消息正文及附件名稱／ID／路徑；不把 API JSON、消息 UUID、序號、雜湊或執行資料直接拼入提示詞。API 維持結構化資料，Client 只負責排版。程式產生的標題、工具說明與提示詞統一使用英文，使用者和 Agent 的原始正文保留原文；既有 native session 的初始化快照不改寫。
