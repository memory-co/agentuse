# 執行上下文：context

Client 爲每個執行環境提供 `context`。AI 用它查詢執行身份與狀態、讀取 conversation 下的全部附件，並把輸出文件交給 Server；人類用 `agentuse context` 調試同一份上下文，也可以進入綁定該 run 的 shell。

文件交互採用兩個方向：

| 方向 | 上傳 | 讀取 |
|---|---|---|
| 輸入文件 | 人類在 Server 頁面或通過 API/SDK 上傳 | Client AI 通過 context 讀取 |
| 輸出文件 | Client AI 通過 context 上傳 | 人類在 Server 頁面查看、下載 |

## 執行身份與狀態

Client 初始化 session 時注入英文工具說明，將 `context` 加入 harness 的 `PATH`。直接調用：

```bash
context status
```

| 字段 | 含義 |
|---|---|
| `endpoint_id` | 輸入來源；本地調試爲 `local` |
| `conversation_id` | Server 或本地 conversation ID |
| `session_id` | Client 本機 session ID |
| `run_id` | 適配器 accepted 事件公佈的實際 run UUID |
| `agent_id` | 當前 Agent |
| `status` | `running`、`waiting` 或實際 result 的終態 |
| `workspace` | conversation 共享工作目錄的絕對路徑 |

`status` 只返回執行身份、狀態和工作目錄，不返回輸入正文、輸入列表或設備憑據。默認通過適配器公開的 status 確認當前 run；歷史 run 可顯式指定。

消息及補充消息統一由 `harnessctl send` 投遞到原生模型。輸入受理、插話、排隊和結果覆蓋範圍以適配器的 accepted/result 事件為準，AI 無需通過 context 再次讀取消息。

## 列出文件

```bash
context attachment list
```

結果為 `{"attachments": [...]}`，直接查詢 Server 中整個 conversation 的附件，不按 run 篩選，也不下載文件。附件包含：

| 字段 | 含義 |
|---|---|
| `relative_path` | 當前 conversation 文件樹中的相對路徑 |
| `id`、`name` | 文件 ID 和名稱 |
| `direction` | `input` 為使用者輸入文件，`output` 為 Agent 產物（包含其他 run） |
| `conversation_id` | 當前 conversation |
| `run_id` | 輸出文件所屬的 run；輸入文件不填此字段 |
| `mime_type`、`size_bytes`、`sha256` | 文件類型、大小、SHA-256 |
| `download_path` | Server 上可供用戶下載的路徑 |

列表以 Server 為準，包含歷史消息、補齊消息及後續上傳的文件；不依賴本地 run 附件收據。

## 下載圖片或其他文件

```bash
context attachment download user_input/截图.png
```

參數是 conversation 文件樹中的完整相對路徑，即 `list` 返回的 `relative_path`，不使用附件 ID。路徑包含空格時用引號包住，例如 `context attachment download "user_input/会议 截图.png"`。不接受絕對路徑或 `..` 等越界路徑。

命令從 Server 按路徑查找文件，下載並校驗大小和 SHA-256，返回 `local_path = workspace/<relative_path>`。例如 `user_input/截图.png` 保存到 `/絕對路徑/workspace/user_input/截图.png`。目標始終是 context 綁定的 workspace，從其他工作目錄調用也一樣。目錄不存在時自動建立；已下載且校驗一致的文件重用，缺失或損壞時重新下載。
圖片用模型原生看圖工具讀取 `local_path`，例如 Codex 的 `view_image` 或 Claude Code 的 `Read`。文本、PDF 等文件使用模型已有的文件工具。命令返回的 JSON 本身只提供文件信息。

## 上傳輸出文件

生成文件後直接上傳：

```bash
context attachment upload ./report.pdf
```

只接受 workspace 內的普通文件，保留相對路徑；workspace 外的文件應先複製進來。`user_input/` 為使用者上傳保留，AI 產物應放在 `reports/` 等其他位置。Server 若為避開重名而改變路徑，Client 會保存至返回的相同路徑，後續使用返回的 relative_path/local_path。目標自動綁定當前 conversation/run，不需要填寫 Server、token、conversation 或 run ID。結果爲 JSON，例如：

```json
{
  "id": "at_示例",
  "name": "report.pdf",
  "relative_path": "report.pdf",
  "direction": "output",
  "conversation_id": "當前conversation的UUID",
  "run_id": "當前run的UUID",
  "mime_type": "application/pdf",
  "size_bytes": 12345,
  "sha256": "文件內容的SHA256",
  "download_path": "/api/sessions/當前conversation的UUID/attachments/at_示例/content"
}
```

產物必須先 upload，再在最終回覆中引用返回的 `download_path`；本地相對路徑不是瀏覽器下載連結。人類可在 Server 頁面查看、下載該附件。上傳只保存文件，不自動發消息或結束執行；AI 繼續按原流程回覆用戶。

Server 在接收和發佈文件時檢查 run 的設備、conversation 和活動狀態。已結束的 run 無法繼續上傳。連接中斷時 Server 可能已經保存，先用 `list` 覈對再決定是否重傳；工具不會自動重複上傳。輸出列表以 Server 記錄爲準，不依賴本機另存一份上傳清單。

## 人類調試上下文

Client 爲本機 session 生成描述文件：

```text
<session.work_root>/<endpoint_id>/<conversation_id>/sessions/<session_id>/context.json
```

默認工作根目錄是 `~/.agentuse/tmp`。在本機頁面找到 endpoint、conversation、session 和 run 後，可顯式查看：

```bash
agentuse context --path /絕對路徑/context.json --run RUN_UUID status
agentuse context --path /絕對路徑/context.json attachment list
agentuse context --path /絕對路徑/context.json attachment download user_input/截图.png
agentuse context enter --path /絕對路徑/context.json --run RUN_UUID
# 進入後：
context status
context attachment list
exit
```

`--path` 默認讀取 `AGENTUSE_CONTEXT`；省略 `--run` 時詢問適配器當前執行。`enter` 使用 `$SHELL`，未設置時使用 `/bin/sh`，工作目錄切換到 conversation workspace，並通過 `AGENTUSE_CONTEXT_RUN` 固定所選 run。裸 `context` 不提供切換 Server、session 或 run 的參數。

文件保留期間可以顯式查詢歷史 run 的狀態；附件查詢及下載需要 Server 可用。歷史 run 的上傳會被拒絕。上下文文件和緩存隨 conversation quota 回收。本地調試支持執行狀態查詢，沒有 Server 上傳目標。

## Server 業務工具

Work 的協調、跨設備派發和工作授權由 Server 負責。協調 Agent 按 Server 隨消息提供的說明獲取獨立工具；Client 和 context 不判斷 Work 角色。操作其他線程的業務行爲見 [Server Work 手冊](../server/work.md)。

遠端 conversation 管理使用 `agentuse conversation`。文件操作統一爲 `context attachment list/download/upload`；下載只按路徑選擇文件，舊的 `read FILE_ID` 入口不保留。頂層 `agentuse attachment`、舊 `context attachments` 和省略子命令的入口也已移除。

命令成功時退出碼爲 `0`，執行或存儲錯誤爲 `1`，參數錯誤爲 `2`。其他管理命令見 [Client CLI](cli.md)。

`attachment list/download` 只需 context 描述文件，不需要活躍 run，也不按 `--run` 過濾；`status/upload/enter` 仍綁定實際 run。
