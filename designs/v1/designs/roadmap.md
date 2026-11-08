# 實施計劃

已經做了:喚醒判定、會話、SQLite 持久化、WebSocket 推送 + 心跳租約、同一線程串行、過期回收重排、回調獨立密鑰、管理查詢接口、平臺側 Go SDK。

2026-10-18 定位改爲「平臺側管理的實例」(見 [README.md §2](README.md#2-定位平臺側管理的實例))。按這個定位,下面按順序做:

## 第一階段:賬號與歸屬

讓多用戶能用,這是現在最大的缺口。見 [accounts.md](accounts.md)。

| 事 | 改哪 |
|---|---|
| User / Agent / Device 三張表 | `internal/models`、`internal/store`(SQLite + 內存,`storetest` 補用例) |
| 平臺管理 agent 的接口(建 / 列 / 改 / 刪、吊銷設備) | `internal/hub`,`pkg/sdk` 加對應方法 |
| 喚醒按被 @ 的 agent 判,一條評論可叫醒多個 | `internal/services/wake`、`Hub.Notify` |
| 會話鍵加 agent;運行帶 `agent_id`;領取只給綁定設備 | `store.ClaimRun`、`internal/hub/connect.go` |
| 設備密鑰取代 `daemon_keys` | `internal/hub/server.go` 認證 |
| 離線排隊超時(`queue_ttl`)與離線提示 | reaper |

## 第二階段:一條命令接入

見 [connect.md](connect.md)。

| 事 | 改哪 |
|---|---|
| 接入碼(一次性、15 分鐘、只存哈希) | store + `POST /v1/connect` |
| `GET /install.sh`、`GET /download/<os>/<arch>` | `internal/hub` |
| `agentuse connect` / `disconnect` / `uninstall` | `internal/cli` |
| launchd / systemd --user 開機自啓 | 新包 `internal/service` |
| 發佈:多平臺二進制 + sha256 | GitHub Actions release |

## 第三階段:運維像起 Redis

| 事 | 說明 |
|---|---|
| 環境變量配置單平臺 | 不寫 hub.json 也能起 |
| `token_key` 自動生成 | 首次啓動寫進數據目錄 |
| Docker 鏡像 | `ghcr.io/memory-co/agentuse` |
| 更新 [onboarding.md](../onboarding.md) | 按新流程重寫 |

## 之後

| 事 | 現狀 | 方向 |
|---|---|---|
| 現場隔離 | 靠 Claude Code 權限模式(`plan` / `acceptEdits` + 工具白名單) | 只讀掛載、無網絡、無憑據;公開平臺放進容器跑 |
| 回調重試 | 回調失敗直接 502 交還給 agent | hub 側帶退避重試,或落一張待投遞表 |
| 非對稱簽名 | 進出各一把對稱密鑰 | hub 用 Ed25519 籤回調,平臺只存公鑰 |
| 其它驅動 | 只有 Claude Code | Codex app-server 等 |
| daemon 併發 | 一個 daemon 一次一條 | 一臺電腦上多個 agent 並行 |
| 進度展示 | activity 原樣轉發 | 平臺側 SDK 提供「先佔位後替換」的幫助函數 |
| 其它語言 SDK | 只有 Go | TypeScript(平臺多是 Web) |
