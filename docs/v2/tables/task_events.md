# task_events

daemon 对任务的每一次回报(进度、日志、结果),只追加不修改。用来回看任务的执行过程。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `task_id` | TEXT | 所属任务 |
| `daemon_id` | TEXT | 回报的 daemon |
| `kind` | TEXT | `progress` / `done` / `failed` |
| `payload` | TEXT | 回报内容(JSON) |
| `created_at` | INTEGER | 收到的时间(unix 秒) |

收到 `done` 或 `failed` 时，同时更新 [tasks](tasks.md) 的 `status` 和 `result`。

```sql
CREATE TABLE task_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    TEXT NOT NULL REFERENCES tasks(id),
  daemon_id  TEXT NOT NULL REFERENCES daemons(id),
  kind       TEXT NOT NULL,
  payload    TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX task_events_task ON task_events(task_id, id);
```
