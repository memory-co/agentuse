# Client runService：只承认适配器生成的 Run

runService 接收输入、驱动 session 并提供实际执行的视图。**run_id 唯一来源是 harnessctl 写入的 accepted.run_id。** Client 不创建另一种执行 run，也不创建 attempt，不规定一次 send 一定得到新 run。

## 输入与控制

1. 根据输入来源确定 endpoint/conversation，选择空闲 session 或向活动 session 继续 send。
2. 通过 sessionService 登记占用，准备文本及必要的上下文资源。
3. harnessService 用 UTF-8 stdin 调用 send，监听该 session 的 events.jsonl。
4. 看到 accepted 后登记其 UUID，再向调用者返回该 run；accepted 前的启动错误直接返回错误。
5. 后续输入仍是 send，由适配器决定加入当前交互还是排队产生新 UUID。
6. request 按 request_id reply；cancel 针对 session，不制造 run 级取消能力。
7. 当前驱动窗口内所有已受理输入都有结果后，释放 session 占用；这只是进程内生命周期，不是另一个 run 身份。

一个驱动窗口可以观察到多个真实 run；页面和 API 分别展示每个 UUID，不能将它们聚合成一个 client run。一条 run 可以包含多个 accepted 输入，例如 Codex 确认的插话。

## 持久化与查询

```text
sessions/<session_id>/
  session.yaml
  events.jsonl
  events.jsonl.d/
  runs/<accepted.run_id>.jsonl
```

run 引用仅定位实际的适配器 UUID：

```json
{"type":"reference","at":1793443200000,"run_id":"550e8400-e29b-41d4-a716-446655440000","endpoint_id":"local","conversation_id":"conversation-1","session_id":"session-1","agent_id":"local-codex","harness":"codex","harness_revision":"revision"}
```

正文、结果、完整输入、请求、用量、费用和执行日志不复制到引用中。runService 按同一个 UUID 从 events.jsonl 读取五种公共事件，返回 completed/failed/cancelled 结果；未结束时为 running，仍有 pending request 时为 waiting。计量按一个真实 result 计数，不把输入数当作执行次数，也不累计原生 session 的累计计量。

input_id 和 request_id 来自适配器，不能以客户端临时 ID 替换。无论 local 还是远端，Run DTO 都没有 attempt_id、server_confirmed、harness_run_ids 或第二套任务 ID。

## 恢复和目录保护

accepted 事件是执行归属的事实。若适配器写入 accepted 后 client 来不及创建引用就退出，启动时扫描该 session 的公共事件即可重建引用；不需要预分配 ID、按时间猜测或恢复执行尝试。

引用已存在时不重新写执行结果。读取只接受完整 JSONL 行，忽略末尾尚未写完的一行；完整记录损坏则明确报错，不从日志猜测结果。适配器私有 .d/ 不由 client 解释。

活动、等待答案或清理未确认的 session 都保护整个 conversation 工作目录。取消失败不能假装已经空闲。声明 quota 后只回收完整的空闲受管目录，不能单独删正在使用的 run 文件。

## 远端传输边界

local 和远端调用完全相同的 sessionService、harnessService 与 runService；都直接展示适配器 UUID。Server 不再有另一套执行 run，输入分配 ID 仅用于关联 server 消息。

每个远端 session 额外保存传输收据：

```text
.server/
  inputs/<prompt_id>.json  输入提交状态、源起点及 accepted 的 run_id/input_id
  stream.json             server 已提交确认的 events.jsonl 字节偏移
  controls/<control_id>.json  reply/cancel 送达收据
```

这些文件不复制执行输出或结果。源 JSONL 是唯一事件 outbox：发送完整行，收到 ACK 后原子推进游标。ACK 丢失则重传同一源位置，server 去重；重连收到同一输入 ID 时读取收据，不再次 send。活动 session 在连接短暂断开时继续运行。

启动先确认遗留前台工作停止，再恢复输入收据：从提交前保存的源位置读取 accepted，补回实际 UUID；存在歧义则报错，不重放输入。只读完整 JSONL 行。尚未确认的失败通知、未确定受理状态和未确认上报的事件保护 conversation 目录，不因 quota 丢掉待交付事实。

控制命令以 control_id 去重；cancel 携带目标 run_id，目标已经终态时不能取消后续交互。request 的状态和最终结果仍以适配器事件为准，传输 ACK 不代表业务状态已改变。

## 实现归属

native_runs.go 管引用与查询，views.go 定义公开视图，reconcile.go 管启动恢复，server_connection.go 管中心连接、输入收据、事件游标和控制传输。没有 store、执行 attempt、结果副本或旧协议兼容层。sessionService 管目录与会话元数据，fileutil 仅提供通用文件操作。

## Server Agent 版本衔接（已实现）

远端输入携带 agent_version_id、model、agent_instructions 和固定 skill 包版本清单。runService 按已分配输入下载资源、校验 SHA-256/大小并安全解包，调用 sessionService 选择同版本会话，再由 harnessService 调用 init/send。下载只使用当前 endpoint 的相对 API 路径，不把设备密钥发到其他域。

活动输入匹配加入 agent_version_id，不能把新版消息塞入旧版驱动窗口；同版本补充输入仍由适配器裁定 run 归属。ClientEvent 信封和本地 run 引用记录该版本关联，公共 accepted/message/request/result/log 的 Schema 不变。详情见 [client-session.md](client-session.md) 与 [版本化通信字段](protocol.md#agent-versioning)。
