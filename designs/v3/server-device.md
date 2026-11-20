# deviceService：執行設備

> 设备证书、设备私钥导入、续签与代次撤销已实现，见[安全通道与执行期授权](client-server-security.md)。

> 本文規則已落實到 遷移基線 `0001_initial`、Server API 與業務 service。使用 device/conversation 命名；session_id 只引用 Client 本地執行會話。時間為 Unix 秒，JSON 在 SQLite 使用 TEXT、MySQL 使用 JSON、PostgreSQL 使用 JSONB，關聯由 service 在事務內校驗與清理，不聲明數據庫外鍵。

## 職責

device 是一臺用戶的執行設備，client 是設備上運行的程序。deviceService 管登記、憑據、連接狀態和執行能力，不管理對話或 run 的業務終態。

- 登记设备时生成 Ed25519 设备私钥，返回 OpenSSH 正文。Client 在安全 WS 内证明持有私钥，Server 签发设备证书。
- 保存 client 本地探測後上報的 harness、版本與業務能力，不下發探測命令。
- 維護 online/offline 和最近活動時間，管理某個 harness 是否允許領取任務。
- 刪除/重置時撤銷憑據並斷開舊連接；未領取輸入可失敗，已經執行的 run 不由 server 合成終態。

## 操作與協作

| 操作 | 行爲 |
|---|---|
| 登記/註冊 | 核对预先保存的公钥指纹，保存设备证书序列号。持有当前私钥可以再次导入。 |
| 上線 | 验证设备私钥持有证明、证书及当前凭据代次，更新 client 上報的 run 協議能力與本地 harness 探測結果，不把 available 自動等同於 enabled |
| 心跳/離線 | 更新最近活動時間和連接狀態；在線連接對象由連接管理組件維護 |
| 開關 harness | 修改 enabled；重新開啓後通知派活 |
| 刪除/重置 | pending 輸入標記 rejected，sent 輸入標記 unknown，撤銷憑據並關閉連接；不改 accepted run 的實際終態 |

agentService 發佈版本時檢查設備歸屬和運行能力，runService 根據輸入固定的 agent 版本選擇可領取任務。設備刪除只改變可用性，不 UPDATE 歷史 agent_versions.device_id；用戶選擇新設備時需發佈新 agent 版本。探測由 Client 本地 harnessService 校驗 manifest.json、適配器入口、平臺及原生程序依賴完成；deviceService 保存上報的 harness 標識、接入包版本、程序版本及業務能力。Server 不保存全局 harnesses 定義或 YAML，也不向設備發送探測命令。

狀態爲 pending（未註冊）、offline、online。在線狀態表示連接狀態，不代表設備當前空閒；是否可以領取由 Client heartbeat 的 free_sessions/active 快照與 runService 的派發規則共同決定。

執行協議僅聲明 agentuse-client/3。Server 接受 client 上報的自定義 harness 標籤，不要求先註冊全局目錄，不按 codex/claudecode 分支。harness 列表接口是設備上報結果的只讀視圖，沒有創建或編輯 YAML 的 API。

## 表結構

### devices

設備主表。 `user_id` 是归属用户，`public_key_hash` 对应当前 Ed25519 私钥。重置替换指纹、增加 `credential_generation` 并撤销旧证书。历史字段 `setup_token` 和 `key_hash` 保留用于迁移兼容。

```sql
CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    public_key_hash TEXT,
    key_hash TEXT, -- 旧字段，新注册为空
    credential_generation INTEGER NOT NULL DEFAULT 1,
    setup_token TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    last_seen_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX devices_public_key
    ON devices(public_key_hash);
CREATE UNIQUE INDEX devices_key
    ON devices(key_hash);
CREATE UNIQUE INDEX devices_setup_token
    ON devices(setup_token);
```

### device_certificates

`0003_device_security` 增加证书元数据：serial、device_id、generation、key_hash（公钥摘要）、not_before、not_after、revoked_at。设备私钥只在 Client，CA 私钥只在 Server 的私有配置目录；数据库不保存私钥。每次连接检查序列号、代次与撤销状态，持续连接也定期复验。重置/删除与撤销在同一事务提交，之后断开连接；run 事件事实不由撤销伪造终态。

### device_harnesses

設備與 harness 的組合。`available` 表示 client 對本地接入包與程序的探測判定可用，`enabled` 表示用戶允許使用，二者都滿足纔可派活。`user_id` 是設備所有者的冗餘字段，由 service 保證一致。

```sql
CREATE TABLE device_harnesses (
    device_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL DEFAULT '',
    capabilities TEXT NOT NULL DEFAULT '[]',
    version TEXT,
    available INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, harness)
);
CREATE INDEX device_harnesses_user_harness
    ON device_harnesses(user_id, harness);
```

### device_capabilities

設備支持的 run 業務協議版本與能力，例如任務領取、標準事件、文件操作。每次上線以上報內容替換舊集合，不把進程流、JSON-RPC 或原生 hook 等 client 內部實現當成 server 指令集。

device_harnesses.harness_revision 是本地接入包版本，連接 capability 聲明公共傳輸協議 agentuse-client/3，不通過 steer/question/plan 等原生能力分支調度。Client 升級接入包後重新探測並上報，不用 server 編譯配置。

```sql
CREATE TABLE device_capabilities (
    device_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    PRIMARY KEY(device_id,capability)
);
```

## 相關設計

[Server 總覽](server-main.md) · [userService](server-user.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [conversationService](server-conversation.md) · [runService](server-run.md) · [fileService](server-file.md) · [嵌套對話視圖](server-conversation-nest.md)

## HTTP 入口

管理端只使用 `/api/devices`、`/api/devices/{id}`、`/api/devices/{id}/reset` 和 `/api/devices/{id}/harnesses/{harness}`。Client 使用 `/api/client/register`、`/api/client/me`、`/api/client/ws`。不保留 daemon 路由、daemon_id 或 SDK Daemon 類型別名。`agentuse daemon` 仍是 Client 啟動本機後臺程序的命令，不是 Server 業務對象。
