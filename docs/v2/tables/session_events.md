# session_events

daemon 跑会话时的每一次回报(进度、结果),只追加不修改。用来回看 agent 回每条消息时是怎么跑的。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `session_id` | TEXT | 所属 [会话](sessions.md) |
| `message_id` | INTEGER | 在回哪条 `user` [消息](messages.md) |
| `daemon_id` | TEXT | 回报的 daemon(不加外键:daemon 删了，历史回报照样留着) |
| `kind` | TEXT | `progress` / `done` / `failed` |
| `payload` | TEXT | 回报内容(JSON) |
| `created_at` | INTEGER | 收到的时间(unix 秒) |

`payload` 约定:`progress` 是 `{"text": "..."}`(自定义 harness 可以带任意字段);`done` / `failed` 是 `{"ok": bool, "output": "...", "error": "...", "session_id": "..."}`。

收到 `done` 或 `failed` 时，追加一条 `assistant` [消息](messages.md)(内容就是 payload),会话回到 `idle`;payload 里带了 `session_id`(harness 的会话 id)就写进 [sessions](sessions.md).`harness_session_id`。每条回报都顺带续租。回报的 `message_id` 已经不是这台 daemon 正在跑的(租约过期被收回、或已经回过)时，回报被拒，服务告诉 daemon 停掉它。

```sql
CREATE TABLE session_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  daemon_id  TEXT NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX session_events_session ON session_events(session_id, id);
```
