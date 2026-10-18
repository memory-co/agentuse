# agentuse —— 总设计

> **状态:定位已改,代码待跟进。** 2026-10-18 定位从「用户自己的实例」改为「**平台侧管理的实例**」(像平台自己的 Redis),并补上账号与 agent 归属、一条命令接入。唤醒、会话、运行队列、租约、WebSocket 推送、运行令牌、回调这些已实施的部分不变;账号、归属、接入码、安装脚本还没写,计划见 [roadmap.md](roadmap.md)。在代码跟上之前,[onboarding.md](../onboarding.md) 描述的是现有实现的接入方式。
>
> 分篇:[accounts.md](accounts.md)(**新**:用户、agent、设备三个对象,运行只派给 agent 主人的电脑)、[connect.md](connect.md)(**新**:用户一条命令接入:接入码、安装脚本、自动登记)、[hub.md](hub.md)(hub 实例:会话表、运行队列、租约、推送、存储)、[identity.md](identity.md)(身份三段式)、[wake-and-trust.md](wake-and-trust.md)(谁能唤醒谁的 agent、谁的话只是材料)、[daemon.md](daemon.md)(用户电脑上的 daemon:连接、驱动 CLI、现场)、[roadmap.md](roadmap.md)(实施计划)。

---

## 1. 要解决的问题

评论区是人与人交流的地方。我们想让平台评论里的 `@alice-claude` 直接叫醒 **alice 自己电脑上**的 agent:它在 alice 的仓库里开分支改代码,回一条评论,把工单改成「待审核」。

一个 @ 背后,平台真正关心的只有两件:**评论存储**和**工单对象与状态**。其余六件与平台无关,却每个平台都要重写一遍:

| # | 事 | agentuse 里谁做 |
|---|---|---|
| 1 | 唤醒判定:是不是在 @ 某个 agent;忽略 agent 自己发的评论 | hub([wake-and-trust.md](wake-and-trust.md)) |
| 2 | 会话映射:MUL-42 对应这个 agent 的哪段会话,后续评论续接 | hub([hub.md](hub.md)) |
| 3 | 连接:用户电脑出站连接穿透 NAT,心跳 | daemon([daemon.md](daemon.md)) |
| 4 | 驱动 CLI:`claude -p --output-format stream-json` | daemon |
| 5 | 现场:工作目录 + 分支 `agent/alice-claude/MUL-42` | daemon |
| 6 | 反向令牌:每次运行签发、最长 24 小时的临时令牌 | hub([identity.md](identity.md)) |

**agentuse 就是把这六件抽出来的中间件**,再加上第七件每个平台都得做、又最容易做砸的事:**让用户把自己的电脑接进来**。平台只做「通知」「接收回调」和「在设置页放一个接入按钮」。

## 2. 定位:平台侧管理的实例

博客里的类比是 Redis:**平台自己起一个 Redis 实例,应用只认地址和密钥**。agentuse 的 hub 就是这样一个实例:

| | Redis | agentuse hub |
|---|---|---|
| 谁部署、谁运维 | 平台的运维 | 平台的运维(或买托管版) |
| 应用怎么用 | 地址 + 密码 + 协议 | 地址 + 密钥 + 协议(notify / 回调) |
| 存什么 | 应用的缓存、队列 | 会话表、运行队列、谁的 agent 在哪台电脑 |
| 不存什么 | 应用的主数据 | **评论**(留在平台) |
| 可替换 | 换 Valkey 不改应用 | 自部署 / 托管版同一套代码和协议,换实例不改平台 |

和多说的区别也在这里。多说是一家公司托管的免费服务,关停后数据跟着没了;agentuse 的 hub 是**平台自己的实例**,数据在平台自己手里,代码和协议开源,不依赖任何一家公司活着。

于是有三类人,各自的接入成本:

| 谁 | 做什么 | 成本 |
|---|---|---|
| **平台运维** | 起一个 hub 实例,配上平台密钥和回调地址 | 一次,和起一个 Redis 差不多 |
| **平台开发者** | 评论存库后 notify;回调里写评论、改状态;设置页加「接入我的 agent」 | 几十行,用 SDK |
| **平台用户** | 在设置页点一下,复制一条命令到自己电脑的终端里跑 | **一条命令**,不碰配置文件,不找任何人要密钥 |

## 3. 为什么不是评论框那样的 iframe

评论框服务(Disqus)做四件事:线程映射、身份(站点 HMAC 签名用户信息,第三方只验签)、存储、治理。agent 版本里这四件都有对应,多出「执行」和「接入」。照搬 iframe 行不通:

1. iframe 里的 agent 拿不到平台数据,改不了工单状态;
2. 对话被劈成平台评论和中间件两处;
3. 中间件能看到所有需求、代码片段和结论。

所以 agentuse **不存评论**:评论留在平台,agent 的回复由平台写成一条普通评论,作者是平台里代表这个 agent 的应用用户。

## 4. 三方分工

```
平台(工单 / 博客 / 论坛)            hub(平台侧的实例)                      各个用户的电脑
┌────────────────────────┐ notify   ┌────────────────────────────┐ 出站连接 ┌─────────────────────┐
│ 评论存库后 → hub       │ ───────▶ │ 唤醒判定:@ 的是谁的 agent  │ ◀─────── │ alice 的 daemon     │
│ 设置页:接入我的 agent  │ 建 agent │ 用户 / agent / 设备        │ 领取运行 │  只领 alice 的运行  │
│  → 拿到一条安装命令    │ ───────▶ │ 会话表 运行队列 租约       │          ├─────────────────────┤
│ /agentuse/callback     │ ◀─────── │ 运行令牌 → 回调平台        │ ◀─────── │ bob 的 daemon       │
│ 写评论 / 改状态        │ hub 签名 └────────────────────────────┘          │  只领 bob 的运行    │
└────────────────────────┘                                                └─────────────────────┘
```

- **平台**:评论写入后 `notify`;在回调里把 `reply` 写成 agent 应用用户的评论、把 `status` 写回对象;设置页替用户向 hub 建 agent,把 hub 交回的安装命令展示给用户。
- **hub**:平台侧的实例。知道每个 agent 属于谁、在哪台电脑上([accounts.md](accounts.md));判唤醒、找会话、排队、签令牌、转发写回;发安装脚本、兑换接入码([connect.md](connect.md))。
- **daemon**:用户电脑上常驻,由安装命令自动装好、登记、设为开机自启;主动连出 hub,**只领自己主人的 agent 的运行**,在 worktree 里驱动 agent CLI。

## 5. 一次 @ 的完整经过

前提:alice 已经在平台设置页建了 agent `alice-claude`,并在自己电脑的 `~/work/shop-api` 里跑过那条安装命令([connect.md](connect.md))。

1. bob 在 MUL-42 评论「@alice-claude 退款金额算错了」,平台存库后 notify hub,用平台密钥签名。
2. hub 验签,在这个平台的 agent 里找到被 @ 的 `alice-claude`,按它的唤醒规则判断 bob 能不能叫它([wake-and-trust.md](wake-and-trust.md))。能叫的话,找 `(issue:MUL-42, alice-claude)` 的会话(没有就建,分支 `agent/alice-claude/MUL-42`),排一条**属于 alice-claude** 的运行。
3. alice 电脑上的 daemon 连着 hub 等着;hub 只把这条运行推给 alice-claude 绑定的那台设备,附上运行令牌。bob 的电脑看不到它。
4. daemon 在 `~/work/shop-api` 上为会话准备 worktree,注入令牌,用 `claude -p` 启动(有旧会话就 `--resume`)。
5. agent 改代码、推分支,然后 `agentuse reply "…"`、`agentuse status in_review`;daemon 每 15 秒心跳续租。
6. hub 校验令牌,用 hub 签名回调平台;平台以 `alice-claude` 的名义写评论、改状态。
7. agent 退出,daemon 交回结果;同一线程里下一次 @alice-claude 续接同一段会话。

alice 的电脑不在线时,运行留在队列里等它上线;notify 的响应会告诉平台「alice-claude 目前离线」,平台可以在评论下提示。

## 6. 成本与可替换性

- 推理费用由每个用户自己的 Claude / ChatGPT 订阅承担,平台不付;
- hub 只是消息路由 + 几张小表 + 队列,成本接近一个小 Redis;
- 数据在平台自己手里:评论在平台主库,会话与运行记录在平台自己的 hub 实例;
- 协议开放:自部署和托管版是同一套代码,平台代码只依赖「地址 + 密钥 + 协议」。

## 7. 代码布局

```
cmd/agentuse/            单一二进制入口
internal/
├── cli/                 命令行:hub start / daemon start / reply / status / activity / run
├── config/              hub.json、daemon.json
├── models/              数据结构(对应 docs/structs)
├── hub/                 hub 业务(hub.go)+ HTTP 接口(server.go)+ daemon 推送连接(connect.go)
├── services/
│   ├── wake/            唤醒判定
│   ├── token/           运行令牌
│   └── callback/        hub → 平台回调
├── store/               会话表与运行队列(接口 + SQLite / 内存实现;storetest 是共用的行为测试)
└── daemon/              领取循环(WebSocket / 轮询)、心跳、worktree 现场、驱动、提示词
pkg/
├── signature/           平台 ⇄ hub 的 HMAC 签名(公开,其它语言照此实现)
└── sdk/                 平台侧 Go SDK:Notify + Handler
docs/                    designs / structs / apis
```
