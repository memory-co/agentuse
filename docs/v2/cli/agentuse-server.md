# agentuse-server

服務端用的 CLI,做兩件事:

- **`start`**:起服務進程。只有它直接打開 SQLite 數據庫。
- **管理命令**(`user …` 等):都是 [HTTP API](../apis/README.md) 的客戶端，帶管理員令牌調接口，不碰數據庫。所以可以在任何能訪問服務的機器上跑，不用登到服務器上。

```
agentuse-server user create ──HTTP──▶ agentuse-server start ──▶ SQLite
       (管理命令，API 客戶端)              (服務進程)
```

## 命令

| 命令 | 說明 | 對應接口 |
|---|---|---|
| `agentuse-server start` | 起服務 | — |
| `agentuse-server login` | 保存服務地址和管理員令牌 | `GET /api/me` |
| `agentuse-server user create <用戶名>` | 建用戶 | `POST /api/users` |
| `agentuse-server user list` | 列用戶 | `GET /api/users` |
| `agentuse-server user token <用戶 id>` | 重置登錄令牌 | `POST /api/users/{id}/token` |
| `agentuse-server version` | 版本 | — |

## start

```bash
agentuse-server start -addr :8080 -db /var/lib/agentuse/server.db \
  -public-url https://agentuse.example.com -dist /opt/agentuse/dist
```

| 參數 | 環境變量 | 默認 | 說明 |
|---|---|---|---|
| `-addr` | `AGENTUSE_ADDR` | `:8080` | 監聽地址 |
| `-db` | `AGENTUSE_DB` | `~/.agentuse/server.db` | 數據庫文件 |
| `-public-url` | `AGENTUSE_PUBLIC_URL` | 空(按請求推斷) | 用戶電腦訪問服務的地址，寫進安裝命令 |
| `-dist` | `AGENTUSE_DIST` | `~/.agentuse/dist`(`install.sh` 裝到這裏) | 各平臺 `agentuse` 二進制目錄 |
| `-reset-admin-token` | | 關 | 啓動時給 `admin` 換一個新令牌並打印，管理員令牌丟了用 |

- 庫裏沒有用戶時，自動建管理員 `admin`,在日誌裏打印登錄鏈接(令牌只顯示這一次)。
- 啓動時把上次留下的 daemon `online` 狀態清成 `offline`。
- `-dist` 裏缺平臺的二進制時打警告，對應平臺的用戶跑 `setup.sh` 會下載失敗。
- 前臺運行，SIGTERM 優雅退出;用 systemd / docker 託管。

## 管理命令怎麼連服務

每個管理命令都需要服務地址和一個 **admin** 的登錄令牌，按下面的順序找，先找到先用:

1. 參數 `-server URL`、`-token ut_…`
2. 環境變量 `AGENTUSE_SERVER`、`AGENTUSE_TOKEN`
3. `agentuse-server login` 存下的 `~/.agentuse/server-cli.json`

令牌不是 admin 時，接口返回 403,命令提示「需要管理員令牌」。

### login

```bash
agentuse-server login -server https://agentuse.example.com -token ut_…
```

調一次 `GET /api/me` 驗證令牌有效、且是 admin,然後把地址和令牌寫進 `~/.agentuse/server-cli.json`(權限 0600)。

## user create

```bash
agentuse-server user create alice -id u_10086 -role user
```

| 參數 | 說明 |
|---|---|
| `-id` | 用戶 id,填接入方自己系統裏的 user id;不填生成 UUID。規則見 [tables/users](../tables/users.md) |
| `-role` | `user`(默認)/ `admin` |

調 [`POST /api/users`](../apis/users.md#post-apiusers),輸出用戶和登錄鏈接(令牌只顯示這一次)。

## user list

```bash
agentuse-server user list [-json]
```

調 `GET /api/users`,列出 id、用戶名、角色、創建時間。

## user token

```bash
agentuse-server user token u_10086
```

給用戶換一個新的登錄令牌，舊的立即失效。調 [`POST /api/users/{id}/token`](../apis/users.md#post-apiusersidtoken)。

管理員自己的令牌丟了，沒法再調接口，用 `start -reset-admin-token` 在服務器上找回。

## 和舊命令的對照

拆分前是一個二進制 `agentuse`,服務端命令 `agentuse server` / `agentuse user …` 直接讀寫數據庫:

| 舊 | 新 |
|---|---|
| `agentuse server …` | `agentuse-server start …` |
| `agentuse user create …`(直連數據庫) | `agentuse-server user create …`(調 API) |
| `agentuse user token <用戶名>`(直連數據庫) | `agentuse-server user token <用戶 id>`(調 API)+ `start -reset-admin-token` |
| — | `agentuse-server login`、`agentuse-server user list`(新增) |
| `agentuse daemon register …` | `agentuse register …` |
| `agentuse daemon run` | `agentuse daemon` |
| — | `agentuse status`、`agentuse harnesses`(新增) |

### 附件存儲參數

`start` 支持 `-attachments-dir`（環境變量 `AGENTUSE_ATTACHMENTS_DIR`，默認 `~/.agentuse/attachments`）、`-attachment-max-bytes`（`AGENTUSE_ATTACHMENT_MAX_BYTES`，默認 104857600）、`-attachment-session-max-bytes`（`AGENTUSE_ATTACHMENT_SESSION_MAX_BYTES`，默認 1073741824）。容量必須是正整數字節數。數據庫和附件目錄一起備份，目錄不能公開爲靜態資源。詳見 [attachment](../designs/attachment.md)。
