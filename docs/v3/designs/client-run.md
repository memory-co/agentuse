# Client runService：任务执行与 JSONL 账本

runService 负责连接各 endpoint、领取任务、控制执行及回报结果。配置与适配器在 client 启动时固定；运行中重连使用同一份启动配置，不热加载。

local 与远端使用相同的任务记录、执行、查询、恢复和清理机制。区别仅是任务来源：local 生成任务及 attempt UUID，远端使用 server 分配的标识。一个本机 session 串行执行多个任务，不同 session 可以并行，共享 conversation 的 workspace。

## 服务关系

| 服务 | 职责 |
|---|---|
| endpointService | 提供启动时固定的 endpoint 身份和凭据 |
| settingService | 提供启动时固定的工作根目录及 quota |
| sessionService | 选择 session、管理占用、历史位置和目录回收 |
| harnessService | 四个 harnessctl 命令、公共事件及执行关联 |
| runService | 领取、调度、任务控制、账本和向所属 server 回报 |

## 文件结构

```text
<session.work_root>/<endpoint_id>/<conversation_id>/
  workspace/
  sessions/<session_id>/
    session.yaml
    events.jsonl
    events.jsonl.d/                       # 适配器私有状态，client 不解析
    runs/
      <run_id>.jsonl
    resources/
      <run_id>.<attempt_id>.history.json  # 仅有非空共享历史时生成
```

每个业务任务一个 JSONL 文件。同一任务在该 session 中的多个 attempt 都追加到同一文件，不建立 run 或 attempt 子目录。任务在另一个 session 重试时，其记录归属于实际执行的 session。执行身份仍是 endpoint/run/attempt；这组身份不要求对应三层磁盘目录。

不再创建 run.json、input.json 或空的 history.json。实际需要交给 agent 读取的共享历史属于执行资源，保存在 resources/，与账本分开。工作目录从 conversation 路径推导，不在账本中重复保存绝对路径。

## 两种 run_id

- 文件名中的 run_id 是业务任务 ID，远端来自 server，local 在本机产生。
- events.jsonl 中的 run_id 是适配器生成的交互 UUID，client 不指定。

一次业务执行可能覆盖多个适配器交互，尤其是补充输入被内部排队时。账本在接收任务时记录 events_offset，harnessService 根据 accepted 立即追加 harness_run_ids 关联，等待本次所有 input_id 都由 result 覆盖后才结束任务。不能把文件名直接当成适配器 run_id，也不能只读取第一个 result。

## runs/<run_id>.jsonl 的格式

每行包含 type、at（Unix 毫秒）、attempt_id 和 data：

```json
{"type":"received","at":1793443200000,"attempt_id":"attempt-1","data":{"endpoint_id":"local","conversation_id":"conversation-1","session_id":"session-1","run_id":"task-1","attempt_id":"attempt-1","status":"running"}}
{"type":"updated","at":1793443200100,"attempt_id":"attempt-1","data":{"harness_run_ids":["550e8400-e29b-41d4-a716-446655440000"]}}
{"type":"updated","at":1793443205100,"attempt_id":"attempt-1","data":{"status":"succeeded","finished_at":1793443205100,"duration_ms":5100}}
{"type":"updated","at":1793443205200,"attempt_id":"attempt-1","data":{"server_confirmed":true}}
```

received 保存任务归属、输入摘要、接入包版本等调度元数据；updated 仅追加发生变化的字段。字段值为 null 表示清除该字段，例如已确认后清除 pending_report。相同 attempt 的重复接收不重置记录；另一个 attempt 追加新的 received。

**正文、结果、用量、费用和执行事件不再复制进账本。** 列表、详情及统计读取账本关联后，从 session 的 events.jsonl 按 harness_run_ids 得到这些内容。问题 pending/replied/expired 和运行中的 waiting 状态同样由公共事件还原，不依赖可丢的 log。账本中的终态和 client 错误用于区分执行结果与调度中断。

input 摘要仅用于尚未受理时的任务列表，限制长度；完整执行输入由 accepted 保存。接口仍可返回聚合后的 output、usage、cost_usd 和 events，但这些是读取结果，不是另一份持久化执行事实。

## 结果交付、恢复与回收

向 server 回报前，账本保存 pending_report 的业务协议头（包括 event_seq 和 attempt），不重复保存结果正文。重启或重连时按原关联从 events.jsonl 重建 payload，使用原身份补报；收到 event_ack 后追加确认记录。local 经过相同的确认和目录保护操作。

写入由单个 client 进程串行进行，JSONL 每行追加并 fsync。读取忽略尚未写完的最后一行；后续追加前截去不完整尾部。完整记录损坏则明确报错，不猜测恢复。多文件之间没有数据库事务，不能把磁盘快照视为 server 已确认任务的证明。

启动恢复逐个还原每个文件中的所有 attempt，建立可重建的内存索引；未正常完成的任务标记 interrupted，不自动重放。若适配器已写 accepted 而 client 来不及保存关联，恢复时只扫描该任务 events_offset 之后的公共事件补齐关联，不能误认此前的交互。任何 attempt 仍待确认时，都保护整个 conversation 目录，不能只检查最后一次 attempt。

声明 quota 后，回收整个空闲 conversation，包含 workspace、session、账本和资源；运行、等待答案、待确认或即将启动的任务均保护目录。不删除 server 历史或文件。

## 已有记录迁移

启动时将旧的 runs/<run_id>/<attempt_id>/run.json 转换为单文件账本，核对并复用已有适配器事件；只移除已转换的已知旧文件和空目录，不递归删除未知文件。非空共享历史迁入 resources/。events.jsonl 和 events.jsonl.d/ 均不修改。

适配器接入前的旧记录没有可引用的事件源，或唯一事件源已丢失时，以一次 legacy_import 保存唯一的历史输入和结果，避免丢数据。这仅是已有数据转换；新执行始终使用元数据账本加适配器事件，不继续写旧格式。
