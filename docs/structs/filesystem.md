# 配置与目录

## 目录

```
~/.agentuse/             $AGENTUSE_HOME 可改
├── hub.json             hub 实例配置(跑 hub 的机器上)
├── hub.db               hub 的用户、agent、设备、会话表与运行队列(SQLite,store=sqlite 时)
├── daemon.json          daemon 配置(用户电脑上,agentuse connect 写)
└── worktrees/           daemon 的现场:<platform>/<session id>/(daemon.json 的 worktrees 可改)
```

hub 和 daemon 通常不在一台机器上;同一台机器两个都跑也行(本地开发)。

## hub.json

```json
{
  "listen": "127.0.0.1:7700",
  "public_url": "https://agentuse.shop.example.com",
  "store": "sqlite",
  "token_key": "用来签运行令牌的随机串",
  "admin_key": "管理接口的密钥",
  "platforms": [
    {
      "id": "shop",
      "secret": "与平台约定的密钥",
      "callback_url": "https://shop.example.com/agentuse/callback",
      "policy": {
        "wake_roles": ["member"],
        "trusted_roles": ["member"],
        "actions": ["reply", "status"],
        "workspace_mode": "readwrite",
        "queue_ttl_seconds": 86400
      }
    }
  ]
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `127.0.0.1:7700` | 监听地址;对外暴露时放在 TLS 反代后面 |
| `public_url` | 空 | 用户电脑访问 hub 的地址,写进接入命令 `agentuse connect <public_url> <接入码>`;空 = 按平台那次请求的 Host 推断(平台走内网地址时会推断错,所以对外部署要配) |
| `store` | `sqlite` | `sqlite` / `memory` |
| `db` | `<home>/hub.db` | SQLite 文件 |
| `token_key` | 必填 | 运行令牌的 HMAC 密钥 |
| `admin_key` | 空 | 管理接口的 Bearer 密钥;空 = 管理接口关闭 |
| `lease_seconds` | `120` | 运行租约;daemon 每 15 秒续一次 |
| `max_attempts` | `3` | 租约过期后最多领取几次(含第一次),用完判 failed |
| `platforms` | 空 | 见 [models.md#platform](models.md#platform) |

没有 `daemon_keys` 了:设备都是用户跑 `agentuse connect` 兑换接入码自己登记的,密钥哈希存在 hub.db。agent 也不写在 hub.json 里,由平台通过管理接口建。

完整示例:[`examples/hub.json`](../../examples/hub.json)。

## daemon.json

由 `agentuse connect` 写(权限 `0600`,里面有设备密钥),一般不用手改:

```json
{
  "hubs": [
    {
      "url": "https://agentuse.shop.example.com",
      "platform": "shop",
      "device_id": "dv_9e41b07c2a3d",
      "device_key": "dk_…"
    }
  ],
  "transport": "ws"
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `hubs` | 必填 | 这台电脑在各个 hub 上的设备身份,一条一个(hub, 平台, 主人);按 `device_id` 去重。`agentuse daemon start` 每条起一个循环,并行跑 |
| `hubs[].url` / `platform` | | hub 地址和平台 id |
| `hubs[].device_id` / `device_key` | | 兑换接入码时 hub 交回的设备 id 和密钥 |
| `worktrees` | `<home>/worktrees` | worktree 根目录 |
| `transport` | `ws` | `ws`(出站 WebSocket,hub 推)/ `poll`(HTTP 轮询) |

仓库目录、基准分支、驱动不在这里:它们记在 hub 的 [Agent](models.md#agent) 上,daemon 领取运行时随 `agent` 一起拿到。同一台电脑再接一个同主人的 agent,复用已有那条,文件不变。

完整示例:[`examples/daemon.json`](../../examples/daemon.json)。

## 环境变量

| 变量 | 谁读 | 说明 |
|---|---|---|
| `AGENTUSE_HOME` | hub / daemon | 数据目录,默认 `~/.agentuse` |
| `AGENTUSE_URL` | agent 侧命令 | hub 地址,daemon 启动 agent 时注入 |
| `AGENTUSE_TOKEN` | agent 侧命令 | 运行令牌,daemon 启动 agent 时注入 |
| `AGENTUSE_ADMIN_KEY` | `agentuse admin` | 管理密钥(也可用 `-key`) |

## hub.db

每张表把整条记录存成 JSON(`data` 列),另抽出查询要用的列:

| 表 | 列 | 说明 |
|---|---|---|
| `users` | `platform`、`id`(联合 PK)、`data` | [User](models.md#user) |
| `agents` | `id` PK、`platform`、`handle`(`platform, handle` 唯一)、`owner`、`device_id`、`agent_user_id`、`created_at`、`data` | [Agent](models.md#agent) |
| `devices` | `id` PK、`data` | [Device](models.md#device) |
| `connect_codes` | `hash` PK、`agent_id`、`expires_at`、`data` | [ConnectCode](models.md#connectcode) |
| `sessions` | `id` PK、`platform`、`thread`、`agent_id`(`platform, thread, agent_id` 唯一)、`updated_at`(纳秒)、`data` | [Session](models.md#session) |
| `runs` | `seq` 自增 PK(入队顺序)、`id` 唯一、`session_id`、`platform`、`agent_id`、`status`、`created_at`、`lease_until`(纳秒,可空)、`data` | [Run](models.md#run) |

WAL 模式。表结构版本记在 `PRAGMA user_version`(现在是 `2`);启动时版本不对就**删掉所有表重建**——发布前的旧库数据直接丢弃,不做迁移。
