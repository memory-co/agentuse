# agentuse v2 总体设计

> v1 文档见 [`docs/v1/`](../../v1/README.md),v2 从这里重新开始。

## 一句话

用户电脑上跑一个**守护进程(daemon)**:从 agentuse 服务**领活**,在本地**干活**,干完**向 agentuse 汇报**。daemon 与 agentuse 之间用一条 **WebSocket** 长连接通信。

```
┌──────────────┐   WebSocket(daemon 主动连出)   ┌──────────────┐
│   agentuse   │ ◀──────────────────────────────▶ │    daemon    │
│     服务     │   下发消息 / 回报进度和结果      │ (用户电脑)   │
└──────────────┘                                  └──────┬───────┘
                                                         │ 调用
                                                         ▼
                                                  本地 harness(claude 等)
```

## 角色

| 角色 | 职责 |
|---|---|
| agentuse 服务 | 管会话、排队、把用户的消息派给在线的 daemon、接收结果 |
| daemon | 常驻在用户电脑上,连上服务,领活、执行、汇报 |

## 流程

1. **连接**:daemon 启动后主动连出 agentuse 的 WebSocket 地址并认证，同时声明本机能用哪些 CLI harness(记在 daemon_harnesses 表)。用户电脑在 NAT 后面也没关系,服务不需要能访问用户电脑。
2. **领活**:用户先建一个 **agent**:起个好懂的名字，绑定一个**运行时**(自己的某台 daemon + 上面的某个 harness),以后可以切换运行时(daemon 和 harness 一起换)而 agent 不变。然后用 agent 建一个**会话**(session)并发出第一条消息;用哪个 agent 由接口传的 `agent_id` 指定(agent id 全局唯一，可以是别人的 agent),会话定在 agent 当时的运行时上，之后可以在会话里一直回复。用户发了消息、harness 还没回的会话就是待派的活。daemon 空闲时告诉服务「我可以接活」,服务只挑定在这台 daemon 上、且 harness 在这台开启了的会话，把最新那条消息通过这条连接推过来。
3. **干活**:会话对齐 harness 自己的会话(Claude Code 的 session、Codex 的 thread)。第一条消息新建 harness 会话，之后每条都续接它，所以一个会话固定在第一次跑它的那台 daemon 上。harness 在服务上全局登记：内置的有 Claude Code、Codex;用户自己手搓的 harness 也能注册，只要按约定从 stdin 读输入、往 stdout 写进度和结果([harnesses](../tables/harnesses.md))。内置 harness 放在仓库顶层 `harness/<name>/`,一个 harness 一个目录，命令入口点位写在里面的 `harness.yaml`([harness](harness.md))。
4. **汇报**:执行过程中回报进度，结束后回报结果(成功 / 失败及输出)和 harness 的会话 id,服务把结果记成 harness 回的一条消息。
5. 回到第 2 步，等下一条消息。

## 连接

- 只有一条连接,由 daemon 发起;所有消息双向都走它。
- 连接断开后 daemon 自动退避重连。
- 消息派出后如果 daemon 掉线、迟迟没有汇报，服务把它收回重新排队(还是给同一台 daemon)。

## 存储

agentuse 服务用 **SQLite** 存数据，一个文件、不依赖外部数据库，部署简单。表设计见 [`docs/v2/tables/`](../tables/),一张表一个文件:

| 表 | 存什么 |
|---|---|
| [users](../tables/users.md) | 用户，分 admin 和普通用户;daemon、agent、自定义 harness、会话都归属到用户 |
| [harnesses](../tables/harnesses.md) | 全局登记的 CLI harness,包括内置的和用户自定义的 harness |
| [agents](../tables/agents.md) | 用户起名字的 agent,绑定一个运行时(daemon + harness),可以切换 |
| [daemons](../tables/daemons.md) | 登记过的 daemon 及其在线状态 |
| [daemon_harnesses](../tables/daemon_harnesses.md) | 每个 daemon 能用哪些 CLI harness |
| [works](../tables/works.md) | 工作：协调者会话 + 线程会话 + 共用说明，照 Claude Projects 的样子 |
| [work_tokens](../tables/work_tokens.md) | 协调者跑的时候用来开线程的工作令牌 |
| [sessions](../tables/sessions.md) | 会话，用某个 agent 开，对齐 harness 的 session,多轮对话 |
| [messages](../tables/messages.md) | 会话里的消息，用户发的和 harness 回的 |
| [session_events](../tables/session_events.md) | daemon 回报的进度和结果，按时间追加 |

daemon 本地不存状态，重启后重新连上服务即可(harness 自己的会话文件照常留在本机，续接要用)。

## 接入与消息

一台电脑怎么接进来(页面添加 daemon → 跑 `setup.sh` 注册 → 选择开启哪些 harness)、认证方式、WebSocket 消息格式、派活规则，见 [connect](connect.md);全部接口见 [`docs/v2/apis/`](../apis/README.md)。

## 嵌入到网站

agentuse 也能像 Redis 一样当中间件嵌进别的网站：网站后端带着服务密钥和自己的 user id 调用它，网站用户不直接接触 agentuse。见 [embedding](embedding.md)。

## 网页

服务自带一个网页(源码在仓库顶层 `frontend/`,React + shadcn/ui,构建产物嵌进 agentuse-server):

- **机器**:添加 daemon、复制安装命令、看在线状态、为每台 daemon 开关 harness。
- **会话**:选 agent、写 prompt 开一个会话，像聊天一样看 harness 的进度和回复，在下面接着发消息。
- **工作**:会话列表上方切换「会话 / 工作」。一个工作里用户跟协调者对话，协调者开线程去干具体的活，共用一份说明，见 [work](work.md)。
- **Agent**:建 agent、给它起名字、选 / 切换运行时(哪台 daemon 上的哪个 harness)。
- **Harness**:看全部 harness,注册自定义 harness。
- **用户**(仅 admin):建用户，拿到对方的登录链接。

## 会话附件

session 下增加 attachment：用户上传输入文件，daemon 下载到会话目录交给 harness，harness 用 agentuse 客户端上传产物，用户从会话下载。文件内容走 HTTP，元数据存 SQLite；完整设计见 [attachment](attachment.md)，接口见 [attachments](../apis/attachments.md)，表见 [attachments](../tables/attachments.md)。

## Session 中 @ 多个 agent（待实现）

会话保存所有 agent 共用的消息和附件，每条消息可 @ 多个 agent；run 记录本次执行者、运行时、租约和结果，每次执行读取同一 session 的当前内容，不保存按 agent 的上下文。方案、权限与兼容迁移见 [session-mentions](session-mentions.md)。
