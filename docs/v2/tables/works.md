# works

工作：一个协调者会话 + 若干线程会话 + 一份共用说明，照 Claude Projects 的样子([设计](../designs/work.md))。协调者和线程都是 [sessions](sessions.md),用 `sessions.work_id` 指向工作;哪个是协调者看 `coordinator_id`。

工作属于建它的用户，只有主人(和 admin)能看、改、删。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 工作 id,如 `wk_xxx` |
| `user_id` | TEXT | 属于哪个 [用户](users.md) |
| `name` | TEXT | 名字，必填，最长 64 个字 |
| `instructions` | TEXT | 说明，协调者和线程每条消息都带上(写进会话目录的 CLAUDE.md / AGENTS.md);可为空 |
| `coordinator_id` | TEXT | 协调者会话，建工作时一起建，不能换 |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次变化：改了工作本身，或者协调者、线程有新消息 |

```sql
CREATE TABLE works (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id),
  name           TEXT NOT NULL,
  instructions   TEXT NOT NULL DEFAULT '',
  coordinator_id TEXT NOT NULL UNIQUE,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX works_user ON works(user_id, updated_at);
```

删除工作时，协调者和线程一起删(应用里先删会话再删工作)。
