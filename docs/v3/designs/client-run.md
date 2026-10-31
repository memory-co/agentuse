# Client runService：执行从中心领取的 Run

> v3 目标设计。这里的 runService 属于 client，负责本机执行；server runService 负责任务分配和业务状态。两者通过标准 run 协议协作，不共享数据库实现。

## 职责

- 读取 endpointService 中已启用的 endpoints，自己建立连接、上报能力、心跳、重连、对账与领取任务。
- 统一管理本机容量与多 endpoint 的领取机会，检查 run/attempt 和包版本。
- 请求 sessionService 选择/恢复原生 session，将领来的 run 绑定到本机 session。
- 获取当前 run_prompts、共享 conversation 增量和文件，组织本次执行输入。
- 将插话、答案、取消路由到对应本地执行，并上报标准事件。
- 写本机 runs 账本，维护本次耗时、用量和错误，结束后释放占用。

Client 不创建独立的业务 run。run_id 和 attempt_id 来自中心；本机记录按 `(endpoint_id, run_id, attempt_id)` 区分。一个本地 session 可以连续执行多个从 server 领来的 run，同一个 session 同时只执行一个。

## 与其他 Service 的关系

| Service | 协作 |
|---|---|
| endpointService | 只提供已注册 server 的地址/身份/凭据；连接、领取与容量全部由 runService 管理 |
| sessionService | 选择/恢复原生会话、执行锁、历史增量位置；run 结束后保留 session |
| harnessService | 在已选 session 中执行任务，将原生交互转换成标准事件 |

每个 endpoint 的连接循环属于 runService，不在 endpointService 或 harnessService 中维护；原生 harness 连接由本地 harnessService 处理。session 的跨 run 生命周期与当前 run 的进程/回调清理分开处理。

## 多个 Server 的连接与领取

runService 启动时读取启用的 endpoints，为每个已注册 server 建立独立连接。一次连接失败只对该 endpoint 退避重连，不阻塞其他 endpoint。探测由本地 harnessService 完成，由 runService 向各 server 上报标准能力。

所有 endpoint 共用 max_running_runs，默认 1。runService 原子预留空闲槽位，再按轮询顺序对某个 endpoint 发起一次有界领取，明确得到 run 或 no_task。无任务立即尝试下一 endpoint；不能向多个 server 广播长期有效的 ready，避免重复派活超过本机容量。

领取超时的晚到任务需重新核对授权，失效则拒绝/交原 server 租约恢复，不能绕过槽位检查直接启动。等待用户答案的 run 仍占容量，长任务不因轮询被抢占。结束后由 runService 自己释放槽位并继续领取。

## Endpoint 隔离

本机用 `(endpoint_id, run_id, attempt_id)` 定位执行，session 和目录也包含 endpoint_id。答案、插话和取消从连接所属 endpoint 关联到任务；结果、心跳、文件与工作 API 始终使用该执行绑定的 server 和凭据。

两个 server 即使返回相同远端 ID，也不共享目录或结果。凭据按本次执行传入，不通过修改整个 client 进程的全局环境切换 server。

## 执行流程

1. 校验中心和领取授权，写 received 记录；无效或不支持的任务记拒绝，不擅自生成替代任务。
2. 调 sessionService 取得空闲且版本匹配的 session；首次创建原生上下文，后续可以恢复并继续使用。
3. 准备当前输入与共享历史增量，交 harnessService 执行。配置和 hook 资源按 endpoint/run/attempt 隔离。
4. 标准进度、问题、消费确认、结果和用量更新本地记录，并经 runService 为该 endpoint 维护的连接回报原 server。
5. 执行结束后清理本次回调/进程占用，session 回到可复用状态，释放全局槽位。原生历史仍保留。

业务状态仍以 server 为权威，本机终态不等于结果已被 server 提交。重启时先对账，不因为账本里有旧任务就自行执行。只有确认有效的 attempt 或重新领来的任务才能工作。

## 表结构

本机时间字段使用 Unix 毫秒，无外键。下列两张表与 endpointService 的 endpoints、sessionService 的 sessions 共用本机 SQLite，不是 server 的 runs 表。

### run_settings：本机执行容量

单行配置由 runService 管理，id 固定为 1。max_running_runs 是所有 endpoint 合计的容量；降低后等待已有任务收尾，不为达到上限立即取消执行。槽位、在线状态及重连计时只保存在运行时内存，不据此恢复旧进程。

```sql
CREATE TABLE run_settings (
    id INTEGER NOT NULL PRIMARY KEY CHECK(id=1),
    max_running_runs INTEGER NOT NULL DEFAULT 1 CHECK(max_running_runs>0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

### runs：本机执行账本

本机 runs 只记录从中心领来的任务及执行尝试。run_id、attempt_id 来自 server，session_id 指向本机 sessions.id，不是 server conversation ID。尚未完成会话选择或本机拒绝的任务可以没有 session_id。

```sql
CREATE TABLE runs (
    endpoint_id TEXT NOT NULL,
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
    PRIMARY KEY(endpoint_id,run_id,attempt_id)
);
CREATE INDEX runs_session ON runs(session_id,received_at);
CREATE INDEX runs_received ON runs(received_at DESC);
CREATE INDEX runs_conversation ON runs(endpoint_id,conversation_id,received_at);
```

native_run_id 可保存 Codex 当前 turn ID；原生接口没有独立执行 ID 时为空。本地运行中对象还需维护请求/回调关联，不把所有协议状态塞进 SQLite。

status 表示本机 received/running/waiting/succeeded/failed/cancelled/rejected/interrupted 等状态；本机结束不代表 server 已接受结果。summary 默认只保存正文前 200 字，token 或费用未知时为空。

同一个 server run 重新分配 attempt 后可以有多行本机记录。每行只绑定实际使用过的一个 session；正常复用 session 不修改 server run_id。表不使用外键，删除中心或清理旧账本不自动删除原生历史。

## 相关设计

[Client 总览](client-main.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [harnessService](client-harness.md) · [Server runService](server-run.md)
