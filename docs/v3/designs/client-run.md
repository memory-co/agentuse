# Client runService：执行从中心领取的 Run

> v3 目标设计。这里的 runService 属于 client，负责本机执行；server runService 负责任务分配和业务状态。两者通过标准 run 协议协作，不共享数据库实现。

## 职责

- 只接受 manageService 授权领取的 server run，检查中心、run/attempt、本机槽位和包版本。
- 请求 sessionService 选择/恢复原生 session，将领来的 run 绑定到本机 session。
- 获取当前 run_prompts、共享 conversation 增量和文件，组织本次执行输入。
- 将插话、答案、取消路由到对应本地执行，并上报标准事件。
- 写本机 runs 账本，维护本次耗时、用量和错误，结束后释放占用。

Client 不创建独立的业务 run。run_id 和 attempt_id 来自中心；本机记录按 `(center_id, run_id, attempt_id)` 区分。一个本地 session 可以连续执行多个从 server 领来的 run，同一个 session 同时只执行一个。

## 与其他 Service 的关系

| Service | 协作 |
|---|---|
| manageService | 提供中心上下文和领取槽位；执行事件回原中心，结束释放本机容量 |
| sessionService | 选择/恢复原生会话、执行锁、历史增量位置；run 结束后保留 session |
| harnessService | 在已选 session 中执行任务，将原生交互转换成标准事件 |

连接循环由 manageService 管理，不在 runService 中为每种 harness 再维护网络连接。session 的跨 run 生命周期与当前 run 的进程/回调清理分开处理。

## 执行流程

1. 校验中心和领取授权，写 received 记录；无效或不支持的任务记拒绝，不擅自生成替代任务。
2. 调 sessionService 取得空闲且版本匹配的 session；首次创建原生上下文，后续可以恢复并继续使用。
3. 准备当前输入与共享历史增量，交 harnessService 执行。配置和 hook 资源按 center/run/attempt 隔离。
4. 标准进度、问题、消费确认、结果和用量更新本地记录，并经 manageService 关联的连接回报原中心。
5. 执行结束后清理本次回调/进程占用，session 回到可复用状态，释放全局槽位。原生历史仍保留。

业务状态仍以 server 为权威，本机终态不等于结果已被 server 提交。重启时先对账，不因为账本里有旧任务就自行执行。只有确认有效的 attempt 或重新领来的任务才能工作。

## 表结构

本机时间字段使用 Unix 毫秒，无外键。此表与 centers、manage_settings、sessions 共用本机 SQLite，不是 server 的 runs 表。

### runs：本机执行账本

本机 runs 只记录从中心领来的任务及执行尝试。run_id、attempt_id 来自 server，session_id 指向本机 sessions.id，不是 server conversation ID。尚未完成会话选择或本机拒绝的任务可以没有 session_id。

```sql
CREATE TABLE runs (
    center_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    session_id TEXT,
    conversation_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    native_run_id TEXT,
    context_seq INTEGER NOT NULL DEFAULT 0,
    summary TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'received',
    error TEXT NOT NULL DEFAULT '',
    received_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    steer_count INTEGER NOT NULL DEFAULT 0,
    attachment_count INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER,
    output_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    cost_usd REAL,
    PRIMARY KEY(center_id,run_id,attempt_id)
);
CREATE INDEX runs_session ON runs(session_id,received_at);
CREATE INDEX runs_received ON runs(received_at DESC);
CREATE INDEX runs_conversation ON runs(center_id,conversation_id,received_at);
```

native_run_id 可保存 Codex 当前 turn ID；原生接口没有独立执行 ID 时为空。本地运行中对象还需维护请求/回调关联，不把所有协议状态塞进 SQLite。

status 表示本机 received/running/waiting/succeeded/failed/cancelled/rejected/interrupted 等状态；本机结束不代表 server 已接受结果。summary 默认只保存正文前 200 字，token 或费用未知时为空。

同一个 server run 重新分配 attempt 后可以有多行本机记录。每行只绑定实际使用过的一个 session；正常复用 session 不修改 server run_id。表不使用外键，删除中心或清理旧账本不自动删除原生历史。

## 相关设计

[Client 总览](client-main.md) · [manageService](client-manage.md) · [sessionService](client-session.md) · [harnessService](client-harness.md) · [Server runService](server-run.md)
