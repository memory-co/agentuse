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

## 来源边界

local 提供本机输入；现有 server 提供旧版派发协议。两者都调用同一个 harnessService，登记同样的 adapter UUID，并使用同一组 runService 查询。旧 server 的派发标识与确认状态仅存在 server_*.go 和 .server/，不是 client run。

如果旧协议需要把多个实际 run 汇总成一个回报，由边界层完成；这个汇总不在 client API 中形成另一个执行实体。

## 旧数据

旧任务文件先移出 runs/：远端旧交付记录进入 .server/，本地旧任务记录进入 legacy/ 只作历史留存。根据其中的真实适配器 UUID及公共 accepted 事件重建 runs 引用。没有适配器 UUID 的适配前记录不伪造成新 run，不生成替代 UUID；保留原数据供查阅。

迁移不修改 events.jsonl 或 events.jsonl.d/，不重放输入，不递归删除未知文件。

## 实现归属

service/run 的 native_runs.go 直接创建原生 run 引用，并提供 List / Detail / Stats；views.go 定义公开查询数据。reconcile.go 编排启动恢复，run_migration.go 保留旧文件的迁移规则。server_*.go 只处理现有中心协议及 .server/ 交付文件。这里没有独立 store、Ledger 或全局 Reader。

runService 使用 sessionService 的目录与 session 查询，不复制 session.yaml 的管理逻辑。HTTP controller 通过只读接口分别查询这两个 service；文件路径、事件格式、API 响应和 run_id 归属不因代码目录调整而变化。
