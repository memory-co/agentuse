# 插话

## 一句话

harness 还在跑的时候，用户也能接着发消息。能插话的 harness(claude)在它调下一个工具前、或者回合要结束时，把这句话塞给它，它在同一个回合里接着处理;不能插话的 harness(codex、自定义)，或者没来得及送达的，等这一轮跑完后自动作为下一条消息发出去。

```
用户: 修一下 README                      ← running
用户: 顺便把 CHANGELOG 也改了            ← 插话，送达后 Claude 在同一回合里接着做
Claude: README 和 CHANGELOG 都改好了
```

## 接口

`POST /api/sessions/{id}/messages` 不再在会话忙时返回 409:

| 会话状态 | 结果 |
|---|---|
| `idle` | 和现在一样，发下一条消息,201 + message |
| `pending` / `running` | 记成一条插话,202 + steer 对象 |

- 插话只收 `{"prompt": "..."}`。带 `input` 返回 409:自定义输入没法合并、也没法塞进上下文。
- 会话的 daemon 已被删除时，返回 400，和现在一样。

```json
{ "id": 7, "session_id": "ss_…", "message_id": 3, "created_by": "u_10010", "prompt": "顺便把 CHANGELOG 也改了", "status": "pending", "created_at": 1792800050 }
```

`GET /api/sessions/{id}` 多返回一个 `steers` 数组，按时间顺序排。

## 状态

插话记在 [steers](../tables/steers.md) 表，挂在发出时正在回的那条 `user` 消息(`message_id`)上:

```
pending ──daemon 塞给了 harness──▶ delivered
   │
   └──这一轮跑完还没送达──▶ queued(转成下一条 user 消息)
```

- **送达**：服务把插话推给正在跑这条消息的 daemon。daemon 塞给 harness 后回报 `steered`，插话变成 `delivered`。
- **没送达**：daemon 回报 `done` / `failed` 时，同一条消息下还在 `pending` 的插话全部变成 `queued`。它们的 prompt 按顺序用空行拼成**一条**新的 `user` 消息，追加在 assistant 消息后面，会话直接回到 `pending`，接着派给同一台 daemon。不能插话的 harness 走的都是这条路，效果就是排队。
- **会话还是 `pending` 时插话**：也挂在那条还没派出的消息上。daemon 领走以后，服务先派消息、再推插话。
- **租约过期、会话被收回**：插话不动，下次派出时重新推。

messages 表还是一问一答交替。送达的插话不进 messages，挂在它那条消息下面显示。

## daemon 协议

服务 → daemon 新增 `steer`:

```json
{ "type": "steer", "steer": { "id": 7, "message_id": 3, "prompt": "顺便把 CHANGELOG 也改了" } }
```

daemon → 服务的 event 新增一种 kind `steered`:

```json
{ "type": "event", "message_id": 3, "kind": "steered", "payload": { "steer_id": 7 } }
```

- daemon 不在跑 `message_id` 这条消息，或者这个 harness 不能插话时，直接忽略。服务等这一轮结束时会把它转成 `queued`。
- `steered` 和 `progress` 一样只续租。

## daemon 怎么塞给 claude

harness.yaml 加一个开关：

```yaml
steer: claude-hooks
```

Claude Code 的 control 协议是它专有的，写成 Go 里一个具名实现(`claude-hooks`)，不用规则描述。没写 `steer` 的 harness 不能插话。

1. 起进程后，stdin 第一行先注册 hook，这个 hook 只对这个进程有效，不写盘:
   ```json
   {"type":"control_request","request_id":"agentuse-init","request":{"subtype":"initialize","hooks":{
     "PreToolUse":[{"hookCallbackIds":["agentuse-pre"]}],
     "Stop":[{"hookCallbackIds":["agentuse-stop"]}]}}}
   ```
   第二行才是 user 消息，和现在一样。
2. 收到的插话先放进本条消息的队列。
3. stdout 读到 `{"type":"control_request","request":{"subtype":"hook_callback","callback_id":…}}` 时，从 stdin 回 `control_response`。**每个 control_request 都必须回**，否则 claude 会一直等:
   - `agentuse-pre`(调工具前)：队列空就回 `{}`。有插话就回下面这个，并对每条插话回报 `steered`:
     ```json
     {"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"[用户插话] 用户在你工作时补充了下面的话。这是对原任务的补充，不是替换原任务：\n顺便把 CHANGELOG 也改了"}}
     ```
   - `agentuse-stop`(回合要结束)：队列空就回 `{}`。有插话就回 `{"decision":"block","reason":"<同上的文字>"}`，让它接着做，同样回报 `steered`。
   - 其他 subtype 回 `{}`。
4. 读到 `result` 行后照旧关闭 stdin。关闭之后才到的插话不再送，等服务把它转成下一条消息。

## 前端

- 会话 `running` / `pending` 时回复框不再禁用，发出去的是插话。占位文字改成"补充说明，会在它下一步之前送达"。
- 插话显示在它那条消息下面，样式比普通消息小一号，标注"已送达"或"排队中"。`queued` 的插话已经转成下一条消息，不再单独显示。

## 实现步骤

1. 新表 `steers`。store 增加：建插话、标记送达、结束时把未送达的转成下一条消息。
2. 回复接口按会话状态分流;`GET` 返回 `steers`;SDK 加类型。
3. 协议新增 `steer` 和 `steered`，hub 负责推送：收到插话时推一次，派出消息后把已有的推一遍。
4. daemon:harness.yaml 的 `steer`，以及 `claude-hooks` 的 initialize 和 hook 回调。
5. 前端：忙的时候也能发，显示插话。
6. 测试：fakeharness 的 claude 模式发一个 PreToolUse 回调，并把收到的 additionalContext 放进结果里;codex 模式检查插话转成了下一条消息。
