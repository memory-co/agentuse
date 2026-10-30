# Session 中 @ 多个 agent

> 待实现设计。当前 session 仍绑定单个 agent；本方案让 session 保存所有 agent 共用的消息和附件，每条消息通过 @ 指定执行者。

## 交互

一个 session 是一段共享对话，可以不指定 agent，也可以在每条消息里 @ 一个或多个 agent。被 @ 的 agent 各执行一次，回复带 agent 身份，按实际完成顺序显示。

```text
用户：@coder @reviewer 看一下附件里的需求，分别说说实现和风险。
reviewer：有两个兼容性风险……
coder：建议分三步实现……
用户：@coder 按这个方案实现，把 reviewer 提到的风险一起处理。
coder：已实现，产物见附件……
用户：@reviewer 检查 coder 刚上传的产物。
```

所有 agent 干活时都看这个 session 里的消息、其他 agent 的回复和附件。session 不维护 agent 名单，不需要加入、退出、启用或重新绑定操作。@ 直接选择一个可调用的 agent。

第一版只有用户或有权限的工作协调者能触发执行。agent 回复里的 `@coder` 是文字，不自动派活；工作里的自动委派继续走 [work](work.md) 机制。

## 数据模型

| 对象 | 职责 | 关键字段 |
|---|---|---|
| session | 所有执行者共用的对话、附件、归属和完成标记 | id、user_id、created_by、work_id、title、resolved_at |
| message | 一条共享消息及其作者、目标 | id、session_id、seq、role、target_agent_ids、run_id（assistant）、agent_key、agent_name、content |
| session_run | 一条用户消息交给一个 agent 的一次逻辑执行 | id、session_id、message_id、agent_id、agent_key、agent_name、daemon_id、harness、status、attempt_id、lease_until、result_message_id |

- 一条消息 @ 三个 agent，保存一条 user message 和三个目标投递关系；空闲目标新建 run，忙碌目标复用其未结束 run 作为插话。消息和附件仍只有一份。
- run 记录“这次由谁执行”，不创建 session 与 agent 的长期绑定。一条消息里重复 @ 同一个 agent 去重；第一版每条消息最多指定 8 个 agent。
- 创建 run 时读取 agent 当前的 daemon/harness，保存本次运行时快照。agent 切换运行时后，新 run 使用新的运行时，已经排队或执行的 run 保持原快照；需要改派时取消原 run，再发消息。
- `agent_key`、`agent_name` 是当时的身份快照，agent 改名、删除后仍能识别历史作者。agent_id 可用外键在删除后置空，展示快照保留。
- agent 删除后不能再 @ 它；它的 pending run 标为 failed，已经执行的 run 可以按原快照完成。daemon 删除、harness 被关闭或能力降级时，仅使相关执行失败，不影响其他 agent 的结果。
- 新模型不保存 session 或 session-agent 级的 harness_session_id，不保存每个 agent 的历史水位、已读集合或私有消息视图。原生 harness 返回的会话 id 如需排查，只记在本次 run 的执行日志里。
- 新增 session_runs、消息目标关联表（含 run_id、投递方式、状态）和必要的执行事件字段；不增加 agent 参与关系表或按 agent 划分的上下文表。正式 SQL 随实现补齐。

## @ 的路由规则

界面输入 `@` 后搜索 agent，选择后生成携带 agent_id 的 mention。显示名可以重复，服务端路由始终使用全局唯一的 agent_id。

HTTP 请求使用 `target_agent_ids`，服务端不从 prompt 字符串猜目标。邮件地址、代码块、引用、附件和 agent 回复里的 `@` 不会意外派活；脚本调用 agent 必须传结构化目标。

- 目标非空：对每个 agent 分别判断当前 session 中是否有未结束 run。没有则创建 run；已有 pending/running/waiting run 则把这条消息作为插话关联到它，不再创建第二个 run。
- 同一条消息可以同时是给 A 的插话、给 B 的新任务。只检查这个 session 中该 agent 的执行，不会插到它在另一个 session 的任务里。
- 目标省略或为空数组：只记录消息，不执行。即使之前只用过一个 agent，也不从历史推断默认目标。前端可以保留输入框里用户选中的 @，但每次请求都显式带目标。
- 不需要提前邀请 agent，也不根据以前是否出现过来限制目标。
- 任一目标不存在或不可调用，整个请求返回 400。需要新建 run 的目标还须校验运行时、harness 和协议能力；插话目标校验已有 run 的有效性。不写入部分消息/目标。daemon 离线但此前声明支持协议时可排队，已存在的 pending run 继续收补充消息。
- 请求成功后，各 run 独立完成或失败；一个 agent 的执行失败不撤销其他 agent 的结果。

## HTTP 接口

兼容阶段用 `mode: "multi_agent"` 创建新模型会话：

```http
POST /api/sessions
Content-Type: application/json

{"mode":"multi_agent","title":"附件能力评审"}
```

返回 201 和 `{"session": …}`，初始 idle，不要求 agent_id，也不需要 defer_start。可以先上传 session [附件](attachment.md)。同时传 prompt/input 时，按发送消息的规则原子创建第一条消息及相应投递关系。

```http
POST /api/sessions/{id}/messages
Content-Type: application/json

{
  "prompt":"看一下附件里的需求，分别说说实现和风险。",
  "target_agent_ids":["coder","reviewer"]
}
```

返回 201，统一表示共享 message 已创建；下面示例中 coder 空闲、reviewer 正在执行：

```json
{
  "message":{"id":31,"seq":12,"role":"user","target_agent_ids":["coder","reviewer"],"content":{"prompt":"…"}},
  "runs":[
    {"id":"sr_1","message_id":31,"agent_id":"coder","status":"pending"}
  ],
  "deliveries":[
    {"agent_id":"coder","run_id":"sr_1","kind":"new_run","status":"pending"},
    {"agent_id":"reviewer","run_id":"sr_0","kind":"steer","status":"pending"}
  ]
}
```

runs 只列本次新建的执行，deliveries 列每个目标的投递方式和当前关联的 run。全是插话时 runs 为 `[]`；没有目标时两个数组都为空。消息及目标投递关系始终只存一份，插话也写进共享 messages，不另外复制 user 消息。自定义 harness 的 input 仍是原始 JSON，目标字段在外层，不混进 input。使用 `Idempotency-Key` 重试同一用户、session 下的发送请求时返回同一条 message，不重复创建 run，deliveries 按当前投递关系和状态返回；相同 key 不同请求体返回 409。key、请求摘要和结果引用在同一事务保存，随 session 删除。

| 接口 | 行为 |
|---|---|
| `GET /api/sessions/{id}` | 返回 session、messages、runs，以及带 run_id 的 events/steers/questions |
| `GET /api/sessions/{id}/runs/{run_id}` | 查看本次执行的 agent、运行时、状态、事件和结果消息 |
| `POST /api/sessions/{id}/runs/{run_id}/cancel` | 取消排队或执行中的 run；重复取消返回同一状态，已成功/失败的 run 返回 409 |
| `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` | 回答指定 run 的提问，不能跨 run 回答 |

插话也只走 `POST /api/sessions/{id}/messages` 和 target_agent_ids，不单独暴露 steers 写接口。内部 steer 仍用于将消息送进正在运行的 harness。

不提供 session 下的 agent 加入、退出、激活或 rebind 接口。历史上调用过哪些 agent，由消息和 run 派生展示，不成为权限或调度依据。

run 必须属于 URL 指定的 session，看不到会话或对象不属于该会话返回 404。`resolve` / `reopen` 保留；有 pending/running/waiting run 时不能 resolve 或删除。已 resolved 的会话收到新消息时重新打开，包括只有笔记、没有 run 的消息。

## 所有 agent 使用同一份 session 内容

session 是消息和附件的唯一来源。用户消息、所有 agent 的回复按 session 内严格递增的 seq 排序，seq 在写事务中分配，不依赖时间戳。assistant 消息记录 agent 身份、run_id 和触发消息。

每次执行开始时，服务从同一个 session 读取当时已有的完整对话和附件，交给本次执行；不会为不同 agent 筛选“自己的历史”，也不按 agent 保存增量上下文。没被 @ 的历史消息、其他 agent 的回复和产物同样可见。

- 每个 run/attempt 都启动新的原生 harness 会话，传入共享历史和本次任务，不使用其他 run 遗留的 Claude session、Codex thread 或 resume id。
- 派活明确指出触发本 run 的 message_id，以及这次分配给它的 task_message_ids（含启动前已收到的补充消息），防止 agent 把共享历史里另一条消息误当成本次任务。触发消息只作为共享历史中的一条消息出现，input 另作执行目标引用，不把同一段内容重复追加成新的对话。
- 不同 agent 的执行开始时间可能不同，后开始的 agent 可以看到已经写入 session 的新回复。同一条消息 @ 的多个 agent 不强制使用冻结的相同历史快照。
- 一次模型调用开始后，再发消息 @ 正在执行的 agent 就是插话，由服务按目标路由到当前 run；没 @ 它的消息不自动注入。agent 也可通过 `agentuse session read` 主动读取 session 最新内容。
- 重派时重新读取 session 当前内容，并启动新原生会话。run 可记录每个 attempt 开始读取到的 context_seq 和附件 id，供排查；这些只是执行日志，不是另存一套上下文，也不要求重派复用旧快照。
- session 变长时，如需截断或摘要，按 session 统一的规则生成、保留消息来源和本次任务，不能为每个 agent 维护不同的记忆。

Claude/Codex 通过 daemon 生成的 session 历史文件读取共享对话，说明文件写明路径和本次任务；自定义 harness 在 stdin 的 `session_messages` 中获得同样的数据，用户原 input 保持不变。历史引用、附件内容和其他 agent 的回复都是有来源的对话数据，不提升为系统指令。

执行中的工具临时输出可以留在本次进程内；需要交给其他 agent 的结论、文件和后续工作依据必须写回 session 消息或附件，不能依赖私有原生会话的隐藏记忆。

## 状态与调度

每个 run 独立流转：

```text
pending → running → succeeded / failed
              ↕
           waiting

pending / running / waiting → cancelled
running / waiting ──租约过期──→ pending（新 attempt）
```

成功和失败都产生一条带 run_id 和 agent 身份的 assistant 消息，一个 run 只有一个最终结果消息。取消记录状态和取消事件，不伪造 agent 回复。

session 的 status 为派生值：任一 run waiting 时为 waiting；否则依次按 running、pending、resolved/idle 返回。同时返回各状态计数，页面能显示“2 个执行中，1 个等回答”。失败只显示在对应 run 上，不把整段 session 标为失败。

不同 daemon 可以并行执行。同一台 daemon 第一版仍只跑一个 run，沿用 ready 表达空闲；如果以后提高本机并发，同一 `(session_id, daemon_id)` 仍串行，避免同时改写同一个 session 工作目录和说明文件。这里是执行资源调度，不产生 session-agent 上下文或长期绑定。

### 发送消息即插话

是否插话按每个目标判定，不按 session 总状态判定，也不要求用户传 run_id。保存 message、查找未结束 run、创建目标投递关系在同一个写事务里完成；与 run 结束事务串行化。结束先提交就新建 run，消息先提交就关联为插话，不能漏掉或同时创建两个 run。

- **pending**：补充消息挂到尚未派出的 run。执行开始时，可合并的 prompt 与主任务一起从共享 session 读取，并在 task_message_ids 中标出；任意 JSON input 留待后续单独执行。daemon 确认启动并接收这些消息后标为 delivered，不再重复通过 steer 推送。
- **running**：内部发 steer，携带补充消息 id、run_id 和 attempt_id；harness 接收后确认，投递状态变成 delivered。
- **waiting**：消息先挂到当前 run，等提问回答后在 harness 可接收插话时送达；不因为等待回答就创建第二个 run。
- **harness 不支持插话或来不及送达**：当前 run 成功/失败结束时，尚未 delivered 的消息自动排到下一次执行。按 seq 排序，将连续的文本补充合并成一个后续 run，复用原有 message，不追加重复 user 消息；将这些目标投递关系改挂到新 run。创建后续 run 时校验 agent 当前运行时，不可用则把相应投递明确标为 failed。
- **租约过期**：这是重派当前逻辑 run，保留未完成的投递关系；新 attempt 从共享 session 读取待处理任务，旧 attempt 的确认无效。
- **显式取消**：当前 run 和仍挂在它上面的投递一起取消，不因为取消而自动再启动后续 run。用户需要继续时重新发消息 @。
- **自定义 input**：消息仍只保存一次。能被 harness 接收的文本 prompt 可以插话；任意 JSON input 不强行拼成 prompt，排队等当前 run 结束后按原始 input 执行。多个此类 input 按 seq 逐个执行，不合并成一个 JSON，也不跨过它们合并前后的文本。其余投递保留在消息目标队列，前一个 run 结束后才创建下一个，仍满足每个 session-agent 最多一个未结束 run。

这些都是消息目标的投递状态，不是 agent 独立上下文。已经 delivered 的插话由当前 run 的最终回复统一回答，可以让一条结果关联多条用户消息，不要求每次插话再生成一条 assistant 回复。

取消在事务中撤销 attempt 的执行权并移出排队，向在线 daemon 发 cancel。daemon 停止进程后才能再次 ready；离线重连也先停止已失效 attempt 再领活。已发生的工具副作用不能撤回；执行权撤销后拒绝旧结果和附件上传。这样离线目标不会一直 pending，阻止 session 完成或删除。

## 工作目录和附件

工作目录继续按 session 划分：

```text
~/.agentuse/work/<session_id>/
```

同一台 daemon 上，不同 agent 执行同一 session 时使用这个目录，每次执行重写共享历史文件和当前 harness 所需的说明。目录按 session 保留，不按 agent 分目录。切换 harness 时也不续接上次原生会话。

不同机器上的目录是 session 的本地工作副本，不是自动同步的共享文件系统。需要跨机器交付的文件上传为 [session attachment](attachment.md)，由其他 agent 下载使用。不能只在回复中给出某台机器的绝对路径。

附件继续只归属 session，每次执行开始取得当前附件列表。之后上传的文件，后开始的执行能看到；正在执行的 agent 如需使用，可以通过 CLI 主动列出并下载。不要求按目标 agent 或触发消息冻结附件快照。

产物用 `agentuse attachment upload` 上传，附件增加可空的 `created_by_run_id`，agent 身份从 run 的快照取得。原 source/created_by/daemon_id 保留。daemon 上传在新协议下必须带 run_id、attempt_id，提交文件前再次验证执行权；不能只凭 message_id，因为同一消息可有多个执行。

multi_agent 会话在尚未 @ 任何 agent 时也允许用户上传附件。上传不检查“session 绑定的 daemon 是否支持附件”；选择执行目标时才校验该 run 的 daemon 能力。legacy 模式保留当前附件行为。

## daemon 协议和执行权限

hello 增加 `session-runs-v1` 能力；有附件的执行另要求 `attachments-v1`。只把新模型 run 派给支持新协议的 daemon，旧 `message` 协议继续服务 legacy 会话。

```json
{
  "type":"run",
  "run":{
    "id":"sr_1","attempt_id":"ra_1","message_id":31,
    "session_id":"ss_1","agent_id":"coder","harness":"claude","task_message_ids":[31],
    "context_seq":12,
    "session_messages":[{"id":20,"seq":11,"role":"assistant","agent_key":"reviewer","content":{"output":"…"}}],
    "attachment_ids":["at_1"],"input":{"prompt":"…"}
  }
}
```

这个格式没有 resume。hello 上报活动 `(run_id, attempt_id)`；heartbeat/event/steer/answer 都携带它们，message_id 仅用于关联消息。每次领取生成新的 attempt_id，租约挂 run。

- 续租、进度、最终结果、提问、插话确认和附件上传必须匹配当前有效 attempt。
- event 增加 attempt 内的 event_seq，按 `(run_id, attempt_id, event_seq)` 去重。
- 一个 run 的结果和终态在同一事务写入。已接受的最终回报重发时返回已接受结果，不重复插消息或触发工作通知。
- 旧 attempt 不能提交结果或附件。daemon 收到 stale 错误就停掉对应进程，停止完才能再次 ready。
- 服务重启不创建新的逻辑 run，租约到期才重派。重派是新的进程和当前 session 输入，工具副作用可能重复，不能保证外部操作恰好发生一次。

给 harness 设置 `AGENTUSE_SESSION`、`AGENTUSE_MESSAGE_ID`、`AGENTUSE_RUN`、`AGENTUSE_ATTEMPT`，CLI 自动带执行身份调用 HTTP。读取 session 当前消息、附件列表和内容时，dk_ 还须带 run/attempt（请求头 `X-Agentuse-Run`、`X-Agentuse-Attempt`），服务验证该执行确实分配给这台 daemon 且未结束、租约有效。

同一份 session 对所有获授权的执行可读；执行结束或取消后，不再允许用该 run 读取。产物上传也检查同一执行权。不能仅凭曾经在这个 session 干过活就永久获得读取权，不能借执行身份调用 session 管理接口。旧 message_id 鉴权格式只用于 legacy 执行。

## 归属与权限

新 session 归创建者所有，created_by 记录创建者；工作内的 session 归工作主人。@ 别人的 agent 不转移 session 归属，也不自动授予 agent 主人人类 API 的读取、回复、删除权限。session 主人/admin，以及范围内的工作令牌可以管理它。

普通 session 沿用当前“可以调用其他用户的 agent”规则；work 内仍只能选工作主人自己的 agent。每次 @ 都是本次执行的授权，所选 agent 的机器会读取共享 session 的消息和文件，界面展示其主人和运行时。未获得当前 run 的 daemon 无权读取。

第一版不增加人类群成员、邀请链接或一般的成员管理功能。

## 工作、插话和提问

- work 的 coordinator_id 继续指向 session，协调角色改由 work 上的 coordinator_agent_id 指定。它是 work 的调度配置，不把该 session 绑定为只能用这个 agent。
- 工作页输入框默认选中协调 agent，并显式发送 target_agent_ids；在这个 session 中也能 @ 其他 agent。只有以该 work 的协调 agent 身份执行的 run 才得到工作令牌。
- 工作令牌绑定当前 run/attempt，随该执行结束失效；不能扩展到其他工作或普通 session。自动通知协调者时，服务显式创建以 coordinator_agent_id 为目标的消息/run，不依赖 session 的默认 agent。
- 线程的一条消息有多个目标时，等它各目标的投递得到最终结果后向协调者发一次汇总，逐个署名列结果、失败或取消原因。插话复用已有 run 的结果；没送达而改挂后续 run 的目标，等后续执行结束。结束事务先处理未送达消息的改挂，再判断是否汇总，不能把旧 run 的结束误当成补充消息已完成。按 `(thread_session_id, message_id)` 去重；仍有离线 pending run 时继续显示等待，可以单独取消。
- steer/question 通过 run_id/attempt_id 归属到具体执行，提问、回答和超时处理不影响其他 run。用户统一用 messages 接口发消息并 @ agent，服务为忙碌目标生成内部 steer，提问回答仍使用对应 run/question 的接口。

## 兼容迁移

1. sessions 增加 mode（legacy / multi_agent）。已有会话及未传 mode 的旧创建请求保持 legacy；新网页和新版 SDK 显式创建 multi_agent。旧接口、原生续接、附件权限和 daemon 协议暂时保留，不能静默改变旧客户端行为。
2. 增加 `POST /api/sessions/{id}/upgrade`，只允许空闲且没有未处理 steer/question 的会话升级。回填共享消息的 seq、作者和目标，把已有最终结果导入为终态 run，不重跑历史任务；原 agent/runtime 仅用来补历史 run 的快照。
3. 升级后不再使用旧 harness_session_id，不把它迁移为按 agent 的续接状态。后续每次执行从共享 session 历史启动新原生会话；原消息、附件和各机器上已有的 session 工作目录保留。已经删除的 agent 只保留历史身份，下次需要选择当前存在的 agent。
4. 旧 user_id、created_by 的 read/write/delete 权限写入 session_access，主归属改为原发起人，避免升级时丢失既有访问权。新建会话没有这种自动授权。
5. work 必须整体空闲后一次性升级。原协调 agent 填入 coordinator_agent_id；若该 agent 已删除，先由主人选择有效 agent 再升级，不能凭空恢复。事务失败不得留下部分升级。

切换 API 默认创建模式和移除旧 runtime 字段另行安排版本；本阶段只定义并存及显式升级。

## 页面、CLI 和 SDK

新建会话不要求 agent。输入框可多选 @，明确显示这条消息交给谁；没有目标时提示“仅记入会话”。会话里用过的 agent 可以作为最近使用项，由历史派生，不需要管理名单。

各 agent 的回复、计划、进度、提问和附件都有署名；同一消息下分别显示“排队”“作为插话等待送达”“已送达”等目标状态。使用同一个输入框，不增加独立的插话发送入口。用户可以下一条只 @ 失败的 agent，不必重新跑全部。取消执行不隐藏历史。

SDK 增加 Run、目标数组和 `{message, runs, deliveries}` 响应，以及 WaitRun、WaitMessageRuns。WaitMessageRuns 按消息目标的当前投递关系等待，跟踪插话改挂后的 run，不能只等待初次响应里的 runs。WaitSession 在新模式下等待全部 run 终态；如配置遇提问即返回，同时给出所有 waiting run。旧模式的接口和响应保留。

CLI 增加 `agentuse session send <id> --agents coder,reviewer "…"`、`agentuse session read <id>`、`agentuse session runs <id>` 和指定 run 的等待/取消。read 返回这个 session 的统一消息，执行身份只决定是否有权读取，不改变内容视图。attachment 命令自动带 run/attempt。work new/send 可传多个目标，原 --agent 对应单元素目标。

## 实现和验收

先增加 mode、run、消息作者/目标/seq 和执行权限，再接多目标调度、新协议及共享 session 输入，最后更新附件、work、页面、CLI/SDK 和升级流程。所有改动先与 legacy 并存。

验收覆盖：

- 先 @ A，再 @ B，B 能看到同一 session 中 A 的回复、所有用户消息和附件；再次 @ A 也读取同一份历史。
- 不创建 agent 加入关系，不保存按 agent 的记忆；每次都是新原生会话，切换 agent 或 harness 无需迁移上下文。
- 同一消息 @ A/B，各有署名结果；后开始的执行能看到已经提交的新回复，排队期间新增附件在开始执行时可见。
- 同机不同 agent 使用同一 session 目录且不并发改写，跨机器通过 session 附件交换文件。
- A 正在执行时再 @ A/B，消息只写一次：A 收插话，空闲的 B 新建 run；pending/waiting、结束竞争和不支持插话的排队回退都不会漏消息或重复执行。
- 只记录消息时不隐式调用上一个 agent；删除 agent、切换运行时只按 run 快照规则影响后续任务和相关排队任务。
- 发送重试、结果重发不重复创建 run/消息；过期 attempt 的心跳、结果、附件和读取请求被拒绝，取消后能完成或删除 session。
- 跨用户/工作/run/question 的越权被拒绝；选择外部 agent 不扩大人类 API 权限。
- legacy 回归及显式升级通过，历史消息、文件和旧访问权限保留，升级后使用共享历史重新开始原生执行。
