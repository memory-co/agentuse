# Client 執行上下文工具

AI 在執行中直接使用 `context`；人類通過 `agentuse context` 選擇或進入相同上下文。它提供當前 run 的輸入與文件能力，遠端 conversation 管理使用 `agentuse conversation`。操作文檔見 [執行上下文手冊](../../../docs/client/context.md)。

## 身份與選擇

不引入 context ID，也不增加 Server task、租約或初始化階段。描述文件固定 endpoint、conversation、Client session 和適配器包位置；run UUID 仍只能來自公共 accepted 事件。

| 標識 | 來源及作用 |
|---|---|
| conversation_id | Server 業務會話，或本地調試的命名空間 |
| session_id | Client 本機 session，固定 events.jsonl 和資源快照 |
| run_id | 適配器 accepted.run_id，一個 session 可以有多個 run |
| input_id | 適配器 accepted.input_id，對應一次被受理的輸入 |
| message_id | 遠端來源消息，由既有受理收據與 input_id 關聯 |

裸 `context` 從 `AGENTUSE_CONTEXT` 找到描述文件。人類進入 shell 時明確設置的 `AGENTUSE_CONTEXT_RUN` 優先，其次是適配器提供的 `AGENTUSE_RUN`；兩者都沒有時，通過固定版本適配器的公共 `harnessctl status` 讀取當前 run_id。不能通過最後一條 accepted、最後修改時間、唯一未結束結果或啓動時的 message 環境變量猜測。

這是必要的：適配器可以先受理多條排隊輸入，各自有 run UUID；原生進程也可能跨 run 複用。正在執行的 run 與最近受理的 run 可能不同。

## 命令與輸出

- `context input`：實際 run 定位、狀態及該 UUID 的已受理輸入；context 讀取消息收據中實際送入 harnessctl 的組裝結果，包含必要的 `complete_messages` 補充，不重新查詢最新共享歷史。
- `context attachment list`：固定輸入快照與本次 run 的輸出元數據，以 direction 區分。
- `context attachment read FILE_ID`：驗證文件並返回本地絕對路徑及 MIME 類型。
- `context attachment upload FILE`：上傳本次 run 的輸出，自動關聯 conversation/run，返回可引用的下載路徑。
- `context --help`：當前可用能力，不列出註冊、啓停等管理命令。

輸入按公共 accepted 的順序讀取，只匹配選定 run UUID。遠端輸入通過 conversation 下的 `.server/messages/` 收據，按 session/run/input 三者匹配來源 message、正文、業務上下文和附件快照。補充輸入屬於同一 UUID 時自然出現在同一結果中；本地輸入直接使用 accepted.text。

公共 accepted 已落盤而 Client 尚未保存來源收據時，工具短暫等待同一個受理關係完成；不能按 prompt 相似度匹配，不能退回另一條消息。只讀完整 JSONL 行；損壞的完整記錄明確報錯。request/result 同樣按 run UUID 計算 waiting 和終態。

輸出不包含設備密鑰、註冊配置位置或消息提交收據。Server API 仍沿用 `/api/sessions`，不因 CLI 改名改動 HTTP 路由或 DTO。

## 文件範圍與圖片

收到遠端 message 時，Client 按報文中的附件快照校驗及下載，不在執行前重新列舉 conversation 的最新文件。快照是該消息明確選定的附件，不自動攜帶整個 conversation 的文件。

工具將同一 run 全部已受理輸入的附件去重，同一 ID 的元數據衝突時報錯。其他排隊 run 的附件即使已下載到共享 workspace，也不能經本工具的當前視圖讀取。這個視圖不改變 conversation 共用 workspace 的既有模型。

`attachment read` 先確認 ID 在輸入快照或本次 run 的輸出內，再驗證緩存的大小和 SHA-256；缺失或內容損壞時，通過描述文件指定的 endpoint 註冊配置取得設備身份，從同一 Server 重新下載。下載不跟隨重定向。文件緩存繼續使用 os.Root、受管子目錄校驗及獨立臨時文件，不跟隨符號鏈接。

返回本地路徑後，AI 使用原生看圖或文件工具讀取內容。Client 不把圖片 base64 塞進 CLI JSON，也不建立另一個圖片 RPC。Codex、Claude 及自定義適配器共用同一個命令實現。

## 輸出上傳與查詢

附件只有輸入和輸出兩個方向：人類在 Server 上傳輸入，AI 通過 context 讀取；AI 通過 context 上傳輸出，人類在 Server 讀取。Client 不另設頂層 attachment 管理命令。

上傳源必須是普通文件，目標固定為當前 conversation/run，禁止通過命令參數覆蓋 Server、令牌或執行身份。Client 使用同 endpoint 的設備憑據和 X-Agentuse-Run；Server 在讀取 body 之前及發布事務中兩次檢查 run 歸屬與活動狀態，結束的 run 不可再發布。上傳不發消息、不創建 run，也不觸發 result。

輸出元數據以 Server 的 created_by_run_id 為事實來源。工具通過附件列表的 run_id 過濾查詢，不新增本機輸出賬本；列表游標同時綁定 conversation/run。來源 accepted 尚在上報時僅短暫重試只讀查詢，不重放 POST。回執丟失時再次 list 能發現已發布文件，防止盲目重傳。

list 將輸入快照與當前 run 輸出去重，read 對兩者使用相同的摘要校驗與緩存。其他 run 的輸出不因位於同一 workspace 而自動可讀，除非後續輸入的固定快照明確包含它。歷史輸出可讀，發布能力只屬於活動 run。

## 準備及文件佈局

```text
<work_root>/<endpoint>/<conversation>/
  workspace/.agentuse/attachments/<file_id>/<filename>
  .server/messages/<message-agent-key-hash>.json
  sessions/<session_id>/
    session.yaml
    context.json
    bin/context -> agentuse 可執行文件
    events.jsonl
    events.jsonl.d/
    runs/<adapter_run_uuid>.jsonl
```

runService 在首次 init 的指令快照中追加工具說明，保留 Agent 自身指令；不向共用 workspace 寫入 AGENTS.md 或 CLAUDE.md。每次啓動 session 驅動時準備描述文件與命令入口，將其 bin 前置到 harness 的 PATH。`AGENTUSE_SESSION` 明確表示 Client 本機 session，`AGENTUSE_CONVERSATION` 才是 conversation；不再將兩者混用。

這些步驟是 Client 處理輸入的內部行爲。send 和公共事件格式不變；Server 不下發 context init，也不記錄新的 context 生命周期。適配器私有 `.d/` 不由 Client 解釋。

## 人類入口與回收

`agentuse context --path context.json --run UUID` 可以讀取該 session 中指定 run；省略 run 時只選擇適配器確認的當前執行。`enter` 切換到 workspace，設置固定 run 的環境並打開 shell，從中直接使用裸 `context`。

裸命令不提供選擇其他 Server、path、session 或 run 的參數。需要切換時，人類重新通過 `agentuse context` 選擇。描述文件只是本機執行元數據，不是新的授權憑據。

歷史 run 在本機文件仍保留時可讀。附件下載憑據由私有註冊配置取得。Work 屬於 Server；Client 與 context 不判斷協調者、不處理工作令牌，也不提供 Work 子命令。Server 將業務說明作為純文本下發，協調 Agent 按說明取得 Server 提供的獨立工具。工具自行向 Server 校驗來源並申請授權，與 Client 的執行驅動無關。

context.json、命令入口和附件緩存隨既有 conversation quota 一起回收，不增加全局索引、全局 run 掃描或新的常駐服務。實現在 runService 和 CLI 入口；維持既有五個 service。

不保留 `agentuse session`、`--session` 或將 conversation 寫入 `AGENTUSE_SESSION` 的舊入口，也不轉換舊本機上下文。

移除頂層 `agentuse attachment`、舊 `context attachments` 和省略 read 的附件入口，不保留別名。
