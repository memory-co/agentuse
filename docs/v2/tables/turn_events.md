# turn_events

daemon 对每一轮的每一次回报(进度、结果),只追加不修改。用来回看 agent 这一轮是怎么跑的。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `turn_id` | TEXT | 所属 [轮](turns.md) |
| `session_id` | TEXT | 所属 [会话](sessions.md),冗余自 turns,方便一次取出整个会话的回报 |
| `daemon_id` | TEXT | 回报的 daemon(不加外键:daemon 删了，历史回报照样留着) |
| `kind` | TEXT | `progress` / `done` / `failed` |
| `payload` | TEXT | 回报内容(JSON) |
| `created_at` | INTEGER | 收到的时间(unix 秒) |

`payload` 约定:`progress` 是 `{"text": "..."}`(自定义 harness 可以带任意字段);`done` / `failed` 是 `{"ok": bool, "output": "...", "error": "...", "session_id": "..."}`。

收到 `done` 或 `failed` 时，更新 [turns](turns.md) 的 `status`、`result`、`finished_at`;payload 里带了 `session_id`(harness 的会话 id)就写进 [sessions](sessions.md).`harness_session_id`。每条回报都顺带给这一轮续租。这一轮已经不归这台 daemon(租约过期被收回、或已结束)时，回报被拒，服务告诉 daemon 停掉它。

```sql
CREATE TABLE turn_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  turn_id    TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  daemon_id  TEXT NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX turn_events_turn ON turn_events(turn_id, id);
CREATE INDEX turn_events_session ON turn_events(session_id, id);
```
