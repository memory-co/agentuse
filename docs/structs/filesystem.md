# 配置与目录

## 目录

```
~/.agentuse/             $AGENTUSE_HOME 可改
├── hub.json             hub 实例配置(跑 hub 的机器上)
├── hub.db               hub 的会话表与运行队列(SQLite,store=sqlite 时)
├── daemon.json          daemon 配置(用户电脑上)
└── worktrees/           daemon 的现场:<platform>/<session id>/
```

hub 和 daemon 通常不在一台机器上;同一台机器两个都跑也行(本地开发)。

## hub.json

```json
{
  "listen": "127.0.0.1:7700",
  "store": "sqlite",
  "token_key": "用来签运行令牌的随机串",
  "admin_key": "管理接口的密钥",
  "daemon_keys": { "alice-mbp": "daemon 密钥" },
  "platforms": [
    {
      "id": "shop",
      "secret": "与平台约定的密钥",
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

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:7700` | 监听地址;对外暴露时放在 TLS 反代后面 |
| `store` | `sqlite` | `sqlite` / `memory` |
| `db` | `<home>/hub.db` | SQLite 文件 |
| `token_key` | 必填 | 运行令牌的 HMAC 密钥 |
| `admin_key` | 空 | 管理接口的 Bearer 密钥;空 = 管理接口关闭 |
| `daemon_keys` | 空 | daemon id → 密钥 |
| `lease_seconds` | `120` | 运行租约;daemon 每 15 秒续一次 |
| `max_attempts` | `3` | 租约过期后最多领取几次(含第一次),用完判 failed |
| `platforms` | 空 | 见 [models.md#platform](models.md#platform) |

完整示例:[`examples/hub.json`](../../examples/hub.json)。

## daemon.json

```json
{
  "hub_url": "https://hub.alice.dev",
  "id": "alice-mbp",
  "key": "daemon 密钥",
  "driver": "claude",
  "transport": "ws",
  "workspaces": {
    "shop": { "repo": "~/work/shop-api", "base_ref": "main" }
  }
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `hub_url` | 必填 | hub 地址 |
| `id` / `key` | 必填 | 与 hub.json 的 `daemon_keys` 对应 |
| `driver` | `claude` | agent CLI 驱动 |
| `transport` | `ws` | `ws`(出站 WebSocket,hub 推)/ `poll`(HTTP 轮询) |
| `workspaces` | 空 | 平台 id → `{repo, base_ref, worktree}`;没配的平台给空目录 |

完整示例:[`examples/daemon.json`](../../examples/daemon.json)。

## 环境变量

| 变量 | 谁读 | 说明 |
|---|---|---|
| `AGENTUSE_HOME` | hub / daemon | 数据目录,默认 `~/.agentuse` |
| `AGENTUSE_URL` | agent 侧命令 | hub 地址,daemon 启动 agent 时注入 |
| `AGENTUSE_TOKEN` | agent 侧命令 | 运行令牌,daemon 启动 agent 时注入 |
| `AGENTUSE_ADMIN_KEY` | `agentuse admin` | 管理密钥(也可用 `-key`) |

## hub.db

两张表,每行把整条记录存成 JSON(`data` 列),另抽出查询要用的列:

| 表 | 列 | 说明 |
|---|---|---|
| `sessions` | `id` PK、`platform`、`thread`(`platform, thread` 唯一)、`updated_at`(纳秒)、`data` | [Session](models.md#session) |
| `runs` | `seq` 自增 PK(入队顺序)、`id` 唯一、`session_id`、`platform`、`status`、`lease_until`(纳秒,可空)、`data` | [Run](models.md#run) |

WAL 模式;表在启动时 `CREATE TABLE IF NOT EXISTS`。
