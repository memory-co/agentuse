# agentuse —— 总设计

> **状态:骨架已实施。** hub 的 notify → 唤醒 → 会话 → 排队 → 领取 → 令牌 → 写回全链路可跑(内存存储);daemon 走 HTTP 轮询,WebSocket 与持久化存储留待后续。
>
> 分篇:[hub.md](hub.md)(hub 实例:会话表、运行队列、策略)、[identity.md](identity.md)(身份三段式:进来验平台签名、中间用运行令牌、出去验 hub 签名)、[wake-and-trust.md](wake-and-trust.md)(谁能唤醒 vs 谁的话只是材料)、[daemon.md](daemon.md)(用户电脑上的 daemon:连接、驱动 CLI、现场)、[roadmap.md](roadmap.md)(还没做的事)。

---

## 1. 要解决的问题

评论区是人与人交流的地方。我们想让平台评论里的 `@claude` 直接叫醒**用户自己电脑上**的 agent:它在用户的仓库里开分支改代码,回一条评论,把工单改成「待审核」。

以工单 MUL-42 为例,bob 评论「@claude 退款金额算错了」。一个 @ 背后,平台真正关心的只有两件:**评论存储**和**工单对象与状态**。其余六件与平台无关,却每个平台都要重写一遍:

| # | 事 | agentuse 里谁做 |
|---|---|---|
| 1 | 唤醒判定:是不是在 @ agent;忽略 agent 自己发的评论,防自触发 | hub([wake-and-trust.md](wake-and-trust.md)) |
| 2 | 会话映射:MUL-42 对应 agent 的哪段会话,后续评论续接 | hub([hub.md](hub.md)) |
| 3 | 连接:出站连接穿透 NAT,心跳 | daemon([daemon.md](daemon.md)) |
| 4 | 驱动 CLI:`claude -p --output-format stream-json`,让交互式工具可被程序调用 | daemon |
| 5 | 现场:工作目录 + 分支 `agent/claude/MUL-42` | daemon |
| 6 | 反向令牌:每次运行签发、最长 24 小时的临时令牌 | hub([identity.md](identity.md)) |

**agentuse 就是把这六件抽出来的中间件。** 平台只做「通知」和「接收回调」两件事。

## 2. 为什么不是评论框那样的 iframe

评论框服务(Disqus)做四件事:线程映射、身份(站点 HMAC 签名用户信息,第三方只验签)、存储、治理。agent 版本里这四件都有对应,多出一件「执行」。但直接照搬 iframe 行不通:

1. iframe 里的 agent 拿不到平台数据,改不了工单状态;
2. 对话被劈成平台评论和中间件两处;
3. 中间件公司能看到所有需求、代码片段和结论。

所以 agentuse **不存评论**:评论留在平台,hub 只存会话表与运行队列;agent 的回复由平台写成一条普通评论,作者是平台里一个代表 agent 的应用用户。

## 3. 三方分工

```
平台(工单 / 博客 / 论坛)          hub 实例(hub.alice.dev)                用户电脑
┌──────────────────────┐  notify   ┌──────────────────────────┐  出站连接  ┌──────────────────┐
│ 评论存库后 → hub     │ ────────▶ │ 唤醒判定  会话表  队列    │ ◀──────── │ daemon           │
│                      │  平台签名  │ 策略      运行令牌        │  领取运行  │  worktree 现场   │
│ /agentuse/callback   │ ◀──────── │ 校验令牌 → 回调平台       │ ◀──────── │  驱动 claude -p  │
│ 写评论 / 改状态      │  hub 签名  └──────────────────────────┘  运行令牌  │  agentuse reply  │
└──────────────────────┘                                                   └──────────────────┘
```

- **平台**:评论写入后 `notify`;在 `/agentuse/callback` 收 hub 的事件,把 `reply` 写成评论、把 `status` 写回对象。线程键由平台定:`issue:MUL-42`、`blog:/posts/tmux`、`topic:8812`。
- **hub 实例**:属于用户或团队的实例,像一个小 Redis。保存会话表(线程键 → `s_…`)、运行队列(`r_…`)和按平台的策略;负责唤醒判断、会话查找、排队、签发令牌、转发写回。
- **daemon**:用户电脑上常驻,主动连出 hub,领取运行,在 worktree 里回放现场、驱动 agent CLI。一个 daemon 可以同时服务多个平台。

## 4. 一次 @ 的完整经过

1. bob 在 MUL-42 评论「@claude 退款金额算错了」,平台存库后 `POST /v1/platforms/shop/notify`,用平台密钥签名。
2. hub 验签 → 唤醒判定(提到了 @claude、bob 是 member、不是 agent 自己)→ 找线程 `issue:MUL-42` 的会话,没有就建 `s_…`,分支 `agent/claude/MUL-42` → 排一条运行 `r_…`(queued),把线程里其它评论按策略标成可信 / 不可信材料。
3. alice 电脑上的 daemon 领到 `r_…`(claimed),同时拿到只给这次运行的令牌 `aut_…`。
4. daemon 在 `~/work/shop-api` 上为会话准备 worktree,把令牌注入 `AGENTUSE_TOKEN`,用 `claude -p` 启动(会话已有 agent session id 则 `--resume`)。
5. agent 改代码、推分支,然后 `agentuse reply "…"`、`agentuse status in_review`。
6. hub 校验令牌(线程对得上、动作在范围内、运行还在进行),用 hub 签名回调平台;平台写评论、改状态。
7. agent 退出,daemon `complete`,并把 agent 的 session id 回填到会话,下一条评论续接同一会话。

## 5. 成本与可替换性

- 推理费用由用户自己的 Claude / ChatGPT 订阅承担;
- hub 只是消息路由 + 会话表 + 队列,成本接近一个小 Redis;
- 数据归实例主人:评论在平台,会话与运行记录在用户实例;
- 协议开放:平台代码只依赖「地址 + 密钥 + 协议」,换实例不改平台。

## 6. 代码布局

```
cmd/agentuse/            单一二进制入口
internal/
├── cli/                 命令行:hub start / daemon start / reply / status / activity / run
├── config/              hub.json、daemon.json
├── models/              数据结构(对应 docs/structs)
├── hub/                 hub 业务(hub.go)+ HTTP 接口(server.go)
├── services/
│   ├── wake/            唤醒判定
│   ├── token/           运行令牌
│   └── callback/        hub → 平台回调
├── store/               会话表与运行队列(接口 + 内存实现)
└── daemon/              领取循环、worktree 现场、驱动、提示词
pkg/
├── signature/           平台 ⇄ hub 的 HMAC 签名(公开,其它语言照此实现)
└── sdk/                 平台侧 Go SDK:Notify + Handler
docs/                    designs / structs / apis
```
