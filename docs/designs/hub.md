# hub 实例

> 代码:[`internal/hub/`](../../internal/hub/)、[`internal/store/`](../../internal/store/)

## 1. 定位:平台侧管理的实例

hub 是**平台自己的实例**,像平台的 Redis:平台运维部署(或买托管版),平台应用只认**地址**和**密钥**;一个 hub 服务这个平台上的所有用户,用户各自把自己的电脑接进来([connect.md](connect.md))。协议固定,实例可换。

hub 保存这些东西:

| 东西 | 是什么 | 结构 |
|---|---|---|
| 会话表 | (线程, agent) → agent 会话 | [Session](../structs/models.md#session) |
| 运行队列 | 每次唤醒一条运行,queued → claimed → succeeded / failed;claimed 带租约 | [Run](../structs/models.md#run) |
| 策略 | 按平台:agent 设置的上限、谁的话可信、排队等多久;写在 hub.json | [Policy](../structs/models.md#policy) |
| 用户 / agent / 设备 / 接入码 | 谁的 agent 叫什么、绑在哪台电脑哪个目录;设备密钥和接入码只存哈希 | [accounts.md](accounts.md) |

**不存评论。** 平台 notify 时可以把线程里最近的评论一起带上(`context`),hub 只把它们挂在这次运行上交给 daemon,不另存一份。

## 2. 会话映射

- 键是 `(platform, thread, agent_id)`:同一工单里 @ 不同人的 agent 是各自独立的会话([accounts.md §4](accounts.md#4-运行只派给主人的设备))。线程键的格式由平台决定,hub 不解析,只在算分支名时去掉第一个 `:` 之前的类型前缀。
- 第一次被唤醒时建会话,分支名 `agent/<handle>/<线程键安全形式>`,如 @alice-claude 在 `issue:MUL-42` → `agent/alice-claude/MUL-42`。
- 第一次运行结束时 daemon 回填 `agent_session_id`(Claude Code 的 session id),之后的运行用 `--resume` 续接。

## 3. 运行队列

```
           ┌──────────── 租约过期且 attempts < max_attempts ────────────┐
           ▼                                                          │
queued ──claim──▶ claimed ──complete──▶ succeeded                     │
                     │  ▲ heartbeat 续租                               │
                     │  └──────┘                                       │
                     ├──complete(err)──▶ failed                        │
                     └──租约过期且 attempts = max_attempts──▶ failed ──┘(否则回到 queued)
```

- **一次 notify 叫醒一个 agent = 一条运行**(一条评论 @ 了两个 agent 就是两条),按入队顺序领取。
- **只派给绑定的设备**:领取时只看「所属 agent 的 `device_id` 是来领的设备」的运行。设备不在线时运行留在队列里等。
- **同一会话严格串行**:领取时跳过「所属会话已有 claimed 运行」的那些。同一线程连着 @ 同一个 agent 两次,第二条要等第一条交回才会被领。不同线程、不同 agent 互不影响。
- **排队超时**:排队超过平台策略 `queue_ttl_seconds`(默认 24 小时)还没被领的,判 failed(`error: queue_ttl: device offline`),并回调平台一条 `error` 进度。和租约回收一起每 10 秒检查一次。
- **租约**:领取时设 `lease_until = now + lease_seconds`(默认 120 秒),daemon 每 15 秒 `heartbeat` 续一次。hub 每 10 秒回收一次过期的:`attempts < max_attempts`(默认 3)的回到 queued,否则判 failed(`error: lease expired`)。
- **一次领取一个身份**:每次领取 `attempts + 1`,运行令牌里带着这次的 attempt。被回收重领之后,上一次的令牌、心跳、交回都是 409——掉线的 daemon 回来也写不进重领后正在跑的运行。
- 只有 claimed 状态、且是这一次领取的令牌能写回;complete 之后令牌即使没过期也写不了(409)。
- 只有领取它的那台设备能 heartbeat / complete 它。
- agent 被删时,它排着和在跑的运行直接判 failed(`error: agent deleted`)。

## 4. 推送

daemon 连着 `/v1/daemon/connect`(WebSocket)空闲等待时,hub 不轮询数据库:新入队、会话空出来、回收、agent 接到(或搬到)一台设备上这几件事都会触发一个变更信号,等着的连接醒来各自尝试领取。领到就推给它,没领到接着等;空闲期间每 15 秒 ping 一次,ping 不通就当连接断了。有推送连接的设备算在线;轮询的设备 45 秒内请求过算在线。见 [daemon.md](daemon.md#1-连接)。

## 5. 存储

`store.Store` 是接口,两个实现跑同一套行为测试([`internal/store/storetest`](../../internal/store/storetest/storetest.go)):

| 实现 | 用途 | 说明 |
|---|---|---|
| SQLite(默认) | 实例 | 一个文件 `~/.agentuse/hub.db`,纯 Go 驱动(modernc.org/sqlite),不需要 cgo。单连接 + `BEGIN IMMEDIATE`,领取的「挑一条再改」在一个事务里完成 |
| Memory | 开发、测试 | 重启即丢 |

hub.json 里 `"store": "sqlite" | "memory"`、`"db": "<路径>"` 选择。SQLite 的表结构版本记在 `PRAGMA user_version`,版本不对时整库重建(发布前,不做迁移)。表结构见 [structs/filesystem.md](../structs/filesystem.md#hubdb)。

## 6. 管理接口

平台运维用 `admin_key` 查看会话和运行(只读):`GET /v1/admin/sessions`、`/v1/admin/runs`、`/v1/admin/runs/{id}`,或 `agentuse admin …`。见 [apis/admin.md](../apis/admin.md)。没配 `admin_key` 时管理接口关闭。
