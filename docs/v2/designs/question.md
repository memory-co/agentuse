# 提问

## 一句话

Claude 跑到一半拿不准时，可以用自带的 `AskUserQuestion` 工具向用户提问。daemon 把问题报上来，会话变成 `waiting`，页面上显示选项。用户选完，答案送回给还在等着的 Claude，它在同一个回合里接着做。用户一直不答，到时间就让 Claude 先结束这一轮，把问题写进最终回复。

```
用户: 写个 hello world                    ← running
Claude 提问: 用 Python 还是 Go？[Python] [Go]   ← waiting
用户: 选 Go                               ← running
Claude: 写好了 main.go
```

## Claude Code 的行为

2026-10-27 用 Claude Code 2.1.311 实测：

- `-p` 模式默认没有 `AskUserQuestion`。加上 `--permission-prompt-tool stdio` 才有，所以原来的 `--disallowedTools AskUserQuestion` 去掉，换成这个参数。
- 它调用这个工具时，stdout 出一条 control_request，进程停在这里等回复：
  ```json
  {"type":"control_request","request_id":"…","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion",
   "tool_use_id":"toolu_…","requires_user_interaction":true,
   "input":{"questions":[{"question":"要用 Python 还是 Go？","header":"语言","multiSelect":false,
     "options":[{"label":"Python","description":"…"},{"label":"Go","description":"…"}]}]}}}
  ```
- 回 `allow`，并在 `updatedInput` 里加上 `answers`（问题 → 答案），Claude 就拿着答案接着做。答案可以是选项的 `label`，也可以是用户自己写的话：
  ```json
  {"type":"control_response","response":{"subtype":"success","request_id":"…","response":{
    "behavior":"allow","updatedInput":{"questions":[…],"answers":{"要用 Python 还是 Go？":"Go"}}}}}
  ```
- 回 `{"behavior":"deny","message":"…"}`，Claude 把问题写进最终回复，然后结束这一轮。
- 在 `bypassPermissions` 下，只有 `AskUserQuestion` 会发 `can_use_tool`，Bash 等工具不会。实测挂着等 90 秒再回也没问题。

## harness.yaml

```yaml
run:
  new: [-p, …, --permission-mode, bypassPermissions, --permission-prompt-tool, stdio, "{args}"]
ask: claude-permission
```

`ask` 和 `steer` 一样是 Go 里的一个具名实现。没写 `ask` 的 harness 不能提问（codex 没有对应的工具）。

## 状态

会话多一个状态 `waiting`：

```
running ──harness 提问──▶ waiting ──答案送到 / 超时──▶ running ──▶ idle
```

- `waiting` 和 `running` 一样占着 daemon，心跳照常续租。租约过期、daemon 掉线时一样收回重排，问题变成 `expired`。
- `waiting` 时也能插话。插话先排着，等 Claude 拿到答案、调下一个工具时再送。

问题记在 [questions](../tables/questions.md) 表：

```
pending ──用户回答，daemon 送到──▶ answered
   │
   └──超时 / 进程没了──▶ expired
```

一条消息同一时间最多一个 `pending` 的问题，因为 Claude 在等回复时不会再调别的工具。

## 接口

`GET /api/sessions/{id}` 多返回一个 `questions` 数组：

```json
{ "id": 5, "session_id": "ss_…", "message_id": 3, "tool_use_id": "toolu_…",
  "questions": [{ "question": "要用 Python 还是 Go？", "header": "语言", "multi_select": false,
                  "options": [{ "label": "Python", "description": "…" }, { "label": "Go", "description": "…" }] }],
  "status": "pending", "created_at": 1792800050 }
```

回答：`POST /api/sessions/{id}/questions/{qid}/answer`

```json
{ "answers": { "要用 Python 还是 Go？": "Go" } }
```

- 每个问题都要有答案，可以是选项的 `label`，也可以是自己写的话。多选时用 `, ` 把几个 label 拼起来。
- 问题不是 `pending` 时返回 409（已经答过，或已经超时）。
- 返回 202。答案先记下来，daemon 送到后问题才变成 `answered`。

## daemon 协议

daemon → 服务，event 新增三种 kind：

```json
{ "type": "event", "message_id": 3, "kind": "question", "payload": { "tool_use_id": "toolu_…", "questions": [ … ] } }
{ "type": "event", "message_id": 3, "kind": "answered", "payload": { "tool_use_id": "toolu_…" } }
{ "type": "event", "message_id": 3, "kind": "question_expired", "payload": { "tool_use_id": "toolu_…" } }
```

- `question`：服务建一条 `pending` 的问题，会话变成 `waiting`。
- `answered`：问题变成 `answered`，会话回到 `running`。
- `question_expired`：问题变成 `expired`，会话回到 `running`。如果用户的答案已经记下但没来得及送到，就把它转成一条插话：能送达就在这一轮里送，送不到就等这一轮结束后变成下一条消息。

服务 → daemon 新增 `answer`：

```json
{ "type": "answer", "answer": { "message_id": 3, "tool_use_id": "toolu_…", "answers": { "要用 Python 还是 Go？": "Go" } } }
```

daemon 不在跑这条消息，或者已经不在等这个 `tool_use_id` 时，直接忽略。用户回答时服务推一次。daemon 断线重连、领回这条消息时，服务把已经答了但还没送到的再推一遍。

## daemon 怎么做

1. 读到 `can_use_tool` 且 `tool_name` 是 `AskUserQuestion`：记下 `request_id`，回报 `question`，先不回 control_response。
2. 收到 `answer`：回 `allow`，把 `answers` 放进 `updatedInput`，然后回报 `answered`。
3. 等到超时（默认 30 分钟，daemon.json 的 `ask_timeout` 可改）：回 `deny`，message 是"用户 30 分钟内没有回答。先结束这一轮，在最终回复里写清楚你的问题和各个选项。"，然后回报 `question_expired`。
4. 其他 `can_use_tool`（理论上在 bypassPermissions 下不会出现）直接回 `allow`，`updatedInput` 原样带回。
5. 读到 `result` 行时还有没回的提问（不该出现），一律按超时处理。

## 前端

- 会话 `waiting` 时，在那条消息下面显示问题卡片：标题、每个问题的选项按钮（多选时可以选多个），再加一个"其他"输入框，最后是提交按钮。
- 会话列表里 `waiting` 的会话标一个"等你回答"。
- 答过的问题显示成一行"问：… 答：…"，过期的显示"没有回答"。
- 回复框照常可用，发出去的是插话。

## SDK

- 会话状态多一个 `waiting`，`SessionDetail` 加 `questions`。
- 加一个方法 `AnswerQuestion(ctx, user, sessionID, questionID, answers)`，Python 和 Node 里同名。
- `WaitSession` 在会话回到 `idle` 或变成 `waiting` 时都返回，调用方看状态决定要不要回答。

## 实现步骤

已完成。用真实的 Claude Code 2.1.311 跑通过：提问、回答、在同一回合里接着做。

1. 新表 `questions`。store 增加：建问题、记答案、标记 answered / expired，以及 `waiting` 状态和过期时把答案转成插话。
2. 回答接口；`GET` 返回 `questions`；SDK 加类型和方法。
3. 协议新增 `question` / `answered` / `question_expired` 和 `answer`，hub 推送答案。
4. daemon：harness.yaml 的 `ask: claude-permission`，以及 claude 的参数换成 `--permission-prompt-tool stdio`。处理 `can_use_tool`、等待、超时。
5. skill 改一句：可以用 AskUserQuestion 提问，但只在真的拿不准时用，问题要能直接选。
6. 前端问题卡片。
7. 测试：fakeharness 的 claude 模式发一个 `can_use_tool`，并把答案写进结果；再测一次超时。
