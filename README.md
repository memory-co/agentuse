# agentuse

> 像接评论框一样接 Agent:让任意平台评论区里的一个 `@alice-claude`,叫醒 alice 自己电脑上的 agent。

agentuse 是**平台侧的中间件**:平台像起一个 Redis 一样起一个 hub 实例,接几十行代码;平台用户在设置页点一下、在自己电脑上跑一条命令,就能在评论里 @ 自己的 agent。

设想见博客 [《能不能像接评论框一样接 Agent:AgentHub 的设想》](https://twwy.net/blog/agenthub-comment-box-for-agents/)。agentuse 是它的实现,三方分工:

- **平台**(工单 / 博客 / 论坛):评论存库后 `notify` hub;在回调路由上把 agent 的回复写成评论、把状态写回对象;设置页替用户建 agent、展示接入命令。
- **hub 实例**:平台自己管的实例(像平台的 Redis),服务平台上所有用户:唤醒判定、谁的 agent 在哪台电脑、会话表、运行队列、签发运行令牌、转发写回。**不存评论。**
- **daemon**:用户电脑上常驻,`agentuse connect` 一条命令接入;主动连出 hub,只领自己 agent 的运行,在 git worktree 里驱动 `claude -p`。

身份三段式:进来验平台签名,中间用线程级运行令牌,出去验 hub 签名。公开评论区里「谁能唤醒」和「谁的话只是材料」分开配置。

设计文档见 [`docs/designs/`](docs/designs/README.md);数据结构 [`docs/structs/`](docs/structs/README.md);HTTP API 与 CLI [`docs/apis/`](docs/apis/README.md)。

## 状态

评论 → 唤醒 → 排队 → 领取 → 回复 / 改状态 → 回调平台,全链路可跑、有端到端测试。每个用户有自己的 agent(`@alice-claude`),运行只派给 agent 主人绑定的那台电脑;一条评论可以 @ 好几个 agent。平台用签名接口替用户建 agent,用户在自己仓库里跑 `agentuse connect <hub> <接入码>` 就接上,不用手写密钥。用户、agent、设备、会话表和运行队列存在 SQLite(`~/.agentuse/hub.db`);daemon 用出站 WebSocket 接 hub 推送的运行,跑的时候每 15 秒心跳续租,掉线的运行到期自动回收重排;同一线程同一 agent 的运行严格串行;电脑一直不在线的运行排队超时后提示平台。**还没做**:安装脚本、开机自启、环境变量配置 hub、Docker,计划见 [roadmap](docs/designs/roadmap.md)。

## 用起来

完整的接入步骤(部署 hub、平台接入、用户电脑接入、验证与排错)见 [`docs/onboarding.md`](docs/onboarding.md)。最短路径:

```bash
go install github.com/memory-co/agentuse/cmd/agentuse@latest

# 跑 hub 的机器
cp examples/hub.json ~/.agentuse/hub.json        # 改密钥、public_url、平台、策略
agentuse hub start

# 用户电脑:在仓库目录里跑平台设置页给出的命令,再起 daemon
cd ~/work/shop-api
agentuse connect https://agentuse.shop.example.com ac_…   # 写 ~/.agentuse/daemon.json
agentuse daemon start
```

平台侧(Go):

```go
hub := sdk.New(os.Getenv("AGENTUSE_URL"), "shop", os.Getenv("AGENTUSE_SECRET"))
created, _ := hub.CreateAgent(ctx, sdk.NewAgent{Owner: u.ID, OwnerName: u.Name, Handle: "alice-claude", AgentUserID: app.ID})
// 把 created.Connect.Command 展示给 alice
hub.Notify(ctx, sdk.Comment{ID: c.ID, Thread: "issue:MUL-42", Author: sdk.Author{ID: u.ID, Name: u.Name, Role: "member"}, Text: c.Body})
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error { /* 写评论 / 改状态 */ return nil }))
```

实例主人查看:

```bash
AGENTUSE_URL=https://agentuse.shop.example.com AGENTUSE_ADMIN_KEY=… agentuse admin runs -status claimed
```

agent 在运行里:

```bash
agentuse reply "已修复,见分支 agent/alice-claude/MUL-42"
agentuse status in_review
```

## 开发

```bash
make test     # go test ./...
make build    # bin/agentuse
```

## 布局

```
cmd/agentuse/        单一二进制:hub / daemon / agent 侧命令
internal/
├── cli/             命令行
├── config/          hub.json / daemon.json
├── models/          数据结构
├── hub/             hub 业务 + HTTP 接口
├── services/        wake(唤醒判定)/ token(运行令牌)/ callback(回调平台)
├── store/           用户 / agent / 设备、会话表与运行队列(接口 + SQLite / 内存实现,共用一套行为测试)
└── daemon/          接入、领取循环、worktree 现场、CLI 驱动、提示词
pkg/
├── signature/       平台 ⇄ hub 签名
└── sdk/             平台侧 Go SDK
docs/                designs / structs / apis
examples/            配置示例
```

## License

[Apache License 2.0](LICENSE)
