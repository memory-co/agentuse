# users

使用 agentuse 的人。角色只分两种:

- **admin**:管理员。管理内置 agent、查看和处理所有人的 daemon 与任务。
- **user**:普通用户。只能管理自己的 daemon、自己注册的自定义 agent、自己创建的任务。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 用户 id,UUID(v4,小写带连字符),如 `3f2b8c1e-9a4d-4e7b-b5a2-1c6d8e0f4a93`;其他表的 `user_id` / `owner_id` 都存这个值 |
| `name` | TEXT | 用户名，唯一 |
| `role` | TEXT | `admin` / `user`,默认 `user` |
| `created_at` | INTEGER | 创建时间(unix 秒) |

```sql
CREATE TABLE users (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  role       TEXT NOT NULL DEFAULT 'user',
  created_at INTEGER NOT NULL
);
```

归属关系:

| 表 | 字段 | 含义 |
|---|---|---|
| [daemons](daemons.md) | `user_id` | daemon 属于谁 |
| [agents](agents.md) | `owner_id` | 自定义 agent 由谁注册，内置为空 |
| [tasks](tasks.md) | `user_id` | 任务由谁创建;只派给这个用户自己的 daemon |
