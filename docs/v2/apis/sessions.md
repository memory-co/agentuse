# sessions

会话和其中的消息。表结构见 [tables/sessions](../tables/sessions.md)、[tables/messages](../tables/messages.md)、[tables/session_events](../tables/session_events.md)。

## session 对象

```json
{
  "id": "ss_b83a324345de1f46",
  "user_id": "u_10086",
  "agent": "claude",
  "daemon_id": "dm_5c1c8602536e55cb",
  "cwd": "~/code/agentuse",
  "harness_session_id": "9f1c…",
  "title": "修一下 README 的错别字",
  "status": "idle",
  "created_at": 1792800040,
  "updated_at": 1792800105
}
```

- `status`:`idle` / `pending` / `running`。
- `daemon_id`、`cwd`、`harness_session_id`、`lease_until` 没有值时不出现。

## message 对象

```json
{ "id": 3, "session_id": "ss_…", "role": "user", "content": { "prompt": "再把 CHANGELOG 也改一下" }, "created_at": 1792800090 }
{ "id": 4, "session_id": "ss_…", "role": "assistant", "content": { "ok": true, "output": "改好了", "session_id": "9f1c…", "cost_usd": 0.031 }, "created_at": 1792800105 }
```

## GET /api/sessions

最近 100 个会话，按 `updated_at` 新的在前。普通用户只看自己的,admin 看全部。

## POST /api/sessions

新建会话，同时发出第一条消息。两种写法:

```json
{ "agent": "claude", "prompt": "修一下 README 的错别字", "cwd": "~/code/agentuse" }
```

```json
{ "agent": "my-harness", "input": { "任意": "JSON" } }
```

- `agent` 必填，必须是登记过的 agent,否则 400。
- `cwd` 可省略，定下后整个会话都在这里跑。
- 给 `prompt` 时消息内容是 `{"prompt": …}`;给 `input` 时原样交给 agent,忽略 `prompt`。
- 返回 201 和 `{"session": …, "message": …}`,会话为 `pending`。只派给创建者自己的、开启了这个 agent 的在线 daemon;没有就一直排队。

## POST /api/sessions/{id}/messages

回复：在会话里发下一条消息。

```json
{ "prompt": "再把 CHANGELOG 也改一下" }
```

或 `{"input": {…}}`,规则同上。

- 会话不是 `idle`(agent 还没回)返回 409。
  - TODO:插话(agent 还在跑时追加消息)以后用 harness 的 hook 来做。
- 会话的 daemon 已被删除返回 400(harness 会话在原来那台电脑上，续不上)。
- 返回 201 和 message 对象。只派给这个会话的 daemon,它离线就等它上线。

## GET /api/sessions/{id}

```json
{
  "session": { "id": "ss_…", "status": "idle", "…": "…" },
  "messages": [ { "id": 1, "role": "user", "…": "…" }, { "id": 2, "role": "assistant", "…": "…" } ],
  "events": [
    { "id": 1, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "progress", "payload": { "text": "…" }, "created_at": 1792800043 },
    { "id": 2, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "done", "payload": { "ok": true, "output": "…" }, "created_at": 1792800045 }
  ]
}
```

`messages` 和 `events` 都按时间顺序;`events.message_id` 是它在回的那条 `user` 消息。

## DELETE /api/sessions/{id}

删除会话和它的消息、回报，返回 204。`running` 时返回 409。只删 agentuse 这边的记录，不动 daemon 本机上 harness 的会话文件。
