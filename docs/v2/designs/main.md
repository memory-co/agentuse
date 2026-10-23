# agentuse v2 总体设计

> v1 文档见 [`docs/v1/`](../../v1/README.md),v2 从这里重新开始。

## 一句话

用户电脑上跑一个**守护进程(daemon)**:从 agentuse 服务**领任务**,在本地**干活**,干完**向 agentuse 汇报**。daemon 与 agentuse 之间用一条 **WebSocket** 长连接通信。

```
┌──────────────┐   WebSocket(daemon 主动连出)   ┌──────────────┐
│   agentuse   │ ◀──────────────────────────────▶ │    daemon    │
│     服务     │   下发任务 / 回报进度和结果      │ (用户电脑)   │
└──────────────┘                                  └──────┬───────┘
                                                         │ 调用
                                                         ▼
                                                  本地 agent(claude 等)
```

## 角色

| 角色 | 职责 |
|---|---|
| agentuse 服务 | 收任务、排队、把任务派给在线的 daemon、接收结果 |
| daemon | 常驻在用户电脑上,连上服务,领任务、执行、汇报 |

## 流程

1. **连接**:daemon 启动后主动连出 agentuse 的 WebSocket 地址并认证，同时声明本机能用哪些 CLI agent(记在 daemon_agents 表)。用户电脑在 NAT 后面也没关系,服务不需要能访问用户电脑。
2. **领任务**:daemon 空闲时告诉服务「我可以接活」,服务只挑同一用户创建、且 agent 是这台 daemon 声明过的任务，通过这条连接推过来。
3. **干活**:每个任务都明确指定用哪个 CLI agent,daemon 在本地调用对应的 CLI 执行任务。agent 在服务上全局登记：内置的有 Claude Code、Codex;用户自己手搓的 harness 也能注册，只要按约定从 stdin 读任务、往 stdout 写进度和结果([agents](../tables/agents.md))。
4. **汇报**:执行过程中回报进度,结束后回报结果(成功 / 失败及输出),服务据此更新任务状态。
5. 回到第 2 步,等下一个任务。

## 连接

- 只有一条连接,由 daemon 发起;所有消息双向都走它。
- 连接断开后 daemon 自动退避重连。
- 任务派出后如果 daemon 掉线、迟迟没有汇报,服务把任务收回重新排队。

## 存储

agentuse 服务用 **SQLite** 存数据，一个文件、不依赖外部数据库，部署简单。表设计见 [`docs/v2/tables/`](../tables/),一张表一个文件:

| 表 | 存什么 |
|---|---|
| [users](../tables/users.md) | 用户，分 admin 和普通用户;daemon、自定义 agent、任务都归属到用户 |
| [agents](../tables/agents.md) | 全局登记的 CLI agent,包括内置的和用户自定义的 harness |
| [daemons](../tables/daemons.md) | 登记过的 daemon 及其在线状态 |
| [daemon_agents](../tables/daemon_agents.md) | 每个 daemon 能用哪些 CLI agent |
| [tasks](../tables/tasks.md) | 任务本身和它当前的状态 |
| [task_events](../tables/task_events.md) | daemon 回报的进度和结果，按时间追加 |

daemon 本地不存状态，重启后重新连上服务即可。

## 待定

后续文档再细化：认证方式、消息格式、并发与重试。
