# works

工作：一组[会话](sessions.md),有一份共用的说明，照 Claude Projects 的样子([设计](../designs/work.md))。会话用 `sessions.work_id` 指向它，一个会话最多属于一个工作。

工作属于建它的用户，只有主人(和 admin)能看、改、删。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 工作 id,如 `wk_xxx` |
| `user_id` | TEXT | 属于哪个 [用户](users.md) |
| `name` | TEXT | 名字，必填，最长 64 个字 |
| `instructions` | TEXT | 说明，这个工作里的会话每条消息都带上(写进会话目录的 CLAUDE.md / AGENTS.md);可为空 |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次变化：改了工作本身，或者里面有会话新建、移入、发了消息 |

```sql
CREATE TABLE works (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id),
  name         TEXT NOT NULL,
  instructions TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX works_user ON works(user_id, updated_at);
```

删除工作时里面的会话保留,`sessions.work_id` 变空(`ON DELETE SET NULL`)。
