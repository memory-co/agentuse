# CLI

一個二進制 `agentuse`,三類使用者。

## 實例主人

```bash
agentuse hub start [-config ~/.agentuse/hub.json]
```

按 [hub.json](../structs/filesystem.md#hubjson) 起 hub(默認 SQLite 存在 `~/.agentuse/hub.db`),同時每 10 秒回收一次租約過期的運行。Ctrl-C 優雅退出。

```bash
agentuse admin sessions [-platform shop] [-limit 20]
agentuse admin runs     [-platform shop] [-session s_…] [-status claimed] [-limit 20]
agentuse admin run <run id>
```

只讀查看會話和運行,輸出 JSON。讀 `AGENTUSE_URL` / `AGENTUSE_ADMIN_KEY`,或用 `-hub` / `-key`。見 [admin.md](admin.md)。

## 用戶電腦

```bash
agentuse daemon start [-config ~/.agentuse/daemon.json]
```

按 [daemon.json](../structs/filesystem.md#daemonjson) 連 hub(默認出站 WebSocket,`transport: poll` 改輪詢),領取運行,驅動 agent,跑的時候每 15 秒心跳。連不上 hub 時每 5 秒重試。

## agent(運行裏)

這些命令只在 daemon 啓動的 agent 裏能用,讀 `AGENTUSE_URL` / `AGENTUSE_TOKEN`:

| 命令 | 等價於 | 說明 |
|---|---|---|
| `agentuse reply "<文本>"` | `POST /v1/run/reply` | 回覆評論;文本爲 `-` 時從 stdin 讀 |
| `agentuse status <狀態>` | `POST /v1/run/status` | 改線程對象的狀態,如 `in_review` |
| `agentuse activity <type> "<文本>"` | `POST /v1/run/activity` | 報進度 |
| `agentuse run` | `GET /v1/run` | 打印這次運行的令牌範圍 |

## 其它

```bash
agentuse version
agentuse help
```

退出碼:0 成功,1 出錯(錯誤打到 stderr),2 用法不對。
