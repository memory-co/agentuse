# Client runService：执行从中心领取的 Run

> v3 目标设计。这里的 runService 属于 client，负责本机执行；server runService 负责任务分配和业务状态。两者通过标准 run 协议协作，不共享存储实现；client 只使用文件，不使用 SQLite。

## 职责

- 读取 endpointService 中已启用的 endpoints，自己建立连接、上报能力、心跳、重连、对账与领取任务。
- 统一管理本机容量与多 endpoint 的领取机会，检查 run/attempt 和包版本。
- 请求 sessionService 选择/恢复原生 session，将领来的 run 绑定到本机 session。
- 获取当前 run_prompts、共享 conversation 增量和文件，组织本次执行输入。
- 将插话、答案、取消路由到对应本地执行，并上报标准事件。
- 写本机 run.json 执行记录，维护本次耗时、用量和错误，结束后释放占用。

平台任务的 run_id 和 attempt_id 来自中心；内置 local 调试入口可在本机生成 run/attempt UUID，并调用同一执行链路，不创建中心业务任务。本机记录按 `(endpoint_id, run_id, attempt_id)` 区分。一个本地 session 可以连续执行多个从 server 领来的 run，同一个 session 同时只执行一个。

## 与其他 Service 的关系

| Service | 协作 |
|---|---|
| endpointService | 提供 endpoints.yaml 中的注册身份，连接与领取由 runService 管理 |
| settingService | 提供工作目录及磁盘容量策略；runService 消费这些设置，不另存 run_settings |
| sessionService | 选择/恢复原生会话、执行锁、历史增量位置；run 结束后保留 session |
| harnessService | 在已选 session 中执行任务，调用 harnessctl，将公共事件转换成业务状态 |

每个 endpoint 的连接循环属于 runService，不在 endpointService 或 harnessService 中维护；原生 harness 连接由独立 harnessctl 适配器处理。session 的跨 run 生命周期与当前 run 的进程/回调清理分开处理。

## 多个 Server 的连接与领取

runService 启动时读取启用的 endpoints，为每个已注册 server 建立独立连接。一次连接失败只对该 endpoint 退避重连，不阻塞其他 endpoint。探测由本地 harnessService 完成，由 runService 向各 server 上报标准能力。

所有 endpoint 共用 runService 内部的领取与并发调度，不在 setting.yaml 暴露执行数量或槽位配置。不同 session 可以并行，同一 session 保持串行。runService 先在内部原子预留本次执行机会，再按轮询顺序对某个 endpoint 发起一次有界领取，明确得到 run 或 no_task。无任务立即尝试下一 endpoint；通过带 claim_id 的 claim 请求单次领取，server 回 run/no_task；所有 endpoint 共享内部执行容量。不能向多个 server 广播长期有效的 ready，避免重复派活超过本机容量。

领取超时的晚到任务需重新核对授权，失效则拒绝/交原 server 租约恢复，不能绕过槽位检查直接启动。等待用户答案的 run 仍占容量，长任务不因轮询被抢占。结束后由 runService 自己释放槽位并继续领取。

## Endpoint 隔离

本机用 `(endpoint_id, run_id, attempt_id)` 定位执行，session 和目录也包含 endpoint_id。答案、插话和取消从连接所属 endpoint 关联到任务；结果、心跳、文件与工作 API 始终使用该执行绑定的 server 和凭据。

两个 server 即使返回相同远端 ID，也不共享目录或结果。凭据按本次执行传入，不通过修改整个 client 进程的全局环境切换 server。

## 执行流程

1. 校验中心和领取授权；尚未绑定 session 的领取请求只在内存中处理，无效或不支持则直接向原 server 回报拒绝，不创建无归属的 run.json。
2. 按 settingService 的配置触发 sessionService 检查工作目录容量；领取前检查统一工作空间容量，已领取却不能启动时明确回报资源不足，不无限占位或伪报 started；有空间才选择/创建空闲且版本匹配的 session，绑定后写 received 记录；没有可回收空间则暂不启动新任务。
3. 准备当前输入与共享历史增量，交 harnessService 执行。配置和 hook 资源按 endpoint/run/attempt 隔离。
4. 标准进度、问题、消费确认、结果和用量更新本地记录，并经 runService 为该 endpoint 维护的连接回报原 server。
5. 执行结束后清理本次回调/进程占用，session 回到可复用状态，释放全局槽位。原生历史仍保留。

业务状态仍以 server 为权威，本机终态不等于结果已被 server 提交。重启时先对账，不因为账本里有旧任务就自行执行。只有确认有效的 attempt 或重新领来的任务才能工作。

## 文件结构

```text
<session.work_root>/<endpoint_id>/<conversation_id>/sessions/<local_session_id>/
  runs/<run_id>/<attempt_id>/run.json
```

每个已绑定 session 的执行尝试一个文件，conversation 根目录由 session.work_root、endpoint_id 和 conversation_id 直接推导，所有实际执行统一以其下的 workspace/ 为 cwd；run.json 在同级 sessions/ 树内。目录层级为 conversation → session → run → attempt，唯一执行身份仍为 endpoint/run/attempt，不再有本机 runs 表或 run_settings 表。磁盘容量策略读取 setting.yaml；领取预留、连接状态和执行槽位由 runService 内部管理，只存在运行时内存中。

```json
{
  "endpoint_id": "https---company.example.com",
  "run_id": "run-1",
  "attempt_id": "attempt-1",
  "session_id": "7877d33b-788f-4f6c-9ff0-987c2128064e",
  "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
  "agent_id": "coder",
  "harness": "codex",
  "harness_revision": "package-revision",
  "native_run_id": "turn-1",
  "context_seq": 12,
  "summary": "修复登录问题",
  "status": "succeeded",
  "error": "",
  "received_at": 1793443200000,
  "started_at": 1793443200100,
  "finished_at": 1793443205100,
  "duration_ms": 5000,
  "steer_count": 0,
  "attachment_count": 0,
  "usage": {"input_tokens": 100, "output_tokens": 20},
  "cost_usd": null
}
```

session_id 必须是所在目录的本机 session UUID，不是 server conversation ID，不能留空。每个本机执行尝试只归属一个 session；同一 server run 重试时可能使用另一个 session，通过 attempt_id 区分。尚未绑定 session 的请求不创建该文件；拒绝结果直接回报 server。native_run_id 可保存 Codex turn ID，没有独立执行 ID 时为空。时间用 Unix 毫秒，用量或费用未知时为空。

run.json 是该执行记录的持久化事实。状态、耗时和计量由 runService 通过临时文件写完后原子替换；同一执行串行写入。Reader 读取文件的完整版本，不使用数据库索引，也不把内存缓存当成唯一记录。

状态包括 received/running/waiting/succeeded/failed/cancelled/rejected/interrupted。本机终态不代表 server 已接受结果。终态回报先保存为 run.json 的 pending_report，收到 server 的 event_ack 后清除并记录 server_confirmed；重启或重连时可重发同一回报，中心按原 event_seq 核对。日志可以单独保存，但不是另一份决定业务状态的账本。

删除 endpoint 不主动删除 run 文件。回收 conversation 工作目录时，其中 session.yaml、run.json 和执行资源一起删除；本机页面不再展示这些已清理记录，历史业务状态由原 server 保存。不另设独立 run 归档目录或固定保留期。尚未结束、等待答案或结果尚未被 server 确认的执行都保护整个工作目录，不能作为清理候选。

## 目录清理与恢复

声明 quota 后，runService 在启动、执行前后和内置周期检查中自动触发 sessionService 清理空闲受管工作目录；不直接递归删除任意路径。同一 conversation 任一 run 仍占用的共享目录都不能成为候选，不能因当前 session 空闲就删除其他 session 正在使用的文件。

创建 run.json、绑定 session、更新同步位置涉及多个文件，没有跨文件数据库事务。进程中串行管理，崩溃后读取状态文件并与 server/原生状态核对；无法确认的任务标中断，不因一个旧 running 字段自行重跑。

## 相关设计

[Client 总览](client-main.md) · [settingService](client-setting.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [harnessService](client-harness.md) · [Server runService](server-run.md)

本机 run.json 额外保存 harness_run_ids：这些 UUID 由适配器分配，一个 server run/attempt 可以对应多个适配器交互。harnessService 等待本次所有输入都被 result 覆盖后才结束任务，详见 client-harness.md。
