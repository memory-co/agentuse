# Client 執行上下文工具

AI 在執行中直接使用 `context`；人類通過 `agentuse context` 選擇或進入相同上下文。它提供執行身份、狀態與文件能力，遠端 conversation 管理使用 `agentuse conversation`。操作文檔見 [執行上下文手冊](../../docs/client/context.md)。

## 與 harnessctl 的邊界

context 屬於 Client 的平台工具層，harnessctl 屬於獨立的原生執行適配層。Client 準備業務綁定、命令入口和繼承環境，通過 harnessctl 驅動原生程序；AI 在原生程序的 shell 工具中調用 context。context 不是 harnessctl 的子命令，獨立適配器不依賴 AgentUse，也不解釋 endpoint、conversation、附件 API 或 Work 業務。

上下文由兩部分組合：

- **業務綁定**由 Client 提供：endpoint、conversation、Agent、本機 session、workspace 及訪問 Server 所需的配置。context.json 固定本機綁定，憑據由 Client 私有註冊配置取得。
- **執行事實**由 harnessctl 提供：原生上下文、實際 run、輸入歸屬、交互請求及結果。context 通過公共 status 和事件選擇、校驗 run，不讀取適配器的私有 `.d/` 狀態，也不另行分配執行身份。

endpoint/conversation 足以確定文件空間，但同一空間可同時有多個 Agent、本機 session 和 run。context 仍保留 session/適配器綁定，以便狀態查詢和輸出上傳取得正確執行來源；調用 AI 無需自行提供這些 ID。list/download 只使用 conversation 範圍，upload 仍按現行規則關聯活動 run。文件操作不參與 send 的受理、插話或排隊決策。

原生程序及工具子進程繼承的是 Client 準備的環境，不是僅從 cwd 獲得身份。原生進程可跨 run 復用，因此 session 的固定業務綁定與動態的當前 run 必須分開：沒有明確執行綁定時詢問公共 status，不把啓動時的 run/message 環境變量作為後續執行身份。

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

- `context status`：只返回執行身份、狀態和工作目錄，不包含輸入正文或輸入列表。
- `context attachment list`：直接查詢 Server 的 conversation 附件列表，不按 run 篩選，不下載文件。
- `context attachment download PATH`：以 conversation 相對路徑精確選擇文件，不接受文件 ID；查詢 Server 元資料，下載並校驗，保存到 `workspace/<relative_path>`，返回本地絕對路徑及 MIME 類型。
- `context attachment upload FILE`：上傳本次 run 的輸出，自動關聯 conversation/run，返回可引用的下載路徑。
- `context --help`：當前可用能力，不列出註冊、啓停等管理命令。

輸入由 harnessctl 統一定義：send 負責投遞，accepted 確認 input/run 歸屬，result 明確覆蓋的 input_ids。Client 保存 Server message 與 accepted 的業務關聯，不向 AI 提供第二份輸入模型，也不要求 AI 輪詢補充消息。

status 僅根據公共事件確認所選 run 的歸屬和 request/result 狀態，不讀取或等待 Client 消息收據，不返回設備密鑰及註冊配置位置。Server API 統一使用 `/api/conversations`。

## 文件範圍與圖片

Client 收到遠端 message 時只保存附件元資料及向 AI 說明名稱、ID、conversation 相對路徑，不提前下載。消息選擇哪些附件，是本次輸入的提示與記錄，不限制文件工具的查詢範圍。

`list` 直接分頁讀取同一 Server 的 conversation 附件列表；`download PATH` 依 conversation 相對路徑讀取該 conversation 的附件元資料，與是否附在當前消息、是否由當前 run 產生無關。因此先前 run 的文件、補齊消息的文件和後續上傳的文件都可查詢；Server 仍驗證設備有權訪問該 conversation，不允許跨 conversation 讀取。

兩個命令只需要既有 context 描述文件的 conversation 綁定，不讀取 harness status、accepted 或 run 收據來選擇附件。`status`、`upload` 和 `enter` 才需要實際 run；上傳仍保留輸出歸屬和活動狀態檢查。

Server 返回的 `relative_path` 是唯一文件路徑。download 參數使用這個完整相對路徑，在分頁附件列表中精確匹配，不按 basename 猜測，也不接受 ID 別名。Client 保存到 context 綁定的 `<workspace>/<relative_path>`，不受調用者工作目錄影響，不另建隱藏附件目錄。例如 `user_input/manual.pdf` 對應 `workspace/user_input/manual.pdf`。已下載且大小、SHA-256 一致時重用文件，缺失或損壞時按需下載；下載使用同 endpoint 的設備身份，不跟隨重定向，透過 os.Root 校驗父目錄、拒絕符號鏈接與越界路徑，校驗臨時文件後原子發布。

返回本地路徑後，AI 使用原生看圖或文件工具讀取內容。Client 不把圖片 base64 塞進 CLI JSON，也不建立另一個圖片 RPC。Codex、Claude 及自定義適配器共用同一個命令實現。

## 輸出上傳與查詢

附件只有輸入和輸出兩個方向：人類在 Server 上傳輸入，AI 通過 context 讀取；AI 通過 context 上傳輸出，人類在 Server 讀取。Client 不另設頂層 attachment 管理命令。

上傳源必須是 workspace 內的普通文件，保留其相對路徑；`user_input/` 專供使用者上傳，生成文件應放在 `reports/` 等其他位置。Client 以設備憑據和 X-Agentuse-Run 發布，Server 校驗 run 的 conversation、設備與活動狀態。已結束的 run 不能上傳，但仍可查詢及讀取 conversation 文件。上傳不發消息、不創建 run，也不結束執行。

Server 若為避免重名而分配新 `relative_path`，Client 同時將已上傳的內容保存到該 workspace 路徑，返回相符的 `relative_path` 和 `local_path`。固定英文工具指令要求 AI 在承諾交付文件前完成 upload，並在回覆中引用返回的 `download_path`；不能把本地相對路徑當作瀏覽器下載連結。

所有文件仍以 Server 附件元資料為事實來源，不新增本機附件賬本，不按 run 合併輸入／輸出列表。`direction` 表示使用者輸入或 Agent 產物，`run_id` 表示產物實際來源。回執丟失時先 list 核對，禁止盲目重放 POST。

## 準備及文件佈局

```text
<work_root>/<endpoint>/<conversation>/
  workspace/user_input/<filename>
  workspace/reports/<filename>
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

`agentuse context --path context.json --run UUID` 可查詢該 session 中指定 run 的狀態或操作附件；省略 run 時只選擇適配器確認的當前執行。`enter` 切換到 workspace，設置固定 run 的環境並打開 shell，從中直接使用裸 `context`。

裸命令不提供選擇其他 Server、path、session 或 run 的參數。需要切換時，人類重新通過 `agentuse context` 選擇。描述文件只是本機執行元數據，不是新的授權憑據。

歷史 run 在本機文件仍保留時可讀。附件下載憑據由私有註冊配置取得。Work 的父子對話組織屬於 Server。Client 與 context 不判斷主子角色，也不提供 Work 子命令。主、子對話使用相同的執行上下文；Server 提供的對話工具調用普通 conversation API，其通用身份接入不屬於 context 或 Work 的業務模型。新目標設計見 [父子 conversation 設計](server-conversation.md#父子對話)。

context.json、命令入口和附件緩存隨既有 conversation quota 一起回收，不增加全局索引、全局 run 掃描或新的常駐服務。實現在 runService 和 CLI 入口；維持既有五個 service。

不保留 `agentuse session`、`--session` 或將 conversation 寫入 `AGENTUSE_SESSION` 的舊入口，也不轉換舊本機上下文。

移除頂層 `agentuse attachment`、舊 `context attachments` 和省略子命令的附件入口，不保留別名。
