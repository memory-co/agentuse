# sessions

会话和其中的消息。表结构见 [tables/sessions](../tables/sessions.md)、[tables/messages](../tables/messages.md)、[tables/session_events](../tables/session_events.md)。

## session 对象

```json
{
  "id": "ss_b83a324345de1f46",
  "user_id": "u_10086",
  "created_by": "u_10010",
  "agent_id": "code-helper",
  "harness": "claude",
  "daemon_id": "dm_5c1c8602536e55cb",
  "harness_session_id": "9f1c…",
  "title": "修一下 README 的错别字",
  "status": "idle",
  "created_at": 1792800040,
  "updated_at": 1792800105
}
```

- `status`:`idle` / `pending` / `running` / `waiting`(在等用户回答[提问](../designs/question.md))。
- `user_id` 是 agent 的主人，会话跑在他的 daemon 上;`created_by` 是发起人，用自己的 agent 时两者相同。
- `daemon_id`、`harness` 是建会话时 agent 的运行时，之后 agent 切换运行时它们也不变。
- `agent_id`、`daemon_id`、`harness_session_id`、`lease_until` 没有值时不出现(agent 删除后 `agent_id` 就没了)。

## message 对象

```json
{ "id": 3, "session_id": "ss_…", "role": "user", "content": { "prompt": "再把 CHANGELOG 也改一下" }, "created_at": 1792800090 }
{ "id": 4, "session_id": "ss_…", "role": "assistant", "content": { "ok": true, "output": "改好了", "session_id": "9f1c…", "cost_usd": 0.031 }, "created_at": 1792800105 }
```

## GET /api/sessions

最近 100 个会话，按 `updated_at` 新的在前。普通用户看自己 agent 上的和自己发起的,admin 看全部。下面的接口也一样：主人和发起人都能看、回复、删除。

## POST /api/sessions

用一个 [agent](agents.md) 新建会话，同时发出第一条消息。两种写法:

```json
{ "agent_id": "code-helper", "prompt": "修一下 README 的错别字" }
```

```json
{ "agent_id": "my-bot", "input": { "任意": "JSON" } }
```

- `agent_id` 必填，可以是任何人的 agent,不存在返回 400;agent 没有运行时(它的 daemon 被删了)也返回 400。会话属于 agent 的主人,`created_by` 是调用者;`daemon_id` 和 `harness` 取 agent 此刻的运行时。
- 不能指定工作目录,daemon 固定用 `~/.agentuse/work/<session_id>/`。
- 给 `prompt` 时消息内容是 `{"prompt": …}`;给 `input` 时原样交给 harness,忽略 `prompt`。
- agent 运行时上的 harness 没开启、或那台 daemon 最近一次连上时没检测到它，返回 400 并说明原因，不会排队。
- 返回 201 和 `{"session": …, "message": …}`,会话为 `pending`。只派给 agent 运行时的那台 daemon;daemon 离线就排队等它上线。
- 排队中的会话，如果它的 harness 之后被关掉、或 daemon 重连时没再上报它，会话直接失败：回到 `idle`,追加一条 `{"ok": false, "error": "原因"}` 的 assistant 消息。

## POST /api/sessions/{id}/messages

回复：在会话里发下一条消息。

```json
{ "prompt": "再把 CHANGELOG 也改一下" }
```

或 `{"input": {…}}`,规则同上。

- 会话的 daemon 已被删除返回 400(harness 会话在原来那台电脑上，续不上)。
- `idle` 时，会话的 harness 没开启或不可用，返回 400 并说明原因。
- `idle` 时返回 201 和 message 对象。只派给这个会话的 daemon,它离线就等它上线。
- `pending` / `running` / `waiting` 时是[插话](../designs/steer.md)：返回 202 和 steer 对象。插话只收 `prompt`,带 `input` 返回 409。

```json
{ "id": 7, "session_id": "ss_…", "message_id": 3, "created_by": "u_10010", "prompt": "顺便把 CHANGELOG 也改了", "status": "pending", "created_at": 1792800050 }
```

`status`:`pending` 还没送达;`delivered` 已经塞给 harness;`queued` 没来得及送，已经转成下一条消息。

## GET /api/sessions/{id}

```json
{
  "session": { "id": "ss_…", "status": "idle", "…": "…" },
  "messages": [ { "id": 1, "role": "user", "…": "…" }, { "id": 2, "role": "assistant", "…": "…" } ],
  "steers": [
    { "id": 7, "session_id": "ss_…", "message_id": 1, "created_by": "u_10010", "prompt": "…", "status": "delivered", "created_at": 1792800044 }
  ],
  "events": [
    { "id": 1, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "progress", "payload": { "text": "…" }, "created_at": 1792800043 },
    { "id": 2, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "done", "payload": { "ok": true, "output": "…" }, "created_at": 1792800045 }
  ],
  "daemon_offline": true
}
```

- `daemon_offline`:会话 `pending`、它的 daemon 不在线时为 `true`(在等 daemon 上线),否则不出现。

还有 `questions`,对象见下。`messages`、`steers`、`questions`、`events` 都按时间顺序;它们的 `message_id` 是挂着的那条 `user` 消息。

## POST /api/sessions/{id}/questions/{qid}/answer

回答 harness 的[提问](../designs/question.md)。

```json
{ "answers": { "要用 Python 还是 Go？": "Go" } }
```

- 每个问题都要有答案，可以是选项的 `label`,也可以是自己写的话;多选用 `, ` 拼起来。缺了返回 400。
- 问题不是 `pending` 返回 409。
- 返回 202 和 question 对象。daemon 把答案送到后才变成 `answered`。

```json
{ "id": 5, "session_id": "ss_…", "message_id": 3, "tool_use_id": "toolu_…",
  "questions": [{ "question": "要用 Python 还是 Go？", "header": "语言", "multi_select": false,
                  "options": [{ "label": "Python", "description": "…" }, { "label": "Go", "description": "…" }] }],
  "answers": { "要用 Python 还是 Go？": "Go" }, "answered_by": "u_10010", "status": "pending", "created_at": 1792800050 }
```

`status`:`pending` 等回答(或答案还没送到);`answered` 已送到;`expired` 超时或进程没了。

## DELETE /api/sessions/{id}

删除会话和它的消息、回报，返回 204。`running` 时返回 409。只删 agentuse 这边的记录，不动 daemon 本机上 harness 的会话文件。
