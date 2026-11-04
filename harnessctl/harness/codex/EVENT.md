# harnessctl：五种公共 JSONL 事件

事件只有 **accepted、message、request、result、log** 五种。前四种是关键消息：可靠落盘，不能通过丢日志的方式丢弃；log 只用于展示过程和诊断，可以丢弃、合并或限流。机器契约见 [event.schema.json](event.schema.json)。每个独立适配器包携带相同文档、Schema 和标准库校验器，所有写入均校验。

## 1. 公共字段和可靠性

每个完整行是一个 UTF-8 JSON 对象，以换行结束。字符串里的换行转义为 `\n`。文件只追加，按完整行数推进游标；半行等待后续数据。

| 字段 | 类型 | 说明 |
|---|---|---|
| type | string | 仅允许五种公共类型 |
| ts | string | UTC RFC 3339，`Z` 或 `+00:00`，可带小数秒；按行序消费，不依赖时间唯一性 |
| run_id | UUID string | 适配器分配的一次交互身份；只有不属于具体交互的 log 可以为 null |

关键消息先校验、再加锁追加并 flush/fsync；accepted 完成落盘后 send 才返回成功。log 不要求 fsync，其写入或校验失败不影响执行、请求状态或结果。关键写入失败不能当作成功返回。文件系统故障和进程崩溃仍不具备跨进程事务或恰好一次保证。

丢弃全部 log 后，仍须能还原用户输入、agent 完整正文、请求状态和每条输入的最终结果。流式片段不能是正文唯一来源，终态必须将仍在内存中的完整正文写成 message。致命错误必须体现在 result，不能只写 error 级日志。

当前 Schema 是写入白名单：未知类型、额外顶层字段、错误枚举/类型、非有限数字均拒绝写入。native 原生报文只能放在 log.data 等诊断数据内；关键消息不携带原生 session/thread ID 或控制报文。读取方可忽略后续版本中的未知字段。

## 2. run_id、input_id 和交互归属

事件文件的规范绝对路径仍是唯一会话地址；run_id 不是另一个 session ID，也不是命令参数，由适配器使用 UUID 分配，使用方不能指定。

- 一次新的交互分配新 run_id；确认插入当前交互的补充输入沿用当前 run_id。
- 适配器内部排到下一次交互的输入使用新的 run_id。多个已受理的交互可以在文件中交错出现，不能按物理连续行切分 run。
- Codex 的运行中输入先等待原生插话回执，确认后才公开 accepted 的归属；若原生报告当前轮刚结束，则内部改为下一次交互并分配新 run_id。公布后不再修改该 input_id 的 run_id。
- Claude Code 忙时直接通过 stream-json 提交 priority=next 输入，在工具轮次之间消费。以 command_lifecycle.started 或 assistant/result 的 user_message_uuids 确认归属后才写 accepted；user replay 本身不算消费证据。当前轮结束后才被消费的输入使用新 UUID。已验证后端 2.1.316。
- input_id 标识一条已受理输入，由适配器分配。一个 run 可以包含初始输入和多条补充输入。result.input_ids 明确列出本次结果覆盖的输入。
- 每条 accepted 输入最终由一个 result 覆盖；同一 run 的输入归并到同一个结果。取消尚未开始的交互也产生 cancelled 结果。run 可能在原生启动前失败，不虚报执行过模型。

send 等待本机受理和必要的原生消费回执；Claude 插话可能等待当前工具结束，到消费边界才确认 run_id，不主动等待模型完成。等待期间 status、reply、cancel 仍可用。控制回执或 IPC 超时时，结果可能不确定，调用方应读取文件；不能根据一次超时盲目重发。

## 3. accepted：已受理输入，关键

必填字段：run_id、input_id（非空 string）、text（非空 string），以及 type、ts。text 原样保存完整输入，支持多行和特殊字符；它不是可丢日志。

```json
{"type":"accepted","ts":"2026-11-01T12:00:00Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"in-1","text":"修复测试"}
```

确认中途补充进入同一次交互：

```json
{"type":"accepted","ts":"2026-11-01T12:00:05Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"in-2","text":"先别跑集成测试"}
```

accepted 表示适配器已承担处理责任，不保证模型已经遵循输入内容。run_id 是适配器确认的交互归属，不要求使用方解析原生 turn ID。

## 4. message：完整 agent 正文，关键

必填字段：run_id、message_id（非空 string）、text（string，可为空）。message_id 是正文关联标识，不是会话或运行身份。

```json
{"type":"message","ts":"2026-11-01T12:00:10Z","run_id":"550e8400-e29b-41d4-a716-446655440000","message_id":"msg-1","text":"已修复单元测试，没有运行集成测试。"}
```

同一 message_id 的后续 message 是完整替换快照，读取方按 ID 更新正文，不能再次拼接。流式增量只用于实时展示；即使这些 log 全部丢失，仍可用 message 恢复完整正文。仅有流式输出的后端，也由适配器在交互结束前汇总为完整快照。

一段交互可以有多条完整消息。result.output 为最终输出快照，不应再次作为新对话正文追加，避免重复显示。

## 5. request：待回复请求及状态，关键

每个 request 都是完整快照，按 request_id 更新。必填公共字段：run_id、request_id（非空 string）、state、kind、message（string）。

| 字段 | 取值 |
|---|---|
| state | pending、replied、expired |
| kind | permission、question |

### 权限请求

pending 不带 answer/reason：

```json
{"type":"request","ts":"2026-11-01T12:00:01Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","state":"pending","kind":"permission","message":"执行 npm test？"}
```

replied 必须带 answer=allow/deny：

```json
{"type":"request","ts":"2026-11-01T12:00:02Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","state":"replied","kind":"permission","message":"执行 npm test？","answer":"deny"}
```

expired 必须带非空 reason，不能再回复：

```json
{"type":"request","ts":"2026-11-01T12:00:03Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","state":"expired","kind":"permission","message":"执行 npm test？","reason":"cancelled"}
```

### 问题请求

所有状态都必须带 questions，至少一项。每项包含 id（非空 string）、text、header（string）、multiple（boolean）、options（array）。每个选项严格包含 label 和 description 两个 string；无预设选项时使用空数组。

```json
{"type":"request","ts":"2026-11-01T12:00:01Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-2","state":"pending","kind":"question","message":"选择语言","questions":[{"id":"q1","text":"使用什么语言？","header":"语言","multiple":false,"options":[]}]}
```

replied 的 answer 是 question id 到非空文本或非空文本数组的 JSON 对象。命令验证必须完整回答所有问题；非多选问题只能提供一个答案。适配器负责转换原生格式。

```json
{"type":"request","ts":"2026-11-01T12:00:02Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-2","state":"replied","kind":"question","message":"选择语言","questions":[{"id":"q1","text":"使用什么语言？","header":"语言","multiple":false,"options":[]}],"answer":{"q1":"Go"}}
```

```json
{"type":"request","ts":"2026-11-01T12:00:03Z","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-2","state":"expired","kind":"question","message":"选择语言","questions":[{"id":"q1","text":"使用什么语言？","header":"语言","multiple":false,"options":[]}],"reason":"runtime_lost"}
```

replied 说明答案已写向原生连接，不保证工具已执行。执行结束、取消或失联会过期待回复请求，必须在 result 前记录请求终态，不能只写日志。原生请求标识不直接复用为公共 request_id。

## 6. result：交互终态与计量，关键

以下字段全部必填：

| 字段 | 类型/取值 |
|---|---|
| run_id | UUID string |
| input_ids | 非空、无重复的 string 数组；均属于此 run |
| status | completed、cancelled、failed |
| output | string；完整输出汇总，可为空 |
| usage | object 或 null；统一为 input_tokens、output_tokens、cache_read_tokens、cache_write_tokens 非负整数，未知时为 null。input_tokens 包含缓存输入计量 |
| cost_usd | 非负有限 number 或 null |
| error | failed 时为非空 string；其余状态必须为 null |

```json
{"type":"result","ts":"2026-11-01T12:00:11Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["in-1","in-2"],"status":"completed","output":"已修复单元测试，没有运行集成测试。","usage":{"input_tokens":120,"output_tokens":20},"cost_usd":0.001,"error":null}
```

```json
{"type":"result","ts":"2026-11-01T12:00:11Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["in-1"],"status":"cancelled","output":"","usage":null,"cost_usd":null,"error":null}
```

```json
{"type":"result","ts":"2026-11-01T12:00:11Z","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["in-1"],"status":"failed","output":"","usage":null,"cost_usd":null,"error":"原生执行进程失联"}
```

结束一条工具调用、收到中断控制回执或回收空闲进程，都不是 result。只有交互终态才产生结果，不能靠日志判断是否完成。未知计量保持 null，不能当作零。

进程异常失联时，适配器读取关键事件，按 run_id 找出仍未有结果的已受理输入，记录失败结果，不自动重放。日志缺失不影响此判断。原生副作用、事件落盘、状态快照和 IPC 回复不是事务，异常窗口可能存在结果不确定性；不能据此盲目重发。

## 7. log：可丢过程与诊断

必填字段：run_id（UUID string 或 null）、topic（非空 string）、level（debug/info/warning/error）、message（string）、data（object 或 null）。topic 是开放诊断标签，不再形成一套上层必须识别的事件枚举。

```json
{"type":"log","ts":"2026-11-01T12:00:04Z","run_id":"550e8400-e29b-41d4-a716-446655440000","topic":"text.delta","level":"debug","message":"","data":{"data_text":"检查中","message_id":"msg-1"}}
```

```json
{"type":"log","ts":"2026-11-01T12:00:12Z","run_id":null,"topic":"runtime.stopped","level":"debug","message":"","data":{"reason":"idle"}}
```

流式增量、工具进度、计划、原生投递回执、重试和运行进程生命周期都可放入日志。native 帧只作 data 内的诊断信息。error 级日志不是失败终态；权限请求、正文和致命错误不能只存在于 log。

## 8. 写入和测试边界

独立包内的校验器在写入前验证公共契约，拒绝旧事件类型和额外字段。关键消息写入失败不得作为受理成功处理；日志校验、写入失败可以丢弃。读取方忽略 log 后仍可按 accepted、message、request、result 重建业务视图。

关键事件不承诺复制原生隐藏推理，也不依赖原生内部 turn ID。原生只提供增量时，适配器负责生成完整 message。异常退出可能丢失尚未完成、尚未可靠写入的正文片段，必须记录失败而不是声称成功；文件系统故障时无法承诺错误事件一定可落盘。

测试覆盖五种类型的 Schema、示例、两个独立分发包一致性、插话归属竞态、日志全部丢弃、正文流式汇总、关键状态恢复和非法事件拒写。
