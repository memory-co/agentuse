# steers

插话：会话还在 `pending` / `running` 时用户追加的话，挂在当时正在回的那条 `user` [消息](messages.md) 上。怎么送达见 [设计](../designs/steer.md)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `session_id` | TEXT | 所属 [会话](sessions.md) |
| `message_id` | INTEGER | 发出时正在回的那条 `user` 消息 |
| `created_by` | TEXT | 谁发的 [用户](users.md) |
| `prompt` | TEXT | 插话内容 |
| `status` | TEXT | `pending`(还没送达)/ `delivered`(已经塞给 harness)/ `queued`(没来得及送，转成了下一条消息) |
| `created_at` | INTEGER | 时间(unix 秒) |
| `updated_at` | INTEGER | 状态最近一次变化的时间 |

- daemon 回报 `steered` 时 `pending` → `delivered`。
- 这条消息的 `done` / `failed` 到达时，剩下的 `pending` 全部变成 `queued`，它们的 prompt 用空行拼成一条新的 `user` 消息，会话回到 `pending`。

```sql
CREATE TABLE steers (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(id),
  prompt     TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX steers_message ON steers(message_id, id);
CREATE INDEX steers_session ON steers(session_id, id);
```
