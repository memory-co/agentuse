# sessions

会话和其中的每一轮。表结构见 [tables/sessions](../tables/sessions.md)、[tables/turns](../tables/turns.md)、[tables/turn_events](../tables/turn_events.md)。

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
  "status": "done",
  "turns": 2,
  "created_at": 1792800040,
  "updated_at": 1792800105
}
```

- `status`:最后一轮的状态,`pending` / `running` / `done` / `failed`。
- `turns`:一共几轮。
- `daemon_id`、`cwd`、`harness_session_id` 没有值时不出现。

## turn 对象

```json
{
  "id": "tn_1d0f…",
  "session_id": "ss_b83a324345de1f46",
  "seq": 2,
  "status": "done",
  "input": { "prompt": "再把 CHANGELOG 也改一下" },
  "result": { "ok": true, "output": "改好了", "session_id": "9f1c…", "cost_usd": 0.031 },
  "created_at": 1792800090,
  "updated_at": 1792800105,
  "finished_at": 1792800105
}
```

`result`、`lease_until`、`finished_at` 没有值时不出现。

## GET /api/sessions

最近 100 个会话，按 `updated_at` 新的在前。普通用户只看自己的，admin 看全部。

## POST /api/sessions

新建会话，同时发出第一轮。两种写法：

```json
{ "agent": "claude", "prompt": "修一下 README 的错别字", "cwd": "~/code/agentuse" }
```

```json
{ "agent": "my-harness", "input": { "任意": "JSON" } }
```

- `agent` 必填，必须是登记过的 agent,否则 400。
- `cwd` 可省略，定下后整个会话都在这里跑。
- 给 `prompt` 时第一轮 `input = {"prompt": …}`;给 `input` 时原样交给 agent,忽略 `prompt`。
- 返回 201 和 `{"session": …, "turn": …}`。只派给创建者自己的、开启了这个 agent 的在线 daemon;没有就一直排队。

## POST /api/sessions/{id}/turns

回复：在会话里发下一轮。

```json
{ "prompt": "再把 CHANGELOG 也改一下" }
```

或 `{"input": {…}}`,规则同上。

- 上一轮还没结束返回 409。
- 会话的 daemon 已被删除返回 400(harness 会话在原来那台电脑上，续不上)。
- 返回 201 和 turn 对象。只派给这个会话的 daemon,它离线就等它上线。

## GET /api/sessions/{id}

```json
{
  "session": { "id": "ss_…", "status": "done", "…": "…" },
  "turns": [ { "id": "tn_…", "seq": 1, "…": "…" }, { "id": "tn_…", "seq": 2, "…": "…" } ],
  "events": [
    { "id": 1, "turn_id": "tn_…", "session_id": "ss_…", "daemon_id": "dm_…", "kind": "progress", "payload": { "text": "…" }, "created_at": 1792800043 },
    { "id": 2, "turn_id": "tn_…", "session_id": "ss_…", "daemon_id": "dm_…", "kind": "done", "payload": { "ok": true, "output": "…" }, "created_at": 1792800045 }
  ]
}
```

`turns` 按 `seq` 排,`events` 是所有轮的回报，按时间顺序，用 `turn_id` 对应到轮。

## DELETE /api/sessions/{id}

删除会话和它的所有轮、回报，返回 204。有一轮正在跑时返回 409。只删 agentuse 这边的记录，不动 daemon 本机上 harness 的会话文件。
