# harnessctl/1 JSONL 事件格式

本文件定义 Codex、Claude Code 适配器写入事件文件的完整契约。机器可读版本为 [event.schema.json](event.schema.json)，采用 JSON Schema 2020-12。每个独立适配器包携带相同的 EVENT.md、event.schema.json 和标准库校验器；所有落盘入口在写入前校验，不符合契约的数据不进入 JSONL。

## 公共约定

每个完整行是一个 UTF-8 JSON 对象，以 `\n` 结束；字符串中的换行编码为 `\n`，不能把一条事件写成多行。文件只追加，读取游标是已消费的完整行数；未结束的半行应等待后续数据。

所有事件共同使用：

| 字段 | 必填 | 类型 | 含义 |
|---|---|---|---|
| type | 是 | string | 下表中的事件类型 |
| ts | 是 | string | UTC RFC 3339 时间，支持 `Z` 或 `+00:00`，小数秒可选 |
| native | 否 | object 或 null | 产生此事件的完整原生 JSON 帧；适配器自产事件可以省略，无来源时可以为 null |

原生行不是合法 JSON 时，error.native 可以包含 `{"_invalid":"坏帧诊断文本"}`，诊断最多保留 4096 个字符，不声称完整保存坏帧。

示例统一使用 `2026-11-01T12:00:00Z`；真实事件使用实际写入时间，不保证时间戳严格递增或唯一，按文件行序消费。缺省字段和显式 null 不等价：只有明确允许 null 的字段才能写 null。空字符串是合法文本，但 input/request/question ID 必须非空。

JSON 不允许 NaN、Infinity 或非 JSON 值。本文各事件列出的字段加上公共字段是当前写入白名单；未知类型、额外顶层字段、缺少必填字段、错误类型和枚举均拒绝写入。读取方仍应忽略后续版本可能出现的未知事件和字段，不能依赖写入白名单实现向前版本兼容。

事件文件路径是唯一会话地址，不新增 session_id、native_session 或 turn 寻址字段。input 是适配器分配的输入关联标识；request_id 是回复一项请求时使用的标识。两者都不是另一种会话身份。native 内可能保留原生 ID，仅供诊断，上层无需解析或传回。

## 全部事件

| type | 用途 |
|---|---|
| session.ready | 原生上下文已建立或恢复 |
| turn.started | 一次执行开始的证据 |
| turn.finished | 一次执行终态 |
| input.accepted | 适配器已受理输入 |
| input.observed | 观察到原生输入回显/输入条目 |
| input.delivered | 输入投递证据及确认范围 |
| input.rejected | 一次原生投递被拒绝，可能内部重试 |
| input.dropped | 该条待投递输入不再尝试 |
| message.delta | 文本增量 |
| message | 完整文本消息 |
| tool.started | 原生工具调用开始/被发起 |
| tool.finished | 原生工具调用结束 |
| plan | 当前计划快照 |
| usage | 用量及可选费用 |
| request.opened | 待外部回复的权限或问题请求 |
| request.replied | 回复已写入原生连接 |
| request.expired | 请求失效 |
| cancel.accepted | 已受理取消 |
| control.response | 原生中断控制回执 |
| runtime.stopped | 适配器内部运行进程停止 |
| error | 适配器或原生错误诊断 |

## 1. session.ready

无额外必填字段。原生 thread/session 已建立或恢复，私有恢复信息已保存。同一事件地址在进程回收后恢复时可以再次出现，不表示创建了新的上层会话。

```json
{"type":"session.ready","ts":"2026-11-01T12:00:00Z"}
```

## 2. turn.started

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| evidence | 否 | `rpc_response`、`adapter_submission`、`assistant_output`、`result` |

Codex 取原生执行 RPC 回执作为证据；Claude 可在适配器提交输入、观察到助手输出或只有 result 时合成执行边界。该事件不保证模型已读到所有输入，不需要另一个 turn ID。

```json
{"type":"turn.started","ts":"2026-11-01T12:00:00Z","evidence":"rpc_response"}
```

## 3. turn.finished

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| status | 是 | `completed`、`interrupted`、`failed` |
| output | 否 | string 或 null；原生 result 提供的最终输出 |

表示一次执行结束，不表示对话结束。Codex 最终文本通常通过 message 提供，可能没有 output。不得把进程退出码为零直接当作 completed。

```json
{"type":"turn.finished","ts":"2026-11-01T12:00:00Z","status":"completed","output":"检查完成"}
```

## 4. input.accepted

input：必填、非空 string。send 已被适配器受理；不证明已到达原生进程或模型，不包含调用方提供的幂等键。

```json
{"type":"input.accepted","ts":"2026-11-01T12:00:00Z","input":"in-1"}
```

## 5. input.observed

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| input | 否 | 非空 string；仅在能关联到受理输入时提供 |
| evidence | 否 | `user_replay` |

Claude 回显可关联 input 并给出 user_replay；Codex 的原生 userMessage 条目可能无法关联 input，此时两个字段都省略，来源放在 native 中。观察到输入不保证模型消费。

```json
{"type":"input.observed","ts":"2026-11-01T12:00:00Z","input":"in-1","evidence":"user_replay"}
```

```json
{"type":"input.observed","ts":"2026-11-01T12:00:00Z","native":{"method":"item/started","params":{"item":{"type":"userMessage"}}}}
```

## 6. input.delivered

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| input | 是 | 非空 string |
| outcome | 是 | `injected`、`started_turn`、`unknown` |
| evidence | 是 | `rpc_response`、`user_replay`、`native_result` |
| reason | 否 | string；解释不确定性等原因 |

injected 表示原生 RPC 确认补充输入进入活动执行；started_turn 表示输入对应一个新执行的提交/回显/结果证据；两者均不保证模型已经遵循指令。unknown 表示原生进程收到输入，但无法确认属于哪一轮。

当前 Codex 使用 rpc_response 确认 injected/started_turn；Claude 的首条输入可通过 user_replay 或 native_result 确认 started_turn，忙时补充的回显不冒充 injected，而保留 unknown。该字段是投递诊断，不是上层必须选择的策略。

```json
{"type":"input.delivered","ts":"2026-11-01T12:00:00Z","input":"in-1","outcome":"started_turn","evidence":"rpc_response"}
```

```json
{"type":"input.delivered","ts":"2026-11-01T12:00:00Z","input":"in-2","outcome":"injected","evidence":"rpc_response"}
```

```json
{"type":"input.delivered","ts":"2026-11-01T12:00:00Z","input":"in-3","outcome":"unknown","evidence":"user_replay","reason":"无法确认原生轮次","native":null}
```

## 7. input.rejected

input：必填、非空 string。reason：必填 string。一次原生投递失败；适配器可能因执行刚结束而内部转交后续执行，因此不等同于 input.dropped，也不要求上层自行重发。

```json
{"type":"input.rejected","ts":"2026-11-01T12:00:00Z","input":"in-2","reason":"no active turn to steer"}
```

## 8. input.dropped

input：必填、非空 string。reason：必填 string。该条尚未确认投递的输入不再尝试。reason 是开放文本，当前包括 cancelled、idle、terminated、backend_exited、error，以及原生拒绝原因，不能假设只有这些值。

取消之后的 dropped 不保证已经发生的工具副作用被撤销，也不能当作所有原生内部队列已清空的证明。

```json
{"type":"input.dropped","ts":"2026-11-01T12:00:00Z","input":"in-2","reason":"cancelled"}
```

## 9. message.delta

text：必填 string，允许空字符串。表示文本增量；没有此事件也可能直接提供完整 message。事件是语义文本，不保留 TUI 控制序列。

```json
{"type":"message.delta","ts":"2026-11-01T12:00:00Z","text":"检查"}
```

## 10. message

text：必填 string，允许空字符串。表示完整文本消息。可能与此前的 message.delta 描述同一段文本，不能无条件把二者再拼接；也不保证每轮只有一条 message。

```json
{"type":"message","ts":"2026-11-01T12:00:00Z","text":"检查完成，没有发现问题。"}
```

## 11. tool.started

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| tool | 是 | string 或 null；原生工具调用标识，不是 session ID |
| title | 是 | string 或 null；命令、工具名或简短标题 |
| status | 是 | `running`、`completed`、`failed`、`interrupted`、`unknown` |

通常 status=running。事件表示原生工具调用被发起，权限仍可能等待外部回复；不能据此认为命令已经获批执行。原生缺少标识/标题时保留 null，不伪造 ID。

```json
{"type":"tool.started","ts":"2026-11-01T12:00:00Z","tool":"tool-1","title":"npm test","status":"running"}
```

## 12. tool.finished

tool：必填 string 或 null。status：必填，枚举同 tool.started。title：可选 string 或 null。没有原生终态时 status=unknown，而不是假定成功。

Codex inProgress/in_progress 归一为 running，declined 归一为 failed，cancelled 归一为 interrupted；其他未知原生状态归一为 unknown。Claude 工具结果的 is_error 决定 failed/completed。原始状态仍可在 native 中查看。

```json
{"type":"tool.finished","ts":"2026-11-01T12:00:00Z","tool":"tool-1","status":"completed","title":"npm test"}
```

## 13. plan

steps：必填 array，允许空数组，表示本次计划快照。每项严格包含 text（必填 string）和 status（必填：pending、in_progress、completed、unknown）。原生 inProgress 归一为 in_progress；未知状态保留 unknown。

目前 Codex 从原生计划通知产生此事件；Claude 尚不合成 plan，不因为工具名像任务工具就虚报已实现的计划事件。

```json
{"type":"plan","ts":"2026-11-01T12:00:00Z","steps":[{"text":"检查实现","status":"completed"},{"text":"运行测试","status":"in_progress"}]}
```

## 14. usage

| 字段 | 必填 | 类型/取值 |
|---|---|---|
| usage | 是 | object 或 null；原生用量对象，内部字段开放 |
| cost_usd | 否 | 非负、有限 number 或 null |

usage 暂不强行统一两家的 token 计量结构；其内部是原生扩展区域。Codex 通常提供 total/last/modelContextWindow，Claude 通常提供 input_tokens/output_tokens/cache_read_input_tokens 等。上层不能把未知、缺失或 null 当作零。费用只来自原生计量，不根据缺失 token 估算。

Codex 示例：

```json
{"type":"usage","ts":"2026-11-01T12:00:00Z","usage":{"total":{"inputTokens":120,"outputTokens":20,"cachedInputTokens":80},"last":{"inputTokens":120,"outputTokens":20,"cachedInputTokens":80},"modelContextWindow":200000}}
```

Claude 示例：

```json
{"type":"usage","ts":"2026-11-01T12:00:00Z","usage":{"input_tokens":120,"output_tokens":20,"cache_read_input_tokens":80},"cost_usd":0.001}
```

未知值：

```json
{"type":"usage","ts":"2026-11-01T12:00:00Z","usage":null,"cost_usd":null}
```

## 15. request.opened

request_id：必填、非空 string。kind：必填 permission/question。message：必填 string。

### 权限请求

kind=permission，不带 questions。reply 的答案为 allow 或 deny，可从 stdin 提供。

```json
{"type":"request.opened","ts":"2026-11-01T12:00:00Z","request_id":"req-1","kind":"permission","message":"执行 npm test？"}
```

### 问题请求

kind=question，questions 必填且至少一项。每项的字段全部必填：

| 字段 | 类型 | 含义 |
|---|---|---|
| id | 非空 string | reply 答案中的键 |
| text | string | 问题正文 |
| header | string | 简短标题，未知可为空字符串 |
| multiple | boolean | 是否多选 |
| options | array | 每项严格包含 label、description 两个 string；可为空数组，表示没有预设选项 |

```json
{"type":"request.opened","ts":"2026-11-01T12:00:00Z","request_id":"req-2","kind":"question","message":"请回答问题","questions":[{"id":"q1","text":"选择语言","header":"语言","multiple":false,"options":[{"label":"Go","description":"Go 语言"}]}]}
```

reply 使用统一答案，例如 `{"q1":"Go"}`。request_id 由适配器独立分配，不直接复用原生 request ID；过期请求不能误答到后续原生请求。CLI 完整语义见 [README.md](README.md)。

## 16. request.replied

request_id：必填、非空 string。回复已向原生连接写入，不保证后端已经完成动作；没有原生确认时不生成虚假的 request.resolved。

```json
{"type":"request.replied","ts":"2026-11-01T12:00:00Z","request_id":"req-1"}
```

## 17. request.expired

request_id：必填、非空 string。reason：必填，枚举 turn_finished、cancelled、runtime_lost。后续 reply 应返回请求失效，不允许重用该 request_id。

```json
{"type":"request.expired","ts":"2026-11-01T12:00:00Z","request_id":"req-1","reason":"cancelled"}
```

## 18. cancel.accepted

无额外必填字段。已受理取消请求；空闲时也可以出现，不等于当前执行已经停止。通过状态与 turn.finished 判断后续结果。

```json
{"type":"cancel.accepted","ts":"2026-11-01T12:00:00Z"}
```

## 19. control.response

operation：必填，当前仅允许 interrupt。原生中断控制回执，通常带 native；回执不等于执行终态。

Claude 的错误控制回执可以先产生此事件再产生 error；原生 still_queued 等细节保留在 native，不假称本地掌握原生所有队列。

```json
{"type":"control.response","ts":"2026-11-01T12:00:00Z","operation":"interrupt","native":{"id":"ctl:1","result":{}}}
```

## 20. runtime.stopped

reason：必填，枚举 idle、terminated、backend_exited、error、lost。分别表示空闲回收、收到停止信号、后端退出、适配器错误、通过锁与 IPC 确认运行进程失联。

此事件仅表示内部运行进程停止，不表示事件地址对应的对话结束。下次 send 在安全条件下自动恢复原生上下文；不得要求上层选择重建或传入另一个 session ID。

```json
{"type":"runtime.stopped","ts":"2026-11-01T12:00:00Z","reason":"idle"}
```

## 21. error

message：必填 string。表示错误诊断，可能有 native；不必然是终态。例如原生拒绝一次控制请求后，已有执行仍可能继续。失败终态通过 turn.finished/status 的 failed 或 runtime.stopped 判断。

```json
{"type":"error","ts":"2026-11-01T12:00:00Z","message":"native control response timed out"}
```

## 写入保证与实现边界

- 所有运行进程写入，以及 status 失联补写、无活动进程时 cancel 的补写，都通过 append_event 统一入口，在取得写锁和修改文件前完成 Schema 校验和严格 JSON 编码。
- 不合法的待写事件不会留下半条 JSONL。运行进程中的格式错误转为合法 error，再走现有失败清理；文件系统故障本身可能导致无法落盘，不能承诺故障时一定能保存 error。
- 每个适配器包携带独立校验器，不依赖仓库级模块或第三方 Python 包。仓库测试校验规范示例、Schema 与包内副本的一致性，以及两家实际执行产生的每一条事件。
- 只有 native 和 usage.usage 的对象内部是开放的原生数据区域；其余嵌套问题、选项、计划步骤也按 Schema 校验。机器 Schema 的 date-time 表示 RFC 3339，写入校验器进一步要求 UTC，拒绝非零时区偏移。
- 事件序列不承诺恰好一次；RPC 回执、落盘和工具副作用不是跨进程事务。异常退出可能缺失末尾事件，不能从没有终态直接推断成功，也不能盲目重放输入。
