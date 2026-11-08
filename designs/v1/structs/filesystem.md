# 配置與目錄

## 目錄

```
~/.agentuse/             $AGENTUSE_HOME 可改
├── hub.json             hub 實例配置(跑 hub 的機器上)
├── hub.db               hub 的會話表與運行隊列(SQLite,store=sqlite 時)
├── daemon.json          daemon 配置(用戶電腦上)
└── worktrees/           daemon 的現場:<platform>/<session id>/
```

hub 和 daemon 通常不在一臺機器上;同一臺機器兩個都跑也行(本地開發)。

## hub.json

```json
{
  "listen": "127.0.0.1:7700",
  "store": "sqlite",
  "token_key": "用來籤運行令牌的隨機串",
  "admin_key": "管理接口的密鑰",
  "daemon_keys": { "alice-mbp": "daemon 密鑰" },
  "platforms": [
    {
      "id": "shop",
      "secret": "與平臺約定的密鑰",
      "callback_url": "https://shop.example.com/agentuse/callback",
      "policy": {
        "agent_name": "claude",
        "agent_user_id": "u_claude",
        "wake_roles": ["member"],
        "trusted_roles": ["member"],
        "actions": ["reply", "status"],
        "workspace_mode": "readwrite"
      }
    }
  ]
}
```

| 字段 | 默認 | 說明 |
|---|---|---|
| `listen` | `127.0.0.1:7700` | 監聽地址;對外暴露時放在 TLS 反代後面 |
| `store` | `sqlite` | `sqlite` / `memory` |
| `db` | `<home>/hub.db` | SQLite 文件 |
| `token_key` | 必填 | 運行令牌的 HMAC 密鑰 |
| `admin_key` | 空 | 管理接口的 Bearer 密鑰;空 = 管理接口關閉 |
| `daemon_keys` | 空 | daemon id → 密鑰 |
| `lease_seconds` | `120` | 運行租約;daemon 每 15 秒續一次 |
| `max_attempts` | `3` | 租約過期後最多領取幾次(含第一次),用完判 failed |
| `platforms` | 空 | 見 [models.md#platform](models.md#platform) |

完整示例:[`examples/hub.json`](../../../examples/hub.json)。

## daemon.json

```json
{
  "hub_url": "https://hub.alice.dev",
  "id": "alice-mbp",
  "key": "daemon 密鑰",
  "driver": "claude",
  "transport": "ws",
  "workspaces": {
    "shop": { "repo": "~/work/shop-api", "base_ref": "main" }
  }
}
```

| 字段 | 默認 | 說明 |
|---|---|---|
| `hub_url` | 必填 | hub 地址 |
| `id` / `key` | 必填 | 與 hub.json 的 `daemon_keys` 對應 |
| `driver` | `claude` | agent CLI 驅動 |
| `transport` | `ws` | `ws`(出站 WebSocket,hub 推)/ `poll`(HTTP 輪詢) |
| `workspaces` | 空 | 平臺 id → `{repo, base_ref, worktree}`;沒配的平臺給空目錄 |

完整示例:[`examples/daemon.json`](../../../examples/daemon.json)。

## 環境變量

| 變量 | 誰讀 | 說明 |
|---|---|---|
| `AGENTUSE_HOME` | hub / daemon | 數據目錄,默認 `~/.agentuse` |
| `AGENTUSE_URL` | agent 側命令 | hub 地址,daemon 啓動 agent 時注入 |
| `AGENTUSE_TOKEN` | agent 側命令 | 運行令牌,daemon 啓動 agent 時注入 |
| `AGENTUSE_ADMIN_KEY` | `agentuse admin` | 管理密鑰(也可用 `-key`) |

## hub.db

兩張表,每行把整條記錄存成 JSON(`data` 列),另抽出查詢要用的列:

| 表 | 列 | 說明 |
|---|---|---|
| `sessions` | `id` PK、`platform`、`thread`(`platform, thread` 唯一)、`updated_at`(納秒)、`data` | [Session](models.md#session) |
| `runs` | `seq` 自增 PK(入隊順序)、`id` 唯一、`session_id`、`platform`、`status`、`lease_until`(納秒,可空)、`data` | [Run](models.md#run) |

WAL 模式;表在啓動時 `CREATE TABLE IF NOT EXISTS`。
