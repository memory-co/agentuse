# hub 實例

> 代碼:[`internal/hub/`](../../../internal/hub/)、[`server/internal/store/`](../../../server/internal/store)

## 1. 定位:平臺側管理的實例

hub 是**平臺自己的實例**,像平臺的 Redis:平臺運維部署(或買託管版),平臺應用只認**地址**和**密鑰**;一個 hub 服務這個平臺上的所有用戶,用戶各自把自己的電腦接進來([connect.md](connect.md))。協議固定,實例可換。

hub 保存這些東西:

| 東西 | 是什麼 | 結構 |
|---|---|---|
| 會話表 | 線程鍵 → agent 會話 | [Session](../structs/models.md#session) |
| 運行隊列 | 每次喚醒一條運行,queued → claimed → succeeded / failed;claimed 帶租約 | [Run](../structs/models.md#run) |
| 策略 | 按平臺:誰能喚醒、誰的話可信、agent 能做什麼、現場權限 | [Policy](../structs/models.md#policy) |
| 用戶 / agent / 設備 | 誰的 agent 叫什麼、綁在哪臺電腦哪個目錄(**設計中**) | [accounts.md](accounts.md) |

**不存評論。** 平臺 notify 時可以把線程裏最近的評論一起帶上(`context`),hub 只把它們掛在這次運行上交給 daemon,不另存一份。

## 2. 會話映射

- 鍵是 `(platform, thread)`;有了 agent 歸屬之後變成 `(platform, thread, agent)`,同一工單裏 @ 不同人的 agent 是各自獨立的會話([accounts.md §4](accounts.md#4-運行只派給主人的設備))。線程鍵的格式由平臺決定,hub 不解析,只在算分支名時去掉第一個 `:` 之前的類型前綴。
- 第一次被喚醒時建會話,分支名 `agent/<agent_name>/<線程鍵安全形式>`,如 `issue:MUL-42` → `agent/claude/MUL-42`。
- 第一次運行結束時 daemon 回填 `agent_session_id`(Claude Code 的 session id),之後的運行用 `--resume` 續接。

## 3. 運行隊列

```
           ┌──────────── 租約過期且 attempts < max_attempts ────────────┐
           ▼                                                          │
queued ──claim──▶ claimed ──complete──▶ succeeded                     │
                     │  ▲ heartbeat 續租                               │
                     │  └──────┘                                       │
                     ├──complete(err)──▶ failed                        │
                     └──租約過期且 attempts = max_attempts──▶ failed ──┘(否則回到 queued)
```

- **一次 notify 喚醒 = 一條運行**,按入隊順序領取。
- **同一線程嚴格串行**:領取時跳過「所屬會話已有 claimed 運行」的那些。同一線程連着 @ 兩次,第二條要等第一條交回纔會被領;多個 daemon 同時在線也一樣。不同線程互不影響。
- **租約**:領取時設 `lease_until = now + lease_seconds`(默認 120 秒),daemon 每 15 秒 `heartbeat` 續一次。hub 每 10 秒回收一次過期的:`attempts < max_attempts`(默認 3)的回到 queued,否則判 failed(`error: lease expired`)。
- **一次領取一個身份**:每次領取 `attempts + 1`,運行令牌裏帶着這次的 attempt。被回收重領之後,上一次的令牌、心跳、交回都是 409——掉線的 daemon 回來也寫不進別人正在跑的運行。
- 只有 claimed 狀態、且是這一次領取的令牌能寫回;complete 之後令牌即使沒過期也寫不了(409)。
- 只有領取它的那個 daemon 能 heartbeat / complete 它。

## 4. 推送

daemon 連着 `/v1/daemon/connect`(WebSocket)空閒等待時,hub 不輪詢數據庫:新入隊、會話空出來、回收這三件事都會觸發一個變更信號,等着的連接醒來各自嘗試領取。領到就推給它,沒領到接着等;空閒期間每 15 秒 ping 一次,ping 不通就當連接斷了。見 [daemon.md](daemon.md#1-連接)。

## 5. 存儲

`store.Store` 是接口,兩個實現跑同一套行爲測試([`server/internal/store/storetest`](../../../server/internal/store/storetest/storetest.go)):

| 實現 | 用途 | 說明 |
|---|---|---|
| SQLite(默認) | 實例 | 一個文件 `~/.agentuse/hub.db`,純 Go 驅動(modernc.org/sqlite),不需要 cgo。單連接 + `BEGIN IMMEDIATE`,領取的「挑一條再改」在一個事務裏完成 |
| Memory | 開發、測試 | 重啓即丟 |

hub.json 裏 `"store": "sqlite" | "memory"`、`"db": "<路徑>"` 選擇。表結構見 [structs/filesystem.md](../structs/filesystem.md#hubdb)。

## 6. 管理接口

平臺運維用 `admin_key` 查看會話和運行(只讀):`GET /v1/admin/sessions`、`/v1/admin/runs`、`/v1/admin/runs/{id}`,或 `agentuse admin …`。見 [apis/admin.md](../apis/admin.md)。沒配 `admin_key` 時管理接口關閉。
