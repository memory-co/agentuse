# runService：任务、消息分配与执行生命周期

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。

## 职责

runService 管一次业务执行任务。一个 run 属于一个 session，确定 agent、device、harness；关联多条任务消息。每次重新领取通过 attempt 区分实际执行身份，不另建一条 run 代表网络重试。

## 与其他 Service 的关系

- sessionService 在发送事务中调用 runService，为每个目标建立分配；runService 不反向调用 sessionService。
- agentService 提供角色及执行选择，deviceService 提供设备能力，harnessService 提供固定版本配置。
- workService 提供工作说明和执行范围的授权；与 run 同寿命的工作令牌由 workService 管理。
- runService 持久化结果事件后通知 sessionService 消费；回复入库后由 sessionService 调用事务内结果绑定操作。

## Message 与 Run 为什么需要关联表

`message_deliveries` 表达任务输入的 N:M，也保存投递状态。一条消息给两个 agent 对应两条 delivery；同 agent 已有活跃 run 时，补充消息增加 delivery，不必新建 run。

run.message_id 只表示主任务，完整输入集合通过 delivery 查找。未消费补充任务转入新 run 时，delivery ID 保留而 run_id 可变；因此它不是不可变投递审计日志。

## 生命周期

| 操作 | 规则 |
|---|---|
| 建立分配 | 同 session + agent 最多一个 pending/running/waiting run；无活跃任务则建新 run，否则追加 steer delivery |
| 领取 | 按 device 找 pending run；校验能力，生成 attempt，设租约，准备任务消息和共享上下文 |
| 插话 | 仅对当前执行推送可接受的输入；执行端确认后记 delivered，不因 HTTP 保存成功就确认消费 |
| 提问/回答 | 问题绑定 run/attempt；答案先保存，执行端确认消费后再推进问题与 run 状态 |
| 结束 | 写终态及持久化结果事件；更新 delivery、撤销执行凭据，通知 sessionService 保存回复 |
| 超时重试 | 同 run 重排 pending，重新领取时换 attempt；拒绝旧执行的新事件 |
| 未消费输入 | 正常结束时可转入后续新 run；取消则终止分配，不自动继续 |

Run 状态是 pending/running/waiting/succeeded/failed/cancelled；delivery 是 pending/delivered/succeeded/failed/cancelled。等待某条消息全部处理完，需等其 delivery 全部终结，不能只等最初返回的某个 run。

所有终态来源（包括取消、运行时不可用、设备删除）都必须写可恢复的终态事件。run_events 同时保存执行端事件和 server 生成的生命周期事件；server 合成事件在事务内分配不冲突的 event_seq。处理终态与后续输入在统一事务内完成，网络通知在提交后进行。

## v3 执行配置快照

session_runs 增加 harness_revision、harness_config_yaml。建立 run 时复制 harnessService 返回的定义，client 按快照执行。更新同名配置不会改变排队/重试任务；创建后续新 run 时重新选择配置。

## 表结构

### session_runs

一次执行任务。`message_id` 是主任务，`result_message_id` 是结果回复；`agent_key/name/user_id` 是历史身份；device_id 可因设备删除置空。attempt_id/lease_until 表示当前执行租约，context_seq 表示上下文读取边界。配置快照是 v3 新增字段。

```sql
CREATE TABLE session_runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_key TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    agent_user_id TEXT NOT NULL REFERENCES users(id),
    device_id TEXT REFERENCES devices(id) ON DELETE SET NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    harness_config_yaml TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_id TEXT NOT NULL DEFAULT '',
    lease_until INTEGER,
    context_seq INTEGER NOT NULL DEFAULT 0,
    result_message_id INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX runs_active_agent
    ON session_runs(session_id,agent_id)
    WHERE agent_id IS NOT NULL AND status IN ('pending','running','waiting');
CREATE INDEX runs_status
    ON session_runs(status,created_at);
CREATE INDEX runs_dispatch
    ON session_runs(device_id,status,created_at);
```

### message_deliveries

一条 user 消息给一个目标 agent 的分配，唯一键为 message_id + agent_key。kind 为 new_run/steer；prepared_attempt 表示哪个执行尝试已准备该输入。run_id/kind 可以随未消费任务回退改变。

```sql
CREATE TABLE message_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_key TEXT NOT NULL,
    run_id TEXT REFERENCES session_runs(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    prepared_attempt TEXT NOT NULL DEFAULT '',
    UNIQUE(message_id,agent_key)
);
```

### run_events

进度、计划、交互确认和终态事件。payload 为 JSON，唯一键保证同 run/attempt/序号重复上报不重复生效。终态 payload 保留完整结果，供结果消费恢复。device_id 是事件来源身份快照，不声明设备外键，便于设备删除后保留历史。

```sql
CREATE TABLE run_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE,
    attempt_id TEXT NOT NULL,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    event_seq INTEGER NOT NULL,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(run_id,attempt_id,event_seq)
);
```

### run_questions

某 attempt 中 tool_use_id 对应的问题。questions 是 JSON 数组，answers 是 JSON map；answered_by 记录用户。保存答案不等于执行端已消费，status 由确认事件推进。

```sql
CREATE TABLE run_questions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE,
    attempt_id TEXT NOT NULL,
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    tool_use_id TEXT NOT NULL,
    questions TEXT NOT NULL,
    answers TEXT,
    answered_by TEXT REFERENCES users(id),
    status TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(run_id,attempt_id,tool_use_id)
);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [harnessService](harness.md) · [sessionService](session.md) · [attachmentService](attachment.md) · [workService](work.md)
