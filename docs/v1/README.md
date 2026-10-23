# agentuse 文档

agentuse 是 [AgentHub 设想](https://twwy.net/blog/agenthub-comment-box-for-agents/)的实现:让任意平台评论区里的一个 @,触达用户自己电脑上的 agent,像接一个评论框一样接入。

**定位**:agentuse 是平台侧的中间件,hub 像平台自己管的 Redis 实例;平台用户一条命令把自己的电脑接进来。见 [designs/README.md §2](designs/README.md#2-定位平台侧管理的实例)。

**接入**:[onboarding.md](onboarding.md) 一步步走到第一个 @claude(按现有实现写;一条命令接入实施后会按 [designs/connect.md](designs/connect.md) 重写)。

三个目录各管一件事:

| 目录 | 回答什么 | 入口 |
|---|---|---|
| [`designs/`](designs/README.md) | **为什么、怎么运转**:定位、三方分工、身份三段式、唤醒与信任、现场 | [designs/README.md](designs/README.md) |
| [`structs/`](structs/README.md) | **是什么**:对象、字段、ID、配置文件与磁盘布局 | [structs/README.md](structs/README.md) |
| [`apis/`](apis/README.md) | **怎么调**:平台 ⇄ hub、daemon ⇄ hub、agent ⇄ hub 的 HTTP 契约,以及 CLI | [apis/README.md](apis/README.md) |

读法:先 designs/README.md 看全貌,再按需要查 structs 和 apis。代码与文档冲突时以代码为准,并修文档。
