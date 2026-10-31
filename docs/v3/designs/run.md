# runService：任务、消息分配与执行生命周期

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

runService 管一次业务执行任务。一个 run 属于一个 session，确定 agent、device、harness；通过 run_prompts 保存多条任务输入副本。每次重新领取通过 attempt 区分实际执行身份，不另建一条 run 代表网络重试。

## 与其他 Service 的关系

- sessionService 在发送事务中调用 runService，为每个目标建立分配；runService 不反向调用 sessionService。
- agentService 提供角色及执行选择，deviceService 提供设备能力，harnessService 提供固定版本配置。
- workService 提供工作说明和执行范围的授权；与 run 同寿命的工作令牌由 workService 管理。
- runService 持久化结果事件后通知 sessionService 消费；由 sessionService 在事务内检查并绑定 runs.result_message_id，防止重复回复；messages 不需要 run_id。

## run_prompts：面向一次 Run 的消息副本

messages 保存用户在 session 中说过什么；run_prompts 保存“这次 run 实际要处理什么”。它既建立 message 与 run 的 N:M 关系，也保存输入内容快照和消费状态，不再另建 message_deliveries。

```text
M1 @coder @reviewer
  → P1：run=R1，message=M1，复制 M1 内容，供 coder 执行
  → P2：run=R2，message=M1，复制 M1 内容，供 reviewer 执行
M2 @coder（R1 仍在执行）
  → P3：run=R1，message=M2，复制 M2 内容，作为 R1 的补充输入
```

一条消息 @ 两个 agent，写两条 run_prompts。一个 run 可有多条 run_prompts。副本保存消息正文和 attachments 路径列表，执行时以副本为准，不每次回查原始 messages 重建输入；运行时上下文仍可读取同 session 的共享历史。

run.message_id 仅标识主任务。run_prompts.run_id 创建后不改变：未消费的输入需要后续 run 时，复制到新 run 的新 run_prompts，旧行保留并标记 deferred，用 superseded_by 连接后续副本。这样旧 run 当时收到的输入仍可追溯。

## 生命周期

| 操作 | 规则 |
|---|---|
| 建立分配 | 同 session + agent 最多一个 pending/running/waiting run；无活跃任务则建新 run，否则追加 supplement run_prompts |
| 领取 | 按 device 找 pending run；校验能力，生成 attempt，设租约，准备任务消息和共享上下文 |
| 插话 | 仅对当前执行推送可接受的输入；执行端确认后记 delivered，不因 HTTP 保存成功就确认消费 |
| 提问/回答 | 问题绑定 run/attempt；答案先保存，执行端确认消费后再推进问题与 run 状态 |
| 结束 | 写终态及持久化结果事件；更新 run_prompts、撤销执行凭据，通知 sessionService 保存回复 |
| 超时重试 | 同 run 重排 pending，重新领取时换 attempt；拒绝旧执行的新事件 |
| 未消费输入 | 正常结束时可转入后续新 run；取消则终止分配，不自动继续 |

Run 状态是 pending/running/waiting/succeeded/failed/cancelled；run_prompts 是 pending/delivered/succeeded/failed/cancelled/deferred。等待某条消息全部处理完，需跟踪该 message 的全部 run_prompts：沿 superseded_by 找到各目标最新副本，等待它们全部成功、失败或取消；deferred 仅表示移交，不代表任务已完成，不能只等最初返回的某个 run。

所有终态来源（包括取消、运行时不可用、设备删除）都必须写可恢复的终态事件。run_events 同时保存执行端事件和 server 生成的生命周期事件；server 合成事件在事务内分配不冲突的 event_seq。处理终态与后续输入在统一事务内完成，网络通知在提交后进行。

## v3 执行配置快照

runs 增加 harness_revision、harness_config_yaml。建立 run 时复制 harnessService 返回的定义，client 按快照执行。更新同名配置不会改变排队/重试任务；创建后续新 run 时重新选择配置。

## 表结构

### runs

一次执行任务。`message_id` 是主任务，`result_message_id` 是执行结果的内部关联；最终回复通过 messages.reply_to_message_id 指向主任务消息，而不是回复这个 run。`agent_key/name/user_id` 是历史身份；设备删除时由 service 清空当前 device_id 引用，历史身份快照仍保留。attempt_id/lease_until 表示当前执行租约，context_seq 对应 messages.seq，表示上下文读取边界；message_id 和 result_message_id 保存消息 UUID，不用 UUID 比较先后。配置快照是 v3 新增字段。

```sql
CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    agent_id TEXT,
    agent_key TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    agent_user_id TEXT NOT NULL,
    device_id TEXT,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    harness_config_yaml TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_id TEXT NOT NULL DEFAULT '',
    lease_until INTEGER,
    context_seq INTEGER NOT NULL DEFAULT 0,
    result_message_id TEXT,
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX runs_active_agent
    ON runs(session_id,agent_id)
    WHERE agent_id IS NOT NULL AND status IN ('pending','running','waiting');
CREATE INDEX runs_status
    ON runs(status,created_at);
CREATE INDEX runs_dispatch
    ON runs(device_id,status,created_at);
```

### run_prompts

每行属于一个确定的 run，并引用原始 message；content 和 attachments 是消息正文及文件路径列表的冗余快照。kind 为 initial/supplement，表示首次任务或补充输入；prepared_attempt 标识准备该输入的执行尝试。相同 run/message 只能插一行，但同一 message 可以在多个 run 各有副本。

```sql
CREATE TABLE run_prompts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    content TEXT NOT NULL,
    attachments TEXT NOT NULL DEFAULT '[]',
    kind TEXT NOT NULL CHECK(kind IN ('initial','supplement')),
    status TEXT NOT NULL DEFAULT 'pending',
    prepared_attempt TEXT NOT NULL DEFAULT '',
    superseded_by INTEGER,
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(run_id,message_id)
);
CREATE INDEX run_prompts_message ON run_prompts(message_id,run_id);
CREATE INDEX run_prompts_pending ON run_prompts(run_id,status,id);
```

attachments 同样是当前 session 下的相对路径 JSON 数组，复制列表不复制文件；文件仍通过 fileService 读取，同一路径覆盖上传后读到更新内容，不承诺文件版本快照。

副本内容在创建后固定；从同 run 的旧 attempt 重试时复用这些输入行，更新准备/消费状态。转入新 run 时在同一事务创建新行并设置旧行的 superseded_by，不移动旧行，不重复创建原消息。通过 run 和 message 的归属检查，保证关联属于同一个 session。

### run_events

进度、计划、交互确认和终态事件。payload 为 JSON，唯一键保证同 run/attempt/序号重复上报不重复生效。终态 payload 保留完整结果，供结果消费恢复。device_id 是事件来源身份快照，设备删除后仍保留历史标识。

```sql
CREATE TABLE run_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
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
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    tool_use_id TEXT NOT NULL,
    questions TEXT NOT NULL,
    answers TEXT,
    answered_by TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(run_id,attempt_id,tool_use_id)
);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [harnessService](harness.md) · [sessionService](session.md) · [fileService](file.md) · [workService](work.md)
