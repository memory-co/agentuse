# conversationService：共享對話與消息編排

> 執行鏈路使用 [agentuse-client/2](protocol.md)：直接投遞 message，消息/Agent 關係見 [server-run.md](server-run.md)，不再有 task 或獨立輸入 ID。

> 本稿對應 schema 9 的嵌套 conversation 實作。關聯不聲明數據庫外鍵，由 service 校驗與清理；不讀取或遷移舊數據庫。

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

父子 conversation 是對話自身的組織與協作能力。每個節點都是普通 conversation，均支持多 Agent 消息；頂層對話也可以沒有子對話。嵌套對話視圖直接使用這個能力；輪次元數據只固定消息範圍及結束事實，不增加 Work、任務身份、service 或原生執行生命週期。本節與下面的表結構是父子模型的唯一規範；[協作場景](server-conversation-nest.md)僅提供使用示例。

### 關係與歸屬

創建時接受 mode=single（默認）或 collaborative，創建後不可更改。single 自己執行，不能創建其他 conversation；collaborative 負責分析、拆分、協調及匯總，不自己執行業務任務。頂層可選兩種模式；只有 collaborative 頂層允許創建子對話。子對話必須 mode=single，不能繼續拆分。所有模式均支持每條消息指定多個 Agent；single 不表示只能有一個 Agent。

parent_conversation_id 為空表示頂層對話；非空時表示直接執行任務的內層對話，其父節點必須是 collaborative 頂層。整體只允許兩層，內層不能繼續創建 conversation。每個對話最多有一個父對話，僅在創建時指定；不提供掛接、移動或重新指定父對話，因而不會生成自引用或環。Server 在創建事務中校驗來源分工及父節點為頂層，違反層級限制返回 403，對所有入口及管理員均一致。

子對話繼承父對話的 user_id，created_by 按普通創建規則記錄。HTTP 入口復用通用身份校驗，本 service 校驗父對話的訪問與創建權限；讀取、發消息及文件操作仍按目標 conversation 校驗。Client 使用當前 endpoint 的設備身份及 execution context；設備請求必須有可核對的來源，缺失時直接拒絕，不降級成整個設備所有者的權限。Server 用已上報的實際 run/session、設備及 Agent 關係核對來源，再按真實父子關係限定允許的操作。Agent 清單也僅返回該來源可用的角色。協作模式頂層只能操作自己的直接子對話；內層僅執行自身任務，由 Server 在整輪結束後通知直接父對話，不能利用普通創建 API、省略父 ID 或代填頂層 ID 繞過禁止再委派的限制。這些操作均須由通用接口支持，不把憑據交換或協調角色判定加入組織視圖。

主、子對話各自保留消息歷史、文件樹和狀態。各節點的消息仍以 to_agent_ids / to_user_ids 明確選擇收件人，不繼承父對話上一次的收件人，也不綁定固定協調 Agent。工作目標及約束由消息表達，名稱和所有者使用 conversation 的既有字段。子對話用 parent_conversation_id 和 parent_agent_id 記錄「由哪個父對話中的哪個 Agent 創建」。Server 從已校驗的 context conversation create 來源寫入這對標識，調用方不能任意指定或覆蓋 parent_agent_id。它是創建關係，不依賴某條消息的收件人、message_id、run_id 或原生 session。頂層兩個字段均為空，內層均非空；父對話可以有多個 Agent，每個子對話各自記錄實際發起者。

### API

創建、讀取和發送都沿用 /api/conversations。界面與 Client 的 context conversation 命令組直接調用這些接口。CLI 的可用性與雙向協作流程見 [嵌套對話設計](server-conversation-nest.md)。

| 方法 | 接口 | 行為 |
|---|---|---|
| POST | /api/conversations | 創建對話；mode 為 single/collaborative，可選 parent_conversation_id 表示父對話 |
| GET | /api/conversations | 列出當前身份可見的頂層對話 |
| GET | /api/conversations?parent_conversation_id=P | 列出 P 的直接子對話，包含空對話 |
| GET | /api/conversations/{id} | 讀取普通對話快照，含 parent_conversation_id 及 rounds |
| GET | /api/conversations/{id}?round=N | 讀取第 N 輪的消息及執行範圍，N 必須為正整數 |
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

省略 prompt 和收件人即可先創建空子對話。之後發消息，使用同一套 conversation API。AI 只上傳自己的輸出；父側輸入文件由子 Agent 顯式讀取父文件樹，不跨對話復用附件 ID。返回值為 conversation、message、runs、deliveries；存在 Agent 輸入時同時返回 round，空對話或備註不返回 round。

子對話必須由已校驗的 Agent context 創建；人類或管理員可創建頂層對話，再向頂層 Agent 提出委派要求，不能在普通用戶請求中代填 parent_agent_id 建立子對話。創建子對話及其首條消息共用普通創建事務，任一收件人或內容校驗失敗時整體回滾。指定父對話的創建或列表請求先校驗父對話存在且可訪問，否則返回 404；不將不存在或無權訪問的父對話當作空列表。

### 輪次與自動通知

創建子對話時，調用方在首條消息中提供子問題及必要背景，不隱式複製父對話的全部歷史、初始化指令或附件。Agent 的最終回覆只保存在該子對話，作者仍為 agent，並記錄固定 Agent 版本。

**回傳只有一條路徑：子 conversation 的整輪執行結束後，由 Server 自動生成一條 child_conversation 消息送到父 conversation。** Client 不提供主動向父發消息的命令或接口，也不能用普通消息接口繞過來源範圍限制。通知不複製某個 Agent 的正文，不偽裝成父對話中的 Agent。

輪次由 conversationService 管理，定義如下：

1. 沒有活動輪次時，第一條含 Agent 收件人的輸入開始新一輪，記錄本對話內遞增的 seq 和 start_message_seq。創建空對話、備註、僅發給用戶的消息、上傳文件及 resolve/reopen 不開始或完成一輪。
2. 活動輪次尚未收束時收到的 Agent 輸入加入同一輪。一輪可以包含多個 Agent、多條輸入及多個原生 run，與 run 的 accepted/result 邊界互相獨立，不新增任務 ID。
3. 只有當本對話沒有 running/waiting run，也沒有 pending/sent/unknown/accepted 投遞時，才在同一事務固定 end_message_seq、finished_at。結果消息、run/投遞狀態與輪次結束邊界一起提交；後續輸入開始下一輪，不再改寫已結束區間。
4. completed、failed、cancelled 及已確認的投遞拒絕均可使一輪收束。投遞 unknown、設備離線、排隊和原生人工請求均不算結束。沒有 result 的拒絕不捏造 Agent 回覆或 run，原因保留在投遞記錄中。

「輪次結束」只表示所有執行已收束，不等於任務成功，也不等於用戶標記 resolved。子 Agent 遇到缺失信息或阻礙時，在最終答復中說明問題並正常結束，不能一邊等待協調者回信、一邊佔住同一輪。Server 通知父側後，協調者讀取結果並用普通 send 向該子對話指定 Agent 發送答案，開始下一輪。原生人工授權/提問保留原有 request/reply 鏈路，等待期間不發結束通知。

父消息示例：

```json
{
  "conversation_id": "parent-conversation-uuid",
  "author_role": "child_conversation",
  "author_conversation_id": "child-conversation-uuid",
  "author_round_seq": 1,
  "to_agent_ids": ["creating-parent-agent"],
  "to_user_ids": [],
  "attachments": [],
  "content": "Child conversation finished round 1. Read this round and review its results."
}
```

author_user_id、author_agent_id、author_agent_version_id 均為空，author_name 讀取子對話標題。author_conversation_id 是作者身份；author_round_seq 固定通知所指的輪次。reply_to_message_id 仍只引用同一對話的消息，不能填子消息 ID。子文件留在子對話文件樹，協調者顯式按子對話及路徑讀取，不復用附件 ID。

目標始終是子對話創建時記錄的 parent_agent_id；它獨立於某條消息的收件人或父 run 是否存活。父消息與普通 message_agents 分配原子提交，版本在這次父消息入隊時固定。父 Agent 正在執行時由適配器決定受理方式；父 run 已結束時可啓動後續執行；設備離線時保留待派發輸入。發起 Agent 已刪除時保留父子關係，顯示 failed 及原因，不改發給其他 Agent 或用戶。

### 可靠性與範圍讀取

子結果和輪次結束先提交，通知使用獨立事務；父側臨時不可用不能回滾子結果。已結束輪次本身就是持久化的通知意圖。Server 在結果/投遞變更後及定期補送時，找出缺少父通知的輪次；父消息的 (conversation_id, author_conversation_id, author_round_seq) 唯一鍵去重。進程重啓、事件重放及重複補送最多保存一條通知，同一子對話按輪次順序補送。這保證消息只落庫一次；原生執行仍沿既有可靠投遞協議。

rounds 的 parent_delivery、parent_message_id、parent_delivery_error 是只讀投影：pending 表示已結束待通知，delivered 表示父消息及其分配已保存，failed 表示發起 Agent 已刪除。delivered 不表示父 Agent 已處理。這些字段屬於輪次，不屬於某條 Agent 結果消息；頂層及未結束輪次不投影回傳狀態。

GET /api/conversations/{id}?round=N 返回 [start_message_seq,end_message_seq] 內的消息、該區間輸入關聯的投遞與 run，以及這些 run 的事件和問題，rounds 僅含指定輪次。不存在的輪次返回 404；非法、重複或未知查詢參數返回 400。對話元數據與回傳狀態仍為當前投影，不宣稱整個 DTO 是歷史時點快照。已結束輪次的消息與執行範圍不受下一輪影響；活動輪次可讀取當前進度。

Client 的 read CHILD_ID --round N 使用此接口。wait CHILD_ID --round N 等待該輪結束；省略 --round 時在開始等待時固定最新輪次，沒有任何輪次則立即返回 no_round。目標輪次結束即 settled，即使下一輪已開始也不繼續等。新父輸入、原生提問、父 run 結束及超時可以中斷等待；等待不是獲得自動通知的前提。

前端父對話的子對話通知默認折疊，摘要只顯示展開箭頭、子對話圖標、來源標題、輪次和結束狀態，減少與協調者回覆的重複；展開後顯示通知詳情、收件人及指定輪次的連結，不展示為 @Agent 回覆。子對話歷史保留各 Agent 原始回覆，並顯示輪次及通知狀態。指定輪次頁面提供只讀消息與執行範圍，可返回完整對話。

### 狀態與刪除

pending/running/waiting/idle 與 resolved 都只描述所選對話；子樹不另存總狀態。普通刪除遇到子對話時返回 409；顯式 recursive=true 才在同一事務確定整個子樹、校驗所有節點的權限與可刪除狀態並執行清理，任一節點忙碌則整體拒絕。有已結束但尚未送達的輪次，且其父對話不在刪除範圍內時，也拒絕刪除，避免丟掉待送通知。清理包含 conversation_rounds，文件前綴在數據庫提交後清理。單獨刪除可刪除的子對話不影響父對話及兄弟對話；已送達通知保留作者來源，來源已刪除時界面顯示來源不可用。

Work 表、工作令牌、專用 API/CLI/SDK 和前端頁面均已移除。schema 9 不讀取、遷移舊數據，兩端、前端和 SDK 一起更新；使用新數據庫，不保留舊回傳入口或別名。

對話分工的提示詞由 Server 根據 mode 及真實 parent_conversation_id 生成，透過每次派發的 message.context 明確要求 collaborative 頂層協調、single 頂層直接執行、內層直接執行。完整模板及 CLI 能力表見 [嵌套對話設計](server-conversation-nest.md#兩層分工與提示詞)。該約束不改變 Agent 版本或原生執行身份。

## 標識和消息順序

conversations.id 和 messages.id 均爲 server 生成的 UUID，SQLite 中使用 TEXT 保存。所有 conversation_id、message_id、reply_to_message_id 等關聯字段使用相同的 UUID 字符串。

UUID 只用於標識，不承擔消息排序。messages.seq 保存 conversation 內的遞增順序號。message 派發快照只包含本次正文、附件和必要業務說明，不把整份共享歷史放進 context。

Client 選定實際本地 session 後，向 Server 讀取 `complete_messages`：以該設備、Agent 固定版本及本地 session 最後一條已受理輸入為界，只補其後、本次消息之前的共享消息，並排除該 session 自己產生的回覆。新 session 尚無受理記錄時，補齊本次之前的歷史。消息保留作者、收件人、正文和附件，不攜帶舊 result、用量或執行日誌；本次正文只傳一次。

邊界直接來自既有 message_agents 的 accepted 關聯，不增加游標表或 harnessctl 命令。未受理的派發不推進邊界，未來消息不提前注入。Client 保存實際組裝的 context，供本機執行上下文查閱。

發送請求不做去重：每次成功發送創建新的 message UUID，不需要請求 key 或內容摘要字段。

## 已實現的消息模型與本次組織調整

本文 SQL 對應當前 schema 9。共享對話使用 conversation_id，原生執行會話使用 Client 產生的 session_id；兩者保持獨立。消息正文仍為原始字符串，收件人、作者、附件及執行關係沿用既有模型。mode 是 conversation 創建參數，不是舊的消息執行包裝；不接受 input/defer_start 或其他舊別名。父子協作不增加執行身份或 service；conversation_rounds 只保存本對話內的輪次區間。

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

共享對話的原始消息。`content` 統一保存對話正文；`author_role` 爲 user/agent/child_conversation。user 填 author_user_id，agent 填 author_agent_id 及固定 author_agent_version_id；child_conversation 填 author_conversation_id 及 author_round_seq。名稱分別關聯 users、不可變 Agent 版本和子對話標題，不保存 agent_key、agent_name 字符串快照。

`to_agent_ids` 和 `to_user_ids` 保存消息的收件人，兩者均為 JSON 字符串數組，可同時非空，三種作者的消息使用相同規則：

- `to_agent_ids`：穩定的 agents.id 數組，表示哪些 Agent 要處理這條消息；具體版本和實際執行歸屬保存在 message_agents 與 runs。
- `to_user_ids`：users.id 數組，表示消息發給哪些用戶；這些收件人不觸發 Agent 執行。

service 校驗元素為非空 ID 字符串，分別去重，檢查 Agent 可在該 conversation 使用、用戶存在且有權讀取該 conversation。收件人字段不授予對話訪問權；消息仍屬於共享歷史，由 conversation 權限決定可見性，不因定向收件而變成私信。

兩組收件人在消息首次入庫時固定，即使後續派發失敗、Agent 改名或 run 結束，也保留原始收件人。每條新回覆獨立指定收件人，不自動繼承父消息的收件人數組。重複 ID 在保留首次出現順序的前提下去重；空 ID 或帶首尾空白的 ID 拒絕寫入。

發送請求用 `attachment_ids` 選擇本條消息的附件。例如：

```json
{"prompt":"請查看這兩個文件","to_agent_ids":["coder"],"to_user_ids":["user-alice"],"attachment_ids":["at_input","at_report"]}
```

文件先通過附件 API 上傳，再在消息中引用；省略或空數組表示本條消息沒有附件，不繼承會話的其他文件。service 在寫入消息的同一事務內校驗附件屬於當前 conversation，將完整元數據快照保存到 `messages.attachments` JSON 數組，包含 `id`、`relative_path`、文件名、大小和摘要等字段。字節內容按 conversation_id/relative_path 存儲，`attachments.relative_path` 定義會話內的文件樹；用戶上傳位於 `user_input/`，AI 輸出可以使用多級相對路徑。最終 Agent 回覆保存本次 run 上傳的輸出文件快照。路徑和刪除規則見 [conversation-files.md](conversation-files.md)。

service 寫入時保證 user 消息只填 author_user_id，agent 消息填 author_agent_id 和 author_agent_version_id 並檢查版本歸屬；child_conversation 消息只填 author_conversation_id、author_round_seq，僅可由 Server 的輪次通知流程創建。讀取作者用 LEFT JOIN；角色選擇器展示當前版本名稱，歷史 agent 回覆按作者版本展示，不受後來改名影響。軟刪除不清空消息作者或已發佈版本。

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
    author_role TEXT NOT NULL CHECK(author_role IN ('user','agent','child_conversation')),
    content TEXT NOT NULL,
    attachments TEXT NOT NULL DEFAULT '[]',
    author_user_id TEXT,
    author_agent_id TEXT,
    author_agent_version_id TEXT,
    author_conversation_id TEXT,
    author_round_seq INTEGER,
    to_agent_ids TEXT NOT NULL DEFAULT '[]',
    to_user_ids TEXT NOT NULL DEFAULT '[]',
    reply_to_message_id TEXT,
    created_at INTEGER NOT NULL,
    CHECK ((author_role='child_conversation'
            AND author_conversation_id IS NOT NULL
            AND author_round_seq IS NOT NULL AND author_round_seq>0
            AND author_user_id IS NULL AND author_agent_id IS NULL
            AND author_agent_version_id IS NULL)
        OR (author_role IN ('user','agent')
            AND author_conversation_id IS NULL AND author_round_seq IS NULL))
);
CREATE UNIQUE INDEX messages_conversation ON messages(conversation_id,seq);
CREATE INDEX messages_replies ON messages(conversation_id,reply_to_message_id,seq);
CREATE UNIQUE INDEX messages_child_round
    ON messages(conversation_id,author_conversation_id,author_round_seq)
    WHERE author_role='child_conversation';
```

### conversation_rounds

每個 conversation 的本地輪次區間。seq 僅在同一對話中遞增，不是另一個全局 UUID 或執行身份。根對話也記錄輪次，只有有父關係的已結束輪次會生成通知。service 在同一事務校驗起止消息歸屬、序號、靜止條件和真實父子關係；不提供調用方修改輪次的接口。

```sql
CREATE TABLE conversation_rounds (
    conversation_id TEXT NOT NULL,
    seq INTEGER NOT NULL CHECK(seq>0),
    start_message_seq INTEGER NOT NULL CHECK(start_message_seq>0),
    end_message_seq INTEGER,
    created_at INTEGER NOT NULL,
    finished_at INTEGER,
    PRIMARY KEY(conversation_id,seq),
    CHECK ((end_message_seq IS NULL AND finished_at IS NULL)
        OR (end_message_seq IS NOT NULL AND end_message_seq>=start_message_seq
            AND finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX conversation_rounds_active ON conversation_rounds(conversation_id)
    WHERE finished_at IS NULL;
```

輪次結束後不再改寫 start/end/finished_at。parent_delivery 等是從該區間、父通知與 Agent 是否存在計算的 DTO 字段，不另存一套回傳狀態機。表結構只在本稿維護，協作場景稿不重複定義。

### 合併掉的輔助表

| 原表 | v3 處理 |
|---|---|
| message_formats | 不再建表，也不保留 format 字段；消息與執行副本均保存正文 |
| message_authors | 不再建表，作者保存在 messages.author_role、author_user_id、author_agent_id、author_agent_version_id；child_conversation 作者使用 author_conversation_id、author_round_seq；名稱關聯用戶、固定 Agent 版本或子對話標題讀取；回覆關係使用 reply_to_message_id，執行關聯留在 runs |

這些信息仍有作用，但都是一條消息自身的屬性，不需要再各拆一張 1:1 的表。

## 相關設計

[Server 總覽](server-main.md) · [userService](server-user.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [runService](server-run.md) · [fileService](server-file.md) · [嵌套對話視圖](server-conversation-nest.md)

消息附件採用顯式 attachment_ids 選擇，messages.attachments 保存元數據快照；省略時為空，不繼承會話文件。每個 conversation 的文件樹與目錄操作 TODO 見 [conversation-files.md](conversation-files.md)。

送入模型的 `complete_messages` 使用可讀的對話文字，按順序標示作者、@收件人、引用的消息正文及附件名稱／ID／路徑；不把 API JSON、消息 UUID、序號、雜湊或執行資料直接拼入提示詞。子對話通知保留可操作的 context conversation read CHILD_ID --round N 指令，作為來源導航。API 維持結構化資料，Client 只負責排版。程式產生的標題、工具說明與提示詞統一使用英文，使用者和 Agent 的原始正文保留原文；既有 native session 的初始化快照不改寫。
