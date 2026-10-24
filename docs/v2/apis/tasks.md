# tasks

任务。表结构见 [tables/tasks](../tables/tasks.md) 和 [tables/task_events](../tables/task_events.md)。

## task 对象

```json
{
  "id": "tk_b83a324345de1f46",
  "user_id": "u_10086",
  "daemon_id": "dm_5c1c8602536e55cb",
  "agent": "claude",
  "status": "done",
  "input": { "prompt": "Reply with exactly: hello from agentuse" },
  "result": { "ok": true, "output": "hello from agentuse", "session_id": "…", "cost_usd": 0.044 },
  "lease_until": null,
  "created_at": 1792800040,
  "updated_at": 1792800045,
  "finished_at": 1792800045
}
```

- `status`:`pending` / `running` / `done` / `failed`。
- `daemon_id`、`result`、`lease_until`、`finished_at` 没有值时不出现。
- `result` 约定为 `{"ok": bool, "output": "…", "error": "…"}`,内置 agent 可能多带 `session_id`、`cost_usd`。

## GET /api/tasks

最近 100 个任务，新的在前。普通用户只看自己的，admin 看全部。

## POST /api/tasks

建任务。两种写法：

```json
{ "agent": "claude", "prompt": "修一下 README 的错别字", "cwd": "~/code/agentuse" }
```

服务把它包成 `input = {"prompt": …, "cwd": …}`,`cwd` 可省略(省略时 daemon 在 `~/.agentuse/work/<task_id>/` 里跑)。

```json
{ "agent": "my-harness", "input": { "任意": "JSON" } }
```

直接给 `input`,原样交给 agent(自定义 harness 从 stdin 读到的就是它)。给了 `input` 就忽略 `prompt` / `cwd`。

- `agent` 必填，必须是登记过的 agent,否则 400。
- 返回 201 和 task 对象(`status` 为 `pending`)。任务只派给创建者自己的、开启了这个 agent 的在线 daemon;没有就一直排队。

## GET /api/tasks/{id}

```json
{
  "task": { "id": "tk_…", "status": "done", "…": "…" },
  "events": [
    { "id": 1, "task_id": "tk_…", "daemon_id": "dm_…", "kind": "progress", "payload": { "text": "hello from agentuse" }, "created_at": 1792800043 },
    { "id": 2, "task_id": "tk_…", "daemon_id": "dm_…", "kind": "done", "payload": { "ok": true, "output": "hello from agentuse" }, "created_at": 1792800045 }
  ]
}
```

`events` 是 daemon 的全部回报，按时间顺序。`kind` 是 `progress` / `done` / `failed`。
