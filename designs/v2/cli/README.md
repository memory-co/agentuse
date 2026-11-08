# CLI

> 历史 CLI 资料。当前命令与参数见 [Server CLI](../../../docs/server/cli.md) 和 [Client CLI](../../../docs/client/cli.md)。

命令行拆成兩個二進制，給不同的人用:

| 二進制 | 給誰 | 跑在哪 | 幹什麼 |
|---|---|---|---|
| [`agentuse`](agentuse.md) | 普通用戶 | 用戶自己的電腦 | 註冊本機、跑 daemon、看本機狀態 |
| [`agentuse-server`](agentuse-server.md) | 部署 / 運維服務的人 | 服務器(`start`);管理命令哪都能跑 | 起服務;通過 HTTP API 管用戶 |

爲什麼拆:

- 用戶電腦上只需要 daemon 那部分，不該帶着建用戶、開服務這些命令。
- 兩邊的配置和權限完全不同(daemon 密鑰 vs 管理員令牌),放一個二進制裏命令容易混。

兩個 CLI 都只通過 HTTP / WebSocket 和服務打交道;只有 `agentuse-server start` 這個服務進程本身讀寫數據庫。

## 構建與分發

```
cmd/agentuse/         → agentuse
cmd/agentuse-server/  → agentuse-server
```

- 發佈:`v*` tag 觸發 release,產出 `agentuse-{linux,darwin}-{amd64,arm64}`、`agentuse-server-{linux,darwin}-{amd64,arm64}`、`SHA256SUMS`。倉庫根的 `install.sh` 從最新 release 裝 `agentuse-server`,並把各平臺 `agentuse` 放到 `~/.agentuse/dist`(`-dist` 的默認值)。
- `make build`:兩個都編到 `bin/`。
- `make dist`:只交叉編譯 `agentuse`(linux / darwin × amd64 / arm64)到 `dist/`,`agentuse-server` 通過 `/download/agentuse-<os>-<arch>` 提供給 `setup.sh` 下載，見 [apis/setup](../apis/setup.md)。
- 拆開後，服務不能再「拿自己的可執行文件當 daemon 給」,所以必須有 `dist/`。`agentuse-server start` 啓動時檢查 `-dist` 目錄，沒有對應平臺的二進制就打一條警告。

## 約定

- 子命令風格：`<二進制> <名詞> <動詞>` 或單個動詞，如 `agentuse-server user create`、`agentuse register`。
- 參數用 `-flag value`(也接受 `--flag`),位置參數只放主對象(如用戶名)。
- 輸出給人看的中文;加 `-json` 時輸出 JSON,方便腳本。
- 成功退出碼 0,參數錯誤 2,其他錯誤 1;錯誤信息以 `錯誤:` 開頭寫到 stderr。
- 都有 `version` 和 `help`。
