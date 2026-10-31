# workService：工作、协调与线程汇总

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

workService 将多个 session 组织成一项工作。一个 work 有一个协调 session、一个协调 agent 和若干线程 session。它复用 sessionService/runService 的对话与任务模型，不引入另一种执行实体。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 创建 work | 通过 agentService 校验协调 agent，通过 sessionService 创建协调 session；在同一事务建立关联 |
| 创建线程 | 以工作范围权限调用 sessionService 创建关联 work_id 的 session |
| 修改配置 | 维护名称、说明和协调 agent；忙碌时拒绝不安全的执行选择变更 |
| 汇总结果 | 在最终回复已由 sessionService 保存后，检查原消息给所有目标的 run_prompts 是否完成（沿 superseded_by 跟踪后续副本）；将汇总作为消息发到协调 session |
| 删除 work | 检查相关任务空闲，再经 sessionService 删除所属会话及 work 数据 |

汇总使用 run_notifications 去重：记录“该线程消息已汇报”与向协调 session 发汇总消息，必须在同一事务完成。不能只收到 run 结束事件就汇总，因为最终回复可能还没消费入库；需要等回复落库通知，并能通过持久化消息/run_prompts 扫描补偿。

## 工作令牌

协调 agent 的 run 领取时，工作授权参与领取事务，由 workService 签发限定 work/run/attempt 的令牌；明文只交给对应 client。每次使用时同时检查 run 当前状态、attempt 和租约。run 结束、重排或设备凭据撤销时协调撤销，不把令牌当作长期用户身份。

`coordinator_id` 是协调 session；`sessions.work_id` 是会话归属。两者仅以标识关联，由 workService 在创建/删除事务里保证一致，不声明外键。

## 表结构

### works

工作名称、说明、所有者、协调 session 和协调 agent。协调 agent 被删除时，由 agentService 协调 workService 清空 coordinator_agent_id；重新配置之前不能继续派协调任务。

```sql
CREATE TABLE works (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    coordinator_id TEXT NOT NULL UNIQUE,
    coordinator_agent_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX works_user
    ON works(user_id, updated_at);
```

### work_run_tokens

令牌只存哈希，绑定一个 work/run/attempt。表中没有 expires_at，实际有效期由当前 run 的状态和 lease_until 决定；校验时不能只查到 token_hash 就放行。

```sql
CREATE TABLE work_run_tokens (
    token_hash TEXT PRIMARY KEY,
    work_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL
);
```

### run_notifications

已汇总的线程 user message_id。按消息而不是 run 去重，因为一条消息可以有多个目标，也可能经历插话回退后的后续 run。

```sql
CREATE TABLE run_notifications (
    message_id INTEGER PRIMARY KEY
);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [harnessService](harness.md) · [sessionService](session.md) · [runService](run.md) · [fileService](file.md)
