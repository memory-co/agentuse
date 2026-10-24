# agentuse v2 总体设计

> v1 文档见 [`docs/v1/`](../../v1/README.md),v2 从这里重新开始。

## 一句话

用户电脑上跑一个**守护进程(daemon)**:从 agentuse 服务**领活**,在本地**干活**,干完**向 agentuse 汇报**。daemon 与 agentuse 之间用一条 **WebSocket** 长连接通信。

```
┌──────────────┐   WebSocket(daemon 主动连出)   ┌──────────────┐
│   agentuse   │ ◀──────────────────────────────▶ │    daemon    │
│     服务     │   下发一轮 / 回报进度和结果      │ (用户电脑)   │
└──────────────┘                                  └──────┬───────┘
                                                         │ 调用
                                                         ▼
                                                  本地 agent(claude 等)
```

## 角色

| 角色 | 职责 |
|---|---|
| agentuse 服务 | 管会话、排队、把每一轮派给在线的 daemon、接收结果 |
| daemon | 常驻在用户电脑上,连上服务,领活、执行、汇报 |

## 流程

1. **连接**:daemon 启动后主动连出 agentuse 的 WebSocket 地址并认证，同时声明本机能用哪些 CLI agent(记在 daemon_agents 表)。用户电脑在 NAT 后面也没关系,服务不需要能访问用户电脑。
2. **领活**:用户建一个**会话**(session)并发出第一条消息，之后可以在会话里一直回复。每条消息是会话里的一**轮**(turn),派给 daemon 的就是一轮。daemon 空闲时告诉服务「我可以接活」,服务只挑同一用户、且 agent 是这台 daemon 开启了的轮，通过这条连接推过来。
3. **干活**:会话对齐 harness 自己的会话(Claude Code 的 session、Codex 的 thread)。第一轮新建 harness 会话，之后每一轮都续接它，所以一个会话固定在第一次跑它的那台 daemon 上。agent 在服务上全局登记：内置的有 Claude Code、Codex;用户自己手搓的 harness 也能注册，只要按约定从 stdin 读输入、往 stdout 写进度和结果([agents](../tables/agents.md))。
4. **汇报**:执行过程中回报进度，结束后回报结果(成功 / 失败及输出)和 harness 的会话 id,服务据此更新这一轮的状态。
5. 回到第 2 步，等下一轮。

## 连接

- 只有一条连接,由 daemon 发起;所有消息双向都走它。
- 连接断开后 daemon 自动退避重连。
- 一轮派出后如果 daemon 掉线、迟迟没有汇报，服务把它收回重新排队(还是给同一台 daemon)。

## 存储

agentuse 服务用 **SQLite** 存数据，一个文件、不依赖外部数据库，部署简单。表设计见 [`docs/v2/tables/`](../tables/),一张表一个文件:

| 表 | 存什么 |
|---|---|
| [users](../tables/users.md) | 用户，分 admin 和普通用户;daemon、自定义 agent、会话都归属到用户 |
| [agents](../tables/agents.md) | 全局登记的 CLI agent,包括内置的和用户自定义的 harness |
| [daemons](../tables/daemons.md) | 登记过的 daemon 及其在线状态 |
| [daemon_agents](../tables/daemon_agents.md) | 每个 daemon 能用哪些 CLI agent |
| [sessions](../tables/sessions.md) | 会话，对齐 harness 的 session,多轮对话 |
| [turns](../tables/turns.md) | 会话里的每一轮：输入、状态、结果 |
| [turn_events](../tables/turn_events.md) | daemon 对每一轮回报的进度和结果，按时间追加 |

daemon 本地不存状态，重启后重新连上服务即可(harness 自己的会话文件照常留在本机，续接要用)。

## 接入与消息

一台电脑怎么接进来(页面添加 daemon → 跑 `setup.sh` 注册 → 选择开启哪些 agent)、认证方式、WebSocket 消息格式、派活规则，见 [connect](connect.md);全部接口见 [`docs/v2/apis/`](../apis/README.md)。

## 网页

服务自带一个简单的网页：

- **机器**:添加 daemon、复制安装命令、看在线状态、为每台 daemon 开关 agent。
- **会话**:选 agent、写 prompt 开一个会话，看每一轮的进度和结果，在下面接着回复。
- **Agent**:看全部 agent,注册自定义 harness。
- **用户**(仅 admin):建用户，拿到对方的登录链接。
