# Session 中 @ 多个 agent

> 待实现设计。当前 session 仍绑定单个 agent；本方案把共享对话、参与 agent 和具体执行分开。现有接口行为在兼容阶段保持不变。

## 交互

一个 session 是一段共享对话，可以没有 agent，也可以先后邀请多个 agent。同一条用户消息可以 @ 一个或多个 agent，被 @ 的 agent 各回复一次，回复带 agent 身份，按实际完成顺序显示。

```text
用户：@coder @reviewer 看一下附件里的需求，分别说说实现和风险。
reviewer：有两个兼容性风险……
coder：建议分三步实现……
用户：@coder 按这个方案实现，把 reviewer 提到的风险一起处理。
coder：已实现，产物见附件……
用户：@reviewer 检查 coder 刚上传的产物。
```

session 不再有唯一的 agent、daemon、harness 或 harness_session_id。它们属于 session 中某个 agent 的参与记录；排队、租约、执行结果属于一次 run。

第一版只有用户或有权限的工作协调者能触发执行。agent 回复中的 `@coder` 只是文字，不自动派活，不自动形成互相回复的循环。需要后续协作时由用户继续 @，工作里的自动委派继续走已有 [work](work.md) 机制。

## 三层模型

| 对象 | 职责 | 关键字段 |
|---|---|---|
| session | 共享消息、附件、归属和完成标记 | id、user_id、created_by、work_id、title、resolved_at |
| session_participant | 某个 agent 在这个 session 中的运行时与独立上下文 | id、session_id、agent_id、agent_key、agent_name、daemon_id、harness、harness_session_id、generation、active |
| session_run | 一条用户消息交给一个 participant 的一次逻辑执行 | id、session_id、message_id、participant_id、status、attempt_id、lease_until、context_seq、result_message_id |

- 一条消息 @ 三个 agent，保存一条 user message 和三个 run。不会复制成三个 session，也不会把同一条输入重复记三次。
- 同一个 agent 在同一个 session 中只允许一条 active 的参与记录。重复 @ 去重；第一版每条消息最多指定 8 个 agent，超过返回 400。
- `agent_key`、`agent_name` 是创建参与记录时的展示快照。agent 改名、删除后，历史回复仍能认出是谁说的。
- `daemon_id`、`harness` 在首次邀请时取 agent 当前运行时，此后固定在该 participant 上；不同 agent 可以在不同机器上。
- agent 切换运行时不会偷偷移动已参与的 session。显式 rebind 会停用旧 participant、创建下一代 participant，使用新运行时和新的 harness 会话；旧消息、run、产物保留。存在未结束 run 时不能 rebind。
- 删除 agent 不删除参与记录或历史。已有 participant 可按原运行时继续使用；不能再通过已不存在的 agent_id 新增参与记录。其 `agent_id` 置空，通过 participant_id 寻址。删除 daemon 后该 participant 不再可运行，但其他 agent 不受影响。
- `session_participants`、`session_runs` 以及消息的关联表都是新增表；正式 SQL 和 store 接口在实现时补齐，本文件给出约束和迁移规则。

## @ 的路由规则

界面输入 `@` 后搜索 agent，选择后生成带 agent_id 的 mention。显示名可以重复，路由使用全局唯一的 agent_id，不能按显示名匹配。

HTTP 请求使用结构化的 `target_agent_ids`；如果要继续使用已删除 agent 留下的参与记录，或明确选择某一代运行时，使用 `target_participant_ids`。两个字段互斥，数组顺序只决定展示顺序，不决定执行顺序。mention 的文字位置由前端渲染，服务端以结构化字段为准，不从 prompt 字符串猜目标。邮件地址、代码块、引用和附件里的 `@` 都不会意外派活；脚本想调用 agent 必须传目标字段。

- 显式目标：只调用指定的 agent，不广播给其他参与者。指定未参与的 agent 时，在同一事务里创建 participant。
- 两个目标字段均省略：只有一个 active participant 时默认发给它；没有或有多个 active participant 时只记录消息，不派活。页面明确显示“仅记入会话”，发送前可以改选目标。
- 显式空数组：始终只记录消息，即使会话里只有一个 agent。可以上传文件、记笔记，再在之后的消息里 @。
- 已停用的 participant 不能直接接新 run；需显式重新启用或通过当前 agent_id 选择有效参与者。
- 任一目标不存在、不可调用、没有运行时、harness 未开启或协议不兼容，整个请求返回 400，不写消息、不创建部分 run。目标 daemon 离线且此前声明支持协议时可以排队。
- 请求成功后，各 run 独立完成或失败。一个 agent 运行中失败不撤销其他 agent 的结果。

## HTTP 接口

兼容阶段用 `mode: "multi_agent"` 创建新模型会话：

```http
POST /api/sessions
Content-Type: application/json

{"mode":"multi_agent","title":"附件能力评审"}
```

返回 201 和 `{"session": …}`，初始 idle，没有 participant 或消息，不要求 agent_id，也不需要 defer_start。可先上传 session [附件](attachment.md)。同时传 prompt/input 时按发送消息的规则原子创建第一条消息和 run。

```http
POST /api/sessions/{id}/messages
Content-Type: application/json

{
  "prompt":"看一下附件里的需求，分别说说实现和风险。",
  "target_agent_ids":["coder","reviewer"]
}
```

返回 201：

```json
{
  "message":{"id":31,"seq":12,"role":"user","target_participant_ids":["sp_coder","sp_reviewer"],"content":{"prompt":"…"}},
  "runs":[
    {"id":"sr_1","message_id":31,"participant_id":"sp_coder","status":"pending"},
    {"id":"sr_2","message_id":31,"participant_id":"sp_reviewer","status":"pending"}
  ]
}
```

没有执行目标时 runs 为 `[]`。自定义 harness 的 input 仍为原始 JSON，目标字段在外层，不混进用户 input。以 `Idempotency-Key` 重试同一用户、session 下的发送请求时返回原 message/runs；相同 key 不同请求体返回 409。key 和请求摘要、结果引用在同一事务保存，随 session 删除，避免网络超时后重复派活。

其他接口：

| 接口 | 行为 |
|---|---|
| `GET /api/sessions/{id}` | 返回 session、participants、messages、runs，以及带 run_id 的 events/steers/questions；明细数组按 seq/id 排序 |
| `GET /api/sessions/{id}/runs/{run_id}` | 查看单次执行及其事件、错误、上下文水位和结果消息 |
| `GET /api/sessions/{id}/participants` | 列出参与者，含停用和历史运行时 |
| `POST /api/sessions/{id}/participants` | `{"agent_id":"coder"}`，只邀请，不发消息；已有 active 参与者时返回该记录 |
| `DELETE /api/sessions/{id}/participants/{pid}` | 停用参与者，保留历史；存在未结束 run 时 409；重复停用 204 |
| `POST /api/sessions/{id}/participants/{pid}/activate` | 重新启用原运行时；与同 agent 的其他 active 代冲突时 409，运行时不可用时 400 |
| `POST /api/sessions/{id}/participants/{pid}/rebind` | 从该 agent 当前运行时创建下一代参与记录，返回 201；已删除的 agent 不支持 rebind |
| `POST /api/sessions/{id}/runs/{run_id}/cancel` | 取消排队或执行中的 run，返回其状态；重复取消返回同一结果，已成功/失败的 run 返回 409 |
| `POST /api/sessions/{id}/runs/{run_id}/steers` | 对指定 run 插话，不隐式发给其他 agent |
| `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` | 回答这次执行的提问，不能跨 run 回答 |

participant/run 都必须属于 URL 指定的 session，看不到会话或对象不属于该会话返回 404。participant 创建、启用、rebind 进行与发送目标相同的运行时和权限校验；同 agent 的 active 唯一性由数据库部分唯一索引保证，不能仅靠 handler 先查再写。

`resolve` / `reopen` 保留。session 有 pending/running/waiting run 时不能 resolve 或删除。已 resolved 的会话收到新消息时重新打开，包括只有笔记、没有 run 的消息。

## 状态与调度

每个 run 独立流转：

```text
pending → running → succeeded / failed
   └──────────┴────→ cancelled
              ↕
           waiting

running / waiting ──租约过期──→ pending（新 attempt）
```

`failed`、`cancelled` 都是终态，用户可再发消息 @ 该 agent；租约失效属于重派同一个逻辑 run，不新增 user message。成功和失败都产生一条带 run_id、participant_id 的 assistant 消息，唯一约束保证一个 run 只有一个最终结果消息。取消只记录 run 状态和取消事件，不伪造 agent 回复。

取消在事务中撤销当前 attempt 的执行权、移出排队，向在线 daemon 发送 cancel；daemon 停止对应进程后才能再次 ready。离线 daemon 重连时必须先停止服务端已取消/失效的 attempt，才能领新活。取消之后的旧回报和附件上传被拒绝，已经发生的工具副作用无法撤回。提供取消入口，避免离线目标一直 pending 导致会话无法完成、移除参与者或删除。

session 对外的 status 是派生值：

1. 任一 run waiting：waiting。
2. 否则任一 run running：running。
3. 否则任一 run pending：pending。
4. 全部结束时，按 resolved_at 返回 resolved 或 idle。

同时返回各状态计数，页面显示“2 个执行中，1 个等回答”，不让一个总状态遮住其他 agent。没有运行中的 run 但有失败结果时 session 仍 idle，失败显示在对应 run 上。

同一 participant 严格按用户消息的 seq 串行执行，前一个 run 结束后才能领下一个，避免两个进程同时续接同一个 harness 会话。不同 participant 可以并行；同一台 daemon 第一版仍只跑一个 run，沿用现有 ready 表达空闲，不增加本机并发。跨 session 按最早可执行 run 公平调度，忙着的 participant 不阻塞别的参与者。

用户在某个 agent 执行期间再次 @ 它，创建下一条消息和 pending run，不悄悄改成 steer。需要影响当前执行时，点该 run 的“插话”。这一区分让每条消息的目标和结果可追踪。运行时后来被关闭或删除时，只将相关 pending run 判为 failed，不影响同一消息的其他目标。

## 共享对话如何交给各个 harness

每个 participant 都有自己的 harness_session_id；coder 和 reviewer 不能共用一个 Claude session 或 Codex thread。用户看到的共享消息存在服务端，不能指望不同机器上的本地历史自动同步。

消息增加 session 内严格递增的 seq，在写事务中分配，不使用时间戳排序。assistant 消息记录其 run_id、participant_id 和触发它的 user message。首次参加的 agent 可看到之前的共享消息和已完成回复；之后继续参加时，也能看到其他 agent 在这期间的回复。

每个 run 第一次派出前冻结一份上下文清单：

- 包含触发消息之前的 user 消息、针对更早 user 消息且此时已经完成的 assistant 回复，以及触发消息本身。
- 不包含更晚的 user 消息，也不包含同一条触发消息下其他 agent 的回复，避免“谁先完成就偷偷影响同一轮另一个 agent”。需要互相评审时由用户在下一条消息里明确 @。
- 本 participant 的前一个 run 已先结束，因此它自己的前一轮结果也在清单里；更早目标尚未完成的其他 agent，其迟到回复到下一次被 @ 时再补给它。
- 保存选中的 message id 和顺序（`run_context_messages`），同时记录 context_seq。重派同一 run 复用该清单，不因为服务重启或其他 agent 新回复而改变输入。
- 参与者保存已确认进入原生上下文的 message id 集（`participant_context_messages`）。不能只记“上次最大 seq”：针对旧 user 消息的迟到回复仍可能需要补齐。续接时把清单中尚未确认的共享历史作为有作者、消息 id 的上下文交给 harness，触发本 run 的 input 单独传入。

Claude/Codex 的共享上下文通过 daemon 生成的独立上下文文件交付，说明文件给出读取路径；自定义 harness 在 stdin 的 `context_messages` 中获得同样的数据，原 input 不改。引用、文件名和别的 agent 的回复按有来源的对话数据呈现，不能提升为系统指令。context 文件归 daemon 管理，每次执行重写。

成功接收合法 attempt 的最终结果后，事务中更新已确认的上下文集、原生 harness_session_id 和结果消息。failed、cancelled 或执行状态不确定时，不推进已确认集；重派或下一次执行重建原生会话并重新注入服务端历史，保留工作目录。重建时忽略原生旧会话的已确认集，全量注入冻结清单；新原生会话成功后替换确认集，不能用旧会话水位跳过历史。租约过期可能导致工具操作重复，不能承诺模型调用或文件外部副作用恰好执行一次；attempt fencing 只保证旧执行不能污染服务端结果和续接状态。

## 工作目录和附件

新模型的工作目录为：

```text
~/.agentuse/work/<session_id>/<participant_id>/
```

同一个 participant 的各轮复用目录，不同 participant 隔离，避免不同 agent 并行改写同一套说明、上下文文件和代码。多个 agent 即使在同一台 daemon，也通过 [session attachments](attachment.md) 交换输入、产物；一个机器上的绝对路径不能交给另一个机器使用。

附件继续属于 session。每条消息接受时，在同一事务保存当时可见的附件 id 集作为本条消息的输入快照；其所有目标使用相同快照。之后上传的文件用于下一条消息，已经排队的 run 不自动扩大输入。附件元数据和内容不可变，删除导致尚未下载的输入不可用时，该 run 明确失败；已下载副本不做远程擦除。

agent 上传产物仍用 `agentuse attachment upload`，产物对 session 的其他参与 agent 在后续执行时可见。附件增加可空的 `created_by_participant_id`、`created_by_run_id`，保留现有 source/created_by/daemon_id，页面能辨认是谁交付的。daemon 在新协议下上传必须提交 run_id 和 attempt_id，提交文件前再次检查租约、归属和执行代次；不再仅凭 message_id 判断，因为同一消息可以同时跑多个 run。

用户/admin/工作令牌的附件权限仍按 session 检查；daemon 的执行权限按 participant/run 检查。支持旧 message_id 的上传格式仅用于 legacy session，不能绕过新模型的执行校验。

## daemon 协议和重派

hello 增加 `session-runs-v1` 能力声明。multi_agent 会话只派给声明该能力的 daemon；附件输入另要求 `attachments-v1`。未声明过能力不能邀请，已声明但离线可以排队；重连移除能力时，相关 pending run 明确失败。

新增 `run` 派活格式，与 legacy 的 `message` 并存：

```json
{
  "type":"run",
  "run":{
    "id":"sr_1","attempt_id":"ra_1","message_id":31,
    "session_id":"ss_1","participant_id":"sp_coder",
    "harness":"claude","resume":"native-session-id",
    "context_seq":12,"context_messages":[{"id":20,"role":"assistant","participant_id":"sp_reviewer","content":{"output":"…"}}],
    "attachment_ids":["at_1"],"input":{"prompt":"…"}
  }
}
```

- hello 的 running 列表扩展为活动 `(run_id, attempt_id)` 列表；heartbeat/event/steer/answer 都携带两者，message_id 只作消息关联。
- 每次领取生成新的 attempt_id，租约挂 run。续租、结果、提问回答、steer 确认、附件上传都必须匹配当前 attempt。
- 旧 attempt 的 done/failed 或上传返回冲突，不写 assistant 消息，不更新原生续接 id，也不影响已重派的 run。daemon 收到 stale 错误就停掉该 attempt。
- event 增加 attempt 内的递增 event_seq，服务按 `(run_id, attempt_id, event_seq)` 去重。断线 outbox 重发不会重复记进度或结果。
- 终态写入和结果唯一约束在同一事务；重试已接受的同一最终回报返回已接受结果，不能再次触发工作通知。
- daemon 重连只保留仍匹配服务当前 attempt 的执行，其余停止，并在停止完成后才能发 ready。服务重启不生成新逻辑 run；到期后才重派。

harness 进程增加 `AGENTUSE_PARTICIPANT`、`AGENTUSE_RUN`、`AGENTUSE_ATTEMPT`，原 AGENTUSE_SESSION、AGENTUSE_MESSAGE_ID 保留。CLI 读到 run/attempt 时使用新格式上传，并支持 `--run` / `--attempt` 显式覆盖；不带这些字段的旧客户端只允许操作 legacy 执行。HTTP 下载仍可由 daemon 在服务端授权后进行，不把用户登录令牌放进 harness。

## 归属与权限

新 session 的 user_id 是创建者，created_by 同样记录创建者；在工作内时归工作主人。邀请别人的 agent 不转移 session 归属，也不自动把整段会话的人类读取、回复、删除权限送给 agent 主人。第一版不引入人类群成员、邀请链接或多人协作编辑；只有 session 主人/admin，以及范围内的工作令牌能管理它。

普通 session 可以使用当前允许调用的其他用户的 agent，沿用现有“可以用任何人的 agent”规则；work 内仍只能邀请工作主人自己的 agent。选择外部 agent 意味着把该次执行的共享历史和输入附件交给其 daemon 所在机器处理，页面显示 agent 主人和机器，供用户选目标。未被邀请的 agent/daemon 无权读取会话内容。

daemon 使用 dk_ 只能读取已分配给自己的有效 participant 对应的执行上下文及 session 附件，不能借 participant 身份调用 session 管理接口，也不能读取其他 daemon 的 run。用户移除 participant 后，daemon 不能再开始新的读取；已下载副本仍受本地保留规则影响。产物上传要求当前 run/attempt 有效。仅有相同 message_id 或猜中 session_id 都不能得到执行权。

## 与工作、插话和提问的关系

- work 仍通过 coordinator_id 指向一个 session；另增加 coordinator_participant_id，明确只有哪个 participant 具备协调者身份。这个会话里 @ 其他 agent 不会把它们变成协调者。
- 工作令牌绑定协调者的 run 和 attempt，有效期随该执行结束；只有该 participant 的派活包含 work_token。工作令牌可以在本工作会话中选择允许的 agent，仍不能访问其他工作或普通会话。
- 线程的一条 user 消息如果派给多个 agent，等这些 run 全部进入终态后，向协调者发送一次汇总通知，逐个署名列结果和失败原因。通知用 `(thread_session_id, message_id)` 唯一键去重；一个目标离线 pending 时不假装整轮结束，页面持续展示未完成目标；用户取消该目标后将取消结果纳入汇总。
- steer 和 question 增加 run_id/attempt_id，页面在对应 agent 的执行卡片下操作，也提供独立的取消按钮。提问超时、daemon 掉线和答案转插话只影响该 run，不影响其他 agent。
- 普通 messages 接口不再根据 session 总状态猜“这是下一轮还是插话”；multi_agent 模式下始终创建正式消息/排队执行，插话必须走指定 run 的接口。

## 兼容迁移

分两步上线，不能直接把 sessions.agent_id 改成数组后继续沿用旧租约和目录。

1. **并存**：sessions 增加 mode（legacy / multi_agent），现有记录和未传 mode 的旧创建请求按 legacy 处理。网页新建会话和新版 SDK 显式传 multi_agent。legacy 接口响应、忙时 steer、单一 daemon 权限、附件上传和工作目录保持原样，旧 daemon 继续工作；新的表和协议用于 multi_agent。
2. **升级已有会话**：增加 `POST /api/sessions/{id}/upgrade`，只允许 session 空闲、没有 pending steer/question 时操作。事务中把原 agent/runtime/resume 建成一个 participant，回填历史消息作者和 seq，旧 user 消息的默认目标关联到它；导入的历史结果是终态 run，不重跑。已有 agent 被删也能创建保留快照的 participant。

升级后原有读取权限必须保留：旧 user_id 和 created_by 都登记在 session_access 授权记录中（read/write/delete，与旧语义一致），再把 session 主归属确定为原发起人；不能因为所有权模型改变让原 agent 主人突然失去原会话。新建 multi_agent 会话没有这种自动授权，session_access 暂不提供一般的成员邀请接口。

迁移的第一个 participant 保存 legacy_workdir 标记，继续用原 `~/.agentuse/work/<session_id>/` 和已有原生续接 id；新加入的参与者使用子目录。daemon 只能在当前 session 根路径内解析该标记，不能接受任意客户端提供的绝对路径。只在有明确本机迁移流程时才搬目录，否则不能假定移动目录后仍能续接原生会话。

工作会话由整个 work 一起升级，原协调者映射到 coordinator_participant_id；任一会话未空闲就整体 409。升级是显式操作，事务失败不留下半套参与记录。切换默认创建模式和移除旧 sessions runtime 字段另行安排 API/SDK 版本，不在第一阶段静默改变旧客户端行为。

## 页面、客户端和 SDK

新建 session 先显示对话和附件，不强制选一个 agent。输入框支持多选 @、展示 agent 的主人和运行时；选中目标后明确显示谁会收到这一条消息。会话顶部显示 active 参与者，各个 assistant 回复、进度、计划、提问和产物都有署名。

同一条输入下展示各目标的排队/执行/失败状态，用户可以只关注失败 agent，下一条消息只 @ 它，不必重新跑全部 agent。不同 agent 的回复按完成顺序排列，不为了等待某一个目标把已完成结果藏起来。停用 participant 不隐藏历史。

SDK 增加 Participant、Run、目标数组和 `{message, runs}` 响应；提供 WaitRun、WaitMessageRuns，区分“某个 agent 回完”与“这条消息所有目标回完”。multi_agent 的 WaitSession 默认等待所有 run 终态，若指定遇到提问即返回，则同时返回所有等待中的 run，不能只返回一个问题。旧模式的方法和返回结构继续保留。

CLI 增加 `agentuse session send <id> --agents coder,reviewer "…"`、`agentuse session runs <id>` 和面向 run 的读取/等待操作；`attachment` 命令自动带当前 participant/run/attempt。work CLI 的 new/send 允许显式传多个目标；原 --agent 映射为单元素目标，原有协调者使用习惯可以保留。

## 实现顺序与验收

1. session mode、新增参与记录/run/上下文/消息目标/输入附件关联表、seq、唯一约束和用户访问授权，先不改 legacy 调度。
2. 多目标 API、幂等发送、run 状态聚合和调度，确保同 participant 串行、不同 daemon 可并行。
3. run/attempt 协议、上下文同步和原生续接，改造心跳、结果去重、stale 处理。
4. 附件权限与来源归属、work 汇总通知、指定 run 的 steer/question。
5. 网页 @、署名和并行状态；CLI 与三个 SDK；最后加入显式升级流程。

验收至少覆盖：

- 一个 session 先 @ A，再 @ B，B 能看到 A 的历史回复；A/B 各自续接，互不覆盖原生会话和目录。
- 同一消息 @ A/B，输入上下文与附件选择遵守快照规则，两份结果各有作者；一个离线、失败或等待回答不阻止另一个完成。
- A 忙时继续 @ A/B，A 严格串行，B 可以先执行；后续 user 消息不会泄露进前一条 run 的输入。
- 离线 pending run 可取消，取消后能移除参与者或删除会话；执行中取消先撤销执行权，重连的旧进程停止后才再领活。
- 同一次发送超时重试不重复创建 run；旧 attempt 的心跳、结果和附件都被拒绝；outbox 重发不重复记事件或通知。
- agent 改名、删除、切换运行时、daemon 删除/降级，历史身份和其他参与者均不受破坏。
- 跨用户 agent 的选择不扩大人类 API 权限；移除 participant、工作令牌过期、跨 session/run/qid/附件的访问都按边界拒绝。
- 旧客户端/daemon 完整回归；空闲单 agent 会话升级后能原目录续接，原主人和发起人的权限保持；工作不能部分升级。
