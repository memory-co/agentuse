# agentuse

普通用戶在自己電腦上用的 CLI。日常不用手動敲：頁面上的安裝命令(`setup.sh`)會替你下載、註冊並裝成後臺服務。下面這些命令是給排查問題和手動接入用的。

本機配置在 `~/.agentuse/daemon.json`(所有命令都可以用 `-config` 換路徑),格式見 [apis/daemon](../apis/daemon.md#本機配置)。

## 命令

| 命令 | 說明 |
|---|---|
| `agentuse register -server URL -token st_…` | 註冊這臺電腦 |
| `agentuse daemon` | 前臺運行 daemon |
| `agentuse status` | 看本機的註冊和連接狀態 |
| `agentuse harnesses` | 看本機檢測到哪些 harness |
| `agentuse work …` | 工作的協調者用來開線程、看線程(harness 在跑時用，不是給人敲的) |
| `agentuse attachment …` | 上傳、列出、下載 session 附件 |
| `agentuse version` | 版本 |

### register

```bash
agentuse register -server https://agentuse.example.com -token st_…
```

用頁面上生成的一次性安裝令牌換長期密鑰，寫進配置文件(保留已有的 `harnesses` 配置)。`-token` 不給時讀環境變量 `TOKEN`。已經註冊過的再註冊一次，會覆蓋成新的 daemon。

調用 [`POST /api/daemon/register`](../apis/daemon.md#post-apidaemonregister)。

### daemon

```bash
agentuse daemon
```

前臺運行：連上服務、上報本機 harness、領活、執行、彙報;斷線自動重連。Ctrl-C / SIGTERM 退出。

`setup.sh` 裝的後臺服務(launchd / systemd --user / nohup)跑的就是這條命令。日誌打到 stderr。

### status

```
$ agentuse status
服務     https://agentuse.example.com
daemon   dm_5c1c8602536e55cb
配置     ~/.agentuse/daemon.json
服務連通 正常(密鑰有效)
後臺服務 systemd --user: active
```

只讀：讀配置、調 [`GET /api/daemon/me`](../apis/daemon.md#get-apidaemonme) 驗證密鑰(不建 WebSocket,不影響正在跑的 daemon)、看後臺服務是否在跑。沒註冊時提示去頁面複製安裝命令。

### harnesses

```
$ agentuse harnesses
名字        命令                       版本
claude      /opt/node/bin/claude       2.1.310 (Claude Code)
codex       -                          未安裝
my-harness  /usr/local/bin/my-harness
```

按 daemon 的檢測邏輯列出本機 harness,和 daemon 連上時上報的一致。是否開啓在頁面上設置，這裏不管。

## 不做的

- 不在這裏開關 harness、開會話、管用戶：這些在網頁 / [HTTP API](../apis/README.md) 上做。
- 卸載先手動：停掉後臺服務、刪 `~/.agentuse/`。以後可以加 `agentuse uninstall`。

### work

```
agentuse work agents                                       # 能用哪些 agent
agentuse work threads                                      # 這個工作的線程和狀態
agentuse work new --agents code-helper "改 daemon 的重連邏輯"  # 開線程，打印線程 id
agentuse work new --defer-start         # 先建空線程，上傳附件後再 send
agentuse work send --agents code-helper ss_xxx "再補個測試"                       # 給線程發消息(在跑就是插話)
agentuse work read ss_xxx                                  # 看線程的消息
agentuse work resolve ss_xxx                               # 標記線程已完成
```

給[工作](../designs/work.md)的協調者用：讀環境變量 `AGENTUSE_SERVER`、`AGENTUSE_WORK`(工作令牌，daemon 起協調者時設好;也認舊名字 `AGENTUSE_WORK_TOKEN`)調 [works](../apis/works.md#工作令牌wt_) 允許的那幾個接口。沒有這兩個變量時報錯退出。輸出是給 harness 讀的純文本。

### attachment

給人和 harness 使用的會話文件命令，設計見 [attachment](../designs/attachment.md)，調用 [attachments API](../apis/attachments.md)。

```bash
agentuse attachment upload ./report.pdf
agentuse attachment upload ./report-v2.pdf --name report.pdf --json
agentuse attachment list --session ss_xxx --json
agentuse attachment download at_xxx --output ./report.pdf
```

- 所有子命令支持 `--session`、`--server`、`--token`、`--config`、`--json`。參數可以放在位置參數之前或之後。session 默認讀 `AGENTUSE_SESSION`；server 按顯式參數 → `AGENTUSE_SERVER` → daemon 配置選取；config 按顯式參數 → `AGENTUSE_CONFIG` → `~/.agentuse/daemon.json` 選取。
- 認證按顯式 `--token` → `AGENTUSE_TOKEN` → `AGENTUSE_WORK`→ daemon 配置中的 `key` 選取。前面提供了但無效就報錯，不降級換身份。顯式用戶/工作令牌和 server 已足夠時，不要求本機註冊。
- harness 默認用當前 session。使用 daemon 密鑰上傳時，從 `AGENTUSE_MESSAGE_ID` 取活動消息 id，缺少時直接報錯；僅對 dk_ 請求發送這個字段。用用戶令牌可以顯式指定有權訪問的其他 session；工作令牌只能指定本工作會話。不會從當前目錄名稱猜 session。
- `upload` 接受一個普通文件路徑，`--name` 可覆蓋文件名；拒絕目錄，流式上傳，成功打印 id、文件名、大小、download_path；`--json` 輸出 attachment 對象。不自動重試 POST，超時提示先檢查 list，避免重複附件。
- `list` 自動取完分頁，普通模式打印表格，`--json` 輸出完整 attachment 數組。
- `download` 的 `--output` 必填，寫臨時文件、覈對元數據大小和 SHA-256 後原子 rename。目標已存在就報錯；顯式 `--force` 才能覆蓋。失敗清理臨時文件；`--json` 輸出 `{"attachment": …, "output": "本地路徑"}`，不混入文件字節。
- HTTP、文件讀寫、校驗失敗都非零退出，錯誤寫 stderr。第一版 CLI 不提供刪除命令，刪除走頁面或 API。

示例：本機用戶上傳輸入文件，憑據也可以用環境變量提供：

```bash
agentuse attachment upload ./requirements.pdf --session ss_xxx --server https://agentuse.example.com --token ut_…
```

### session

```bash
agentuse session send ss_xxx --agents coder,reviewer "看看實現和風險"
agentuse session send ss_xxx --users alice "請查看結果"
agentuse session read ss_xxx --json
agentuse session runs ss_xxx
agentuse session wait ss_xxx --timeout 10m
agentuse session cancel ss_xxx sr_xxx
```

默認從 AGENTUSE_SESSION 取當前會話，顯式 session 參數覆蓋；支持 --server、--token、--config、--json。地址/憑據來源與 attachment 一致。send 用 --agents、--users 分別指定 Agent 和用戶收件人，對應 to_agent_ids、to_user_ids。只有 Agent 收件人會啟動執行，用戶必須已有會話讀取權限；兩組皆空時記筆記。補充消息的 run 歸屬由 Client accepted 決定。read 返回統一的 session 消息，不按 agent 區分上下文。

harness 內部的 read/attachment 請求自動帶 AGENTUSE_RUN、AGENTUSE_ATTEMPT，讀配置裏的 dk_，只在當前執行有效時可用；協調者優先用工作令牌。所有新執行都給出 AGENTUSE_AGENT、AGENTUSE_SESSION、AGENTUSE_MESSAGE_ID、AGENTUSE_RUN、AGENTUSE_ATTEMPT。session 管理和 send 仍需要用戶或工作令牌。

work new/send 支持 --agents coder,reviewer 和 --users alice（放在位置參數前），兩者可同時指定。--defer-start 則不帶消息或收件人。協調者開的線程爲 multi_agent。空線程上傳文件後，用 work send --agents 指明第一輪目標。
