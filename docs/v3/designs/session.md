# sessionService：共享对话与消息编排

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。

## 职责

sessionService 管理 session、message 和共享历史，并编排消息分配。一个 session 包含若干 message 和若干 run；message 与 run 的任务输入关系是 **N:M**，由 runService 的 run_prompts 连接。

- 创建/读取/删除会话，维护标题、归属和共享历史。
- 接收消息，调用 runService 为目标 agent 分配任务；对话原文只保存一次，每个目标 run 另存一份 run_prompts。
- 消费执行结果事件，把最终回复写为 assistant message，并记录作者和被回复的 message。
- 读取会话时组合对话、run、run_prompts、事件和问题的统一快照。

![Session、消息、投递与执行的关系](../../images/session-run.svg)

## 发送和回复

发送链路是 `controller → sessionService → runService`：先检查权限与请求幂等，在一个事务中写一条 message 和全部目标 run 的 run_prompts，最后提交并通知设备。任一目标失败则整体回滚。空目标允许只保存一条备注。

```text
M1 @coder @reviewer → P1 / R1，P2 / R2
M2 @coder          → P3 / R1（R1 仍活跃，补充输入）
```

M1 对应两个 run，R1 处理两条消息；session 不绑定 agent，没有 session_participant 或每个 agent 独立的上下文。

runService 先持久化终态与结果事件；sessionService 消费后，在同一事务写带作者和 reply_to_message_id 的 assistant message、更新 runs.result_message_id 和会话状态投影。runService 根据结果关联去重，messages 不保存 run_id。设备进度不直接变成普通聊天消息；取消可以只更新状态，不生成回复。

结果关联由 runService 提供事务内绑定操作完成，不要求 sessionService 直接改 runs。结果事件未消费时，run 可以已经结束、最终回复尚未写入；快照必须明确保留这个区别。恢复时可扫描终态 run 的持久化终态事件，由 runService 检查 runs.result_message_id 判断是否需要补写，不依赖一次性的内存通知。

## 状态和删除

Session 状态是 run 的聚合投影，优先 waiting、running、pending，再到 idle；resolved 是用户的完成标记。投影由 sessionService 通过 runService 查询当前状态后更新，run 状态变化和最终结果事件都触发重算；重算读取当前 run 集合，不用旧事件直接覆盖投影。idle 不表示历史任务全部成功。

有活跃 run 时拒绝删除，检查与删除共用事务；回复消费前也要重新检查会话是否存在，已删除的会话不恢复创建。删除会话级联清理业务记录；磁盘附件由 attachmentService 清理。Work 的协调会话和线程归属由 workService 决定。

## 幂等

发送用户和 request_key/request_digest 直接保存在原始用户消息上；service 保证带 key 时用户与摘要非空。

messages 中的唯一索引按 session、用户和非空 request_key 去重。相同 key、相同内容及目标返回原消息；摘要不一致则冲突。它解决重复提交，不保证执行工具的外部副作用只发生一次。

## 表结构

### sessions

共享会话主表。`user_id` 是资源所有者，`created_by` 是创建者；可归属一个 work，不绑定 agent。所有 session 都支持多 agent，不设 mode 字段。状态是聚合投影，不替代 run 明细。

```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    created_by TEXT NOT NULL REFERENCES users(id),
    work_id TEXT REFERENCES works(id),
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'idle',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX sessions_work
    ON sessions(work_id,updated_at);
CREATE INDEX sessions_status
    ON sessions(status, updated_at);
CREATE INDEX sessions_user
    ON sessions(user_id, updated_at);
CREATE INDEX sessions_created_by
    ON sessions(created_by, updated_at);
```

### messages

共享对话的原始消息。`format` 直接区分 prompt/json；`user_id` 记录发送用户，`agent_key/name` 记录回复作者快照。`target_agent_ids` 保存原始 @ 目标数组，即使执行分配变化，对话仍能显示当时 @ 了谁。

message 永远回复另一条 message，不回复 run。`reply_to_message_id` 表示被回复的消息，开头的根消息为空；后续用户消息、agent 回复都可以引用同 session 中已有的消息。messages 不保存 run_id，也不以 run 为对话层级。

```text
M1：@coder @reviewer 修复并评审
  ├─ M2：coder 的回复，reply_to_message_id=M1
  ├─ M3：reviewer 的回复，reply_to_message_id=M1
  └─ M4：用户追问，reply_to_message_id=M1 或选中的具体回复
```

一个 run 消费多条输入时，最终回复默认指向其主任务 message；补充输入仍通过 run_prompts 记录。service 检查父消息已存在、属于同一 session，避免跨会话引用和循环回复。

执行结果的内部关联留在 runs.result_message_id，允许 runService 追踪及去重，但不会出现在 messages 的字段或回复语义中。sessionService 在同一事务调用 runService 检查结果是否已绑定，未绑定才新增回复并绑定；重复结果直接返回已有回复。取消可以没有回复。

```sql
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('user','assistant')),
    content TEXT NOT NULL,
    format TEXT NOT NULL DEFAULT 'prompt' CHECK(format IN ('prompt','json')),
    user_id TEXT REFERENCES users(id),
    target_agent_ids TEXT NOT NULL DEFAULT '[]',
    agent_key TEXT,
    agent_name TEXT,
    reply_to_message_id INTEGER REFERENCES messages(id),
    request_key TEXT,
    request_digest TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX messages_session ON messages(session_id,id);
CREATE INDEX messages_replies ON messages(session_id,reply_to_message_id,id);
CREATE UNIQUE INDEX messages_request ON messages(session_id,user_id,request_key)
    WHERE role='user' AND request_key IS NOT NULL;
```

### 合并掉的辅助表

| 原表 | v3 处理 |
|---|---|
| message_formats | 不再建表，格式直接保存在 messages.format；执行副本复制到 run_prompts.format |
| message_authors | 不再建表，回复作者直接保存在 messages.agent_key/name；回复关系使用 reply_to_message_id，执行关联留在 runs |
| session_requests | 不再建表；保留发送幂等需求，用 messages 的 user_id、request_key、request_digest 和唯一索引实现 |

这些信息仍有作用，但都是一条消息自身的属性，不需要再各拆一张 1:1 的表。

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [harnessService](harness.md) · [runService](run.md) · [attachmentService](attachment.md) · [workService](work.md)
