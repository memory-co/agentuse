# Session 与 Run：共享历史、任务分配和一次执行

> 本文描述当前实现，与 [Server 设计](server.md) 配套。数据定义以 [基础 schema](../../../server/internal/store/store.go)、[run schema](../../../server/internal/store/run_schema.go) 和 [Go SDK 模型](../../../sdk/golang/types.go) 为准。

Session 是一段持续存在的共享会话；run 是某个 agent 在这段会话里承担的一次任务。Session 可以没有 run，也可以先后或同时有多个 run。Agent 的选择发生在每条消息上，具体执行信息保存在 run 中。

理解这两者还需要 message、delivery 和 attempt。它们分别回答五个问题：

| 对象 | 回答什么 | 持久化位置 |
|---|---|---|
| session | 这些交流属于哪段共享历史？ | `sessions` |
| message | 用户说了什么，或者 agent 最终回复了什么？ | `messages` |
| delivery | 这条用户消息交给哪个 agent，交到哪里，处理到哪一步？ | `message_deliveries` |
| run | 哪个 agent 使用哪个 daemon/harness 承担这次任务？ | `session_runs` |
| attempt | 这个 run 当前由哪一次实际执行持有？ | run 的 `attempt_id / lease_until`；事件、问题等也带 attempt_id |

## 一、关系图

![Session、Message、Delivery、Run 与 Attempt 的关系](../../images/session-run.svg)

图中的 message/delivery/run 分支同属一个 session。delivery 连接的是任务消息和执行 run；不是每条消息都必须有 delivery。

### 1.1 Session 包含什么

一个 session 拥有共享的 messages、runs、deliveries、events、questions 和 attachments，另有所有者、创建者、标题、状态和可选的 work 归属。

Session 不保存“当前只属于哪个 agent”的绑定。多 agent 模式下，每条消息显式提供 `target_agent_ids`；server 不从 prompt 中解析 `@名字` 来决定执行目标，也不在服务端替调用者继承上一条消息的目标。网页中的默认目标继承是前端交互行为。

没有 `session_participant`，也没有每个 agent 一份独立历史。不同 agent 从同一个 session 读取历史；它们在不同时间开始工作，所以实际读到的历史末尾可能不同。

### 1.2 Message 是内容，Delivery 是分配

用户发送一次消息，`messages` 只插入一行。不论 @ 了几个 agent，内容都不复制。

每个去重后的目标 agent 对应一条 delivery，唯一约束是 `(message_id, agent_key)`。delivery 保存 `message_id / session_id / agent_key / run_id / kind / status`：

- `kind=new_run`：这条分配创建了一个新的 run，作为它的主任务。
- `kind=steer`：这条分配加入已有的活跃 run；可能被领取时合并、运行中插入，或者结束后转入后续 run。
- `status`：这次分配是否已接收，以及最后如何结束，与 kind 分开记录。

`target_agent_ids=[]` 时，仅保存共享消息，不创建 delivery 和 run。这条备注以后可以出现在 agent 读取的历史中，但没有主动分配给任何 agent 执行。

### 1.3 Run 是任务，不等于一条消息

Run 创建时确定 session、主任务 message、agent、daemon 和 harness，并保存 agent key/name/owner 等快照。后续关联的补充消息通过 delivery 找到这个 run。

| 关系 | 当前规则 |
|---|---|
| session → message | 一个 session 有 0 到多条消息 |
| session → run | 一个 session 有 0 到多个 run |
| user message → delivery | 每个目标一条，可以为 0 |
| run → delivery | 一个 run 可以消费多条不同消息的 delivery |
| user message → run | 通过 delivery，可以关联多个 agent 的 run |
| session + agent → 活跃 run | 最多一个，活跃指 pending/running/waiting |
| 同一 agent → 不同 session 的 run | 可以各有活跃 run；实际领取仍受 daemon ready 状态限制 |
| run → attempt | 每次重新领取生成一个新 attempt；run 行只保存当前 attempt |

`session_runs.message_id` 是创建 run 的主任务 ID，**不是 run 消费过的全部消息**。全部分配要看 `message_deliveries`。`result_message_id` 指向最终 assistant 结果；进度、计划和插话确认保存在 `run_events`，不都变成普通聊天消息。

### 1.4 Attempt 是执行身份，不是新的 Run

Run 刚创建时 pending，attempt_id 为空。daemon 领取后，server 生成 `ra_...`，把 run 改为 running 并设租约。

租约过期或重连对账发现执行已丢失时，原 run 可以重新变成 pending。再次领取仍使用同一个 run_id，但生成新的 attempt_id。旧 attempt 的事件和问题可保留在各自表中；当前实现没有单独的 `run_attempts` 历史表。

这与“未消费插话创建后续 run”不同：**重试换 attempt，后续任务换 run。**

## 二、发消息时，什么时候创建 Run

请求例子：

```http
POST /api/sessions/ss_demo/messages
Idempotency-Key: task-001
Content-Type: application/json

{"prompt":"修复登录问题，并评审修改","target_agent_ids":["coder","reviewer"]}
```

[`runs.go`](../../../server/internal/service/runs.go) 的 `sendMultiTx` 在一个事务中保存消息，并分别为 coder、reviewer 查本 session 的活跃 run。

| 目标当前状态 | 创建什么 | 后续行为 |
|---|---|---|
| 没有活跃 run | pending run + new_run/pending delivery | 叫醒 daemon，等 ready 后领取 |
| 已有 pending run | steer/pending delivery，关联已有 run | 连续文本 prompt 可在领取时合并；结构化 input 不会无条件拼进去 |
| 已有 running run | steer/pending delivery，关联已有 run | 提交后尝试推送插话，等待确认 |
| 已有 waiting run | steer/pending delivery，关联已有 run | 等恢复 running 后再尝试，或结束后回退到后续 run |

目标最多 8 个。同一消息的目标去重后，在一个事务内全部处理；一个目标校验失败，整条消息及所有分配一起回滚。

已有活跃 run 的约束来自部分唯一索引 `runs_active_agent`，并由单连接事务保护。这里限制的是 **session + agent**，没有禁止同 session 的不同 agent 各自有活跃 run。

## 三、用一段会话看清关系

下面的 `M1 / R1 / A1` 等是示意编号；实际消息是整型 ID，run/attempt 是带前缀的随机 ID。

### 3.1 第一条消息 @ 两个 agent

Session S 中发 M1：“修复并评审”，目标 coder、reviewer；两者都没有活跃 run。

| 对象 | 新增记录 |
|---|---|
| message | M1，user，只保存一次内容 |
| run | R1：coder，主任务 M1；R2：reviewer，主任务 M1 |
| delivery | D1：M1 → coder → R1，new_run；D2：M1 → reviewer → R2，new_run |

R1/R2 由各自 daemon 领取，形成各自 attempt。它们可以同时执行；如果位于同一 daemon，则按该连接 ready 的节奏领取，不能仅凭两个 pending run 判断已经并行开跑。

这条消息同时触发两个 agent，不自动建立“先 coder 后 reviewer”的依赖。若评审必须看到 coder 最终产物，应在结果出现后再给 reviewer 发消息。

### 3.2 R1 还在运行，补充消息只 @ coder

发 M2：“注意兼容移动端”，目标 coder：

| 对象 | 变化 |
|---|---|
| message | 新增 M2 |
| run | 仍是 R1/R2，不新建 R3 |
| delivery | 新增 D3：M2 → coder → R1，steer/pending |

Server 尝试对 R1 当前 attempt 发 steer。daemon 真正注入并上报 `steered` 后，D3 才变 delivered。R1 主任务仍是 M1，补充任务通过 D3 追踪。

Reviewer 没被 M2 主动触发。它下一次读取共享历史时可能看到 M2，但当前正在运行的上下文不会因为数据库多了一条消息就自动刷新；实时注入需要明确的目标和执行端确认。

### 3.3 插话送达，R1 正常完成

D3 已 delivered；R1 返回成功结果：

- 新增 assistant 消息 M3，`message_authors` 记录其作者是 coder、来源 R1。
- R1 改 succeeded，`result_message_id=M3`，清租约。
- D1 和 D3 改 succeeded，表示 M1/M2 给 coder 的分配都由 R1 完成。
- R2 仍独立推进；它没结束时，整个 session 也未必 idle。

所以，“M1 处理完了没有”要同时看 D1、D2；只看 R1 成功还不够。

### 3.4 插话没有被消费，R1 就结束了

假设 D3 仍 pending，也没有被本 attempt 准备并成功消费：

1. R1 的终态和结果照常保存。
2. 正常完成/失败路径尝试创建后续 R3，主任务是 M2。
3. **D3 保持同一个 delivery ID**，run_id 从 R1 改为 R3，第一个待处理 delivery 的 kind 改为 new_run。
4. R3 被领取、执行，最后 D3 才进入终态。

没有再复制一条 M2，也没有把没接收的插话记为 R1 已处理。无法建立后续执行时，delivery 记失败；用户取消则终止相关分配，不自动继续这些插话。

这也说明：delivery 的 run_id/kind 会变化，它是当前任务归属，不是不可变的投递审计日志。

### 3.5 R1 超时后重新执行

R1 的 attempt A1 租约过期，server 把 **R1 本身**重排为 pending；重新领取变为 A2。

```text
S / R1 / A1：租约过期
S / R1     ：回到 pending
S / R1 / A2：重新领取并执行
```

这里没有 R3。旧 A1 晚到的新事件不能更新 A2 的状态。与之相对，3.4 是 R1 已结束、仍有未消费任务，所以创建新的 R3。

## 四、三种状态分别是什么意思

### 4.1 Run 状态

| 状态 | 意义 | 常见下一步 |
|---|---|---|
| pending | 已建立任务，等待领取 | running；也可能取消或因运行时不可用失败 |
| running | 有当前 attempt 和租约 | waiting / succeeded / failed / cancelled；超时可回 pending |
| waiting | 当前执行在等用户回答问题 | 收到回答确认后 running；也可能终结或超时重排 |
| succeeded | 此 run 成功结束 | 终态；未消费任务可能另建后续 run |
| failed | 此 run 失败结束 | 终态；正常失败路径仍可为未消费输入建立后续 run |
| cancelled | 被用户等取消 | 终态；不继续关联的待处理插话 |

回答问题的 HTTP 请求只表示答案已保存并尝试推送，不会立刻把 waiting 改 running；执行端上报 answered 等事件后才推进相应状态。

### 4.2 Delivery 状态

| 状态 | 意义 |
|---|---|
| pending | 已分配，尚未确认消费，或等待后续执行 |
| delivered | 本次输入已由执行端接收；不是成功终态 |
| succeeded / failed / cancelled | 这条消息给这个 agent 的分配已经终结 |

`started` 确认当前 prepared_attempt 的输入；`steered` 确认对应 steer_id。run 成功结束时，本 attempt 已准备的输入也可被归为成功，即使缺少独立的 started 确认。没有被消费的 pending 输入按后续任务规则处理。

“消息 HTTP 201”“delivery delivered”“run succeeded”分别代表保存、接收和完成，不能互相替代。

### 4.3 Session 状态

Session 状态由其中的活跃 run 聚合，优先级是：

```text
任一 waiting → waiting
否则任一 running → running
否则任一 pending → pending
否则保留 resolved，或为 idle
```

`idle` 只表示当前没有活跃 run，不表示所有历史任务都成功；历史 failed/cancelled 仍保留。`resolved` 是用户在空闲时做的完成标记，不是把全部 run 变为成功。继续发送消息会重新打开 session。

例如 coder 正在 running、reviewer 正在 waiting，session 展示 waiting；实际仍有 coder 在执行。展示所有执行者时应读取 runs/run_counts，不能只看 session 的一个聚合状态。

## 五、共享上下文与 Run 的边界

Session 的消息和附件是共享数据。每次执行从它们准备上下文，内置 harness 为该次执行建立原生会话，不维护“每个 agent 一个长期独立聊天上下文”的服务端实体。

领取时，`ClaimRun` 计算初始 `task_message_ids / context_seq`。随后 client 的 [`loadRunHistory`](../../../client/internal/service/session_history.go) 用 daemon 凭据及 run/attempt 头读取最新 session 快照，并把读到的最后消息 ID 更新为 context_seq；started 再把实际读取边界报回 server。因此 **context_seq 不是 run 创建时冻结的历史快照**，两个同时开始的 agent 也不保证读到完全相同的消息末尾。

- `task_message_ids` 表示本次明确要处理的任务消息。
- `context_seq` 表示这次执行准备时读到的历史上界；message 的 seq 就是 ID，不要求在单个 session 内连续。
- 运行期间的新消息不会自动刷新原生 harness 的上下文，目标明确的补充消息通过 steer 注入。
- 附件归 session，run 只是通过受保护的接口读取/上传；没有按 agent 复制一套附件库。

Session 所有者/创建者和 run 执行 agent 的所有者也可以不同；当前 dispatch 的 sender 取 session 创建者，不能当作“这条消息具体是谁发送的”的逐消息作者字段。

## 六、接口应该怎样看这些对象

| 目的 | 接口/方法 | 应关注什么 |
|---|---|---|
| 建立共享会话 | `POST /api/sessions` | 可以不带任务，先得到 session ID，上传附件后再发送消息 |
| 分配任务或插话 | `POST /api/sessions/{id}/messages` | `target_agent_ids` 和返回的 message/deliveries；没有独立 steer 写接口 |
| 读取整体会话 | `GET /api/sessions/{id}` | 一致快照中的 messages/runs/deliveries/events/questions/run_counts |
| 查看某 run | `GET /api/sessions/{id}/runs/{run_id}` | 当前状态、attempt 和该 run 的事件；插话正文由 delivery.message_id 关联 messages |
| 取消某次任务 | `POST /api/sessions/{id}/runs/{run_id}/cancel` | 只终止该 run 的分配；同 session 的其他 run 继续 |
| 回答当前执行问题 | `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` | 问题属于 run/attempt，不属于整个 session 的统一暂停点 |

[`WaitRun`](../../../sdk/golang/runs.go) 只等指定 run 到终态。若补充任务已经转到 R3，等 R1 结束不能证明补充任务结束。

[`WaitMessageRuns`](../../../sdk/golang/runs.go) 读取 session，按 message_id 找 delivery，等它们全部到终态；delivery 迁到后续 run 后仍能跟踪。这才适合回答“我刚发的这条多目标消息都处理完了吗”。目标为空时没有 delivery，该等待会直接完成，不代表执行过任务。

发送重试可使用 `Idempotency-Key`：同 session、同用户、同 key 且请求摘要相同，返回原 message/deliveries；摘要不一致则冲突。它解决重复提交，不保证租约重试时外部工具副作用只发生一次。

## 七、生命周期、删除与 Work

删除 session 会级联删除它的消息、run、delivery、event、question、请求幂等记录和附件元数据；有活跃 run 时拒绝删除。磁盘附件正文由文件清理机制处理，SQLite 级联本身不删除文件。

Work 只是为 session 增加工作归属、协调会话和协调 agent 的配置，不改变上述 message/delivery/run 模型。线程消息的所有 delivery 终结后，server 才向协调会话汇总一次；通知通过 `run_notifications` 去重。协调 run 的工作令牌限定当前 run/attempt，并在结束时撤销。

## 八、对应源码

| 问题 | 主要实现 |
|---|---|
| 保存一条消息，查已有活跃 run，创建/关联 delivery | [`service/runs.go`](../../../server/internal/service/runs.go) |
| 领取、准备输入、结束、回退插话、重排与重连 | [`service/run_lifecycle.go`](../../../server/internal/service/run_lifecycle.go) |
| started/steered/问题/终态事件与幂等 | [`service/run_events.go`](../../../server/internal/service/run_events.go) |
| 提交后派活、推送插话/取消/答案 | [`service/notifications.go`](../../../server/internal/service/notifications.go) |
| WebSocket ready/dispatch/steer 与在线状态 | [`hub/hub.go`](../../../server/internal/hub/hub.go) |
| session 一致快照 | [`service/session_snapshot.go`](../../../server/internal/service/session_snapshot.go) |
| work 汇总及工作令牌 | [`service/run_work.go`](../../../server/internal/service/run_work.go) |
| 表约束、状态聚合和查询条件 | [`store/run_schema.go`](../../../server/internal/store/run_schema.go)、[`store/queries.go`](../../../server/internal/store/queries.go) |
