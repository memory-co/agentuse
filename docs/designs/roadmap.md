# 实施计划

已经做了:唤醒判定、会话、SQLite 持久化、WebSocket 推送 + 心跳租约、同一线程串行、过期回收重排、回调独立密钥、管理查询接口、平台侧 Go SDK;**第一阶段账号与归属**全部,以及第二阶段里的接入码兑换和 `agentuse connect` / `agents` / `disconnect`。

2026-10-18 定位改为「平台侧管理的实例」(见 [README.md §2](README.md#2-定位平台侧管理的实例))。按这个定位,下面按顺序做:

## 第一阶段:账号与归属(已完成)

让多用户能用。见 [accounts.md](accounts.md)。

| 事 | 改了哪 |
|---|---|
| User / Agent / Device / ConnectCode 四张表 | `internal/models/accounts.go`、`internal/store`(SQLite + 内存,`storetest` 补了用例);SQLite 表结构版本记在 `PRAGMA user_version`,旧库直接重建 |
| 平台管理 agent 的接口(建 / 查 / 列 / 改 / 删、重发接入码、吊销设备) | `internal/hub/accounts.go`、`server.go`;`pkg/sdk` 加了对应方法和 `*sdk.Error`。签名改为签「方法 + 路径 + 请求体」(`signature.Request`) |
| 唤醒按被 @ 的 agent 判,一条评论可叫醒多个;notify 响应逐个交代 | `internal/services/wake`(`Handles` + `Decide`)、`Hub.Notify` |
| 会话键加 agent;运行带 `agent_id`;领取只给绑定设备;领到的运行带 `agent` | `store.ClaimRun`、`internal/hub` |
| 设备密钥取代 `daemon_keys`(`X-AgentUse-Device`) | `internal/hub/server.go` 认证 |
| 离线排队超时(`queue_ttl_seconds`)与离线提示(回调 `error` 进度) | `Hub.Expire`,和租约回收一起每 10 秒跑 |

原计划在第二阶段的这几件提前一起做了:接入码(一次性、15 分钟、只存哈希)、`POST /v1/connect`、`agentuse connect` / `disconnect`(另加了 `agentuse agents`)、daemon.json 改由 `agentuse connect` 写、hub.json 的 `public_url`。

## 第二阶段:一条命令接入(剩下的)

见 [connect.md](connect.md)。

| 事 | 改哪 |
|---|---|
| `GET /install.sh`、`GET /download/<os>/<arch>` | `internal/hub` |
| `agentuse uninstall` | `internal/cli` |
| launchd / systemd --user 开机自启;`agentuse connect` 接入后自动起 / 重载 daemon,并等它上线再报「已接入」 | 新包 `internal/service` |
| 发布:多平台二进制 + sha256 | GitHub Actions release |

## 第三阶段:运维像起 Redis

| 事 | 说明 |
|---|---|
| 环境变量配置单平台 | 不写 hub.json 也能起 |
| `token_key` 自动生成 | 首次启动写进数据目录 |
| Docker 镜像 | `ghcr.io/memory-co/agentuse` |
| 更新 [onboarding.md](../onboarding.md) | 已按 `agentuse connect` 流程重写;装好安装脚本、环境变量配置之后再简化 |

## 之后

| 事 | 现状 | 方向 |
|---|---|---|
| 现场隔离 | 靠 Claude Code 权限模式(`plan` / `acceptEdits` + 工具白名单) | 只读挂载、无网络、无凭据;公开平台放进容器跑 |
| 回调重试 | 回调失败直接 502 交还给 agent | hub 侧带退避重试,或落一张待投递表 |
| 非对称签名 | 进出各一把对称密钥 | hub 用 Ed25519 签回调,平台只存公钥 |
| 其它驱动 | 只有 Claude Code | Codex app-server 等 |
| daemon 并发 | daemon.json 里每条(每台设备)一个循环,一个循环一次一条;同一设备上的多个 agent 排队 | 一台设备上多个 agent 并行 |
| 进度展示 | activity 原样转发 | 平台侧 SDK 提供「先占位后替换」的帮助函数 |
| 其它语言 SDK | 只有 Go | TypeScript(平台多是 Web) |
