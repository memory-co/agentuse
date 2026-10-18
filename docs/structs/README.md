# Structs

agentuse 的数据结构:对象、字段语义、ID、配置与磁盘布局。**是什么**;机制见 [`../designs/`](../designs/README.md),HTTP 契约见 [`../apis/`](../apis/README.md)。Go 定义在 [`internal/models/`](../../internal/models/)。

## 对象清单

| 对象 | 是什么 | 存哪 | 文档 |
|---|---|---|---|
| Platform | 接入 hub 的一个平台:id、密钥、回调地址、策略 | `hub.json` | [models.md](models.md#platform) |
| Policy | 按平台的策略:每个 agent 设置的上限、谁可信、排队多久 | `hub.json`(挂在 Platform 下) | [models.md](models.md#policy) |
| User | 平台里的一个人:id 和显示名 | `users` 表 | [models.md](models.md#user) |
| Agent | 一个人的一个 agent:handle、主人、绑定的设备和目录、唤醒规则、权限 | `agents` 表 | [models.md](models.md#agent) |
| Device | 一个人接进来的一台电脑,存设备密钥的哈希 | `devices` 表 | [models.md](models.md#device) |
| ConnectCode | 一次性接入码,存哈希 | `connect_codes` 表 | [models.md](models.md#connectcode) |
| Comment / Author | 平台 notify 进来的一条评论及其作者;**hub 不存** | 只挂在 Run 上 | [models.md](models.md#comment) |
| Material | 线程里的其它评论,标了可不可信 | 挂在 Run 上 | [models.md](models.md#material) |
| Session | (线程, agent) → agent 会话,带现场分支 | 会话表 | [models.md](models.md#session) |
| Run | 一次唤醒对应的一次运行 | 运行队列 | [models.md](models.md#run) |
| RunToken | 只给一次运行的令牌,不落盘 | 无(自包含签名) | [models.md](models.md#runtoken) |
| Event / Activity | hub 回调平台的事件 | 不落盘 | [models.md](models.md#event) |

配置文件与目录:[filesystem.md](filesystem.md)。

## ID

| 对象 | 形态 | 示例 |
|---|---|---|
| Agent | `ag_<12 hex>` | `ag_5d02e8a1c4b7` |
| Device | `dv_<12 hex>` | `dv_9e41b07c2a3d` |
| 设备密钥 | `dk_<22 位 base64url>`(128 位随机) | `dk_Zq3…` |
| 接入码 | `ac_<22 位 base64url>`(128 位随机) | `ac_7Hq2mX9pLw…` |
| Session | `s_<12 hex>` | `s_31a0c9e2b7f4` |
| Run | `r_<12 hex>` | `r_1180a4c3d2e1` |
| RunToken | `aut_<base64url claims>.<base64url sig>` | `aut_eyJydW4iOi….Qx9…` |
| 线程键 | 平台定,`<类型>:<键>` 是惯例 | `issue:MUL-42`、`blog:/posts/tmux`、`topic:8812` |
| 分支 | `agent/<handle>/<线程键安全形式>` | `agent/alice-claude/MUL-42` |

前缀 = 类型。线程键 hub 不解析,只在算分支名时去掉第一个 `:` 之前的部分,把非 `[A-Za-z0-9._-]` 的字符折成 `-`。

## 引用关系

```
Platform ──(policy)──▶ Policy
User (platform, id) ◀──(owner)── Agent ──(device_id)──▶ Device ──(owner)──▶ User
ConnectCode ──(agent_id)──▶ Agent
Session (platform, thread, agent_id) ◀──(session_id)── Run ──(agent_id)──▶ Agent
                                                       ├──(trigger)──▶ Comment
                                                       └──(context)──▶ Material[] ──▶ Comment
RunToken ──(run)──▶ Run
Event ──(run_id, in_reply_to)──▶ Run, 叫醒评论
```

全部是裸 id 引用,没有外键;评论 id 指向平台那边的对象,hub 不校验。
