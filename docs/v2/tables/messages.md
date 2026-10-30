# messages

会话里的消息，按顺序一条接一条，只追加不修改。

- `user`:用户发的。`content` 是交给 harness 的输入，内置 harness 读 `{"prompt": "..."}`,自定义 harness 拿到原文。
- `assistant`:harness 回的，daemon 回报结果时写入。`content` 是 `{"ok": bool, "output": "...", "error": "..."}`,内置 harness 可能多带 `session_id`、`cost_usd`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `session_id` | TEXT | 所属 [会话](sessions.md) |
| `role` | TEXT | `user` / `assistant` |
| `content` | TEXT | 内容(JSON),见上 |
| `created_at` | INTEGER | 时间(unix 秒) |

```sql
CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX messages_session ON messages(session_id, id);
```

daemon 每次领到的是会话里最后一条 `user` 消息;回报 `done` / `failed` 时追加一条 `assistant` 消息。

## 多 agent 会话方案（待实现）

[session-mentions](../designs/session-mentions.md) 中，一条 user 消息可对应多个目标 run；assistant 消息记录 participant_id、run_id 和触发消息，消息按 session 内 seq 排序。当前表和“最后一条 user 消息”派活规则只用于 legacy 模式。
