# 接入與通信

怎麼把一臺電腦接成 daemon,以及 daemon 和服務之間說什麼。

## 接入一臺電腦

```
頁面「添加 daemon」 ──▶ daemons 新增一行(status=pending,帶一次性 setup_token)
        │
        ▼ 頁面展示安裝命令
curl -fsSL "https://<服務>/setup.sh" -o setup.sh && TOKEN="st_xxx" bash setup.sh
        │
        ▼ setup.sh 在用戶電腦上
1. 從 <服務>/download/agentuse-<os>-<arch> 下載二進制到 ~/.agentuse/bin/agentuse
2. agentuse register:用 TOKEN 換長期密鑰(dk_xxx),寫進 ~/.agentuse/daemon.json
3. 裝成後臺服務並啓動:macOS 用 launchd,Linux 有 systemd --user 就用它，否則 nohup
        │
        ▼
daemon 連上 WebSocket,上報本機 harness ──▶ 頁面上出現這臺電腦和它的 harness 開關
```

- 安裝令牌只能用一次，註冊後作廢;要換電腦或重裝，在頁面上「重新生成安裝命令」,舊密鑰同時失效。
- 二進制從服務的 `-dist` 目錄取(`make dist` 交叉編譯 linux/darwin × amd64/arm64)。目錄裏缺平臺時 `agentuse-server start` 會警告。
- 用戶在頁面上爲每臺 daemon 選擇開啓哪些 harness,默認都關閉。

## 認證

| 誰 | 憑據 | 怎麼帶 |
|---|---|---|
| 用戶(網頁 / 腳本) | 登錄令牌 `ut_` | 網頁 cookie;腳本 `Authorization: Bearer ut_…` |
| daemon 註冊 | 安裝令牌 `st_` | `POST /api/daemon/register` 的 body |
| daemon 長連接 | 密鑰 `dk_` | WebSocket 握手 `Authorization: Bearer dk_…` |

庫裏只存哈希(安裝令牌除外，見 [daemons](../tables/daemons.md))。

## 消息

註冊接口和 WebSocket 消息的完整格式見 [apis/daemon](../apis/daemon.md)。

## 派活與本機執行

服務領取 pending run，按本次執行的 daemon/harness 快照派發。一臺 daemon 發 ready 後才接下一次執行。租約過期或重連時沒有對應執行，服務重新排隊併產生新的 attempt；舊 attempt 的事件和文件操作失效。

每次執行讀取同一個 session 的共享消息和附件，在 `~/.agentuse/work/<session_id>/` 啓動新的原生會話。agent、harness、機器可以在後續 run 中更換，沒有原生續接狀態。

本機 CLI 路徑與參數由 `~/.agentuse/daemon.json` 的 harnesses 配置決定，內置命令定義見 [harness](harness.md)。協議見 [daemon API](../apis/daemon.md)，執行語義見 [共享 session](session-mentions.md)。
