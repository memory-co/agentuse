# tasks

任务。服务收到任务时插入一行，派给 daemon、daemon 汇报时更新状态。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 任务 id,如 `tk_xxx` |
| `daemon_id` | TEXT | 领到它的 daemon,未派出时为空 |
| `status` | TEXT | 见下方状态 |
| `input` | TEXT | 任务内容(JSON),交给 agent 的东西 |
| `result` | TEXT | 最终结果(JSON),完成或失败时写入 |
| `lease_until` | INTEGER | 租约到期时间(unix 秒);daemon 没在此前汇报，任务回到 `pending` |
| `created_at` | INTEGER | 创建时间 |
| `updated_at` | INTEGER | 最近一次状态变化时间 |

状态:

```
pending ──派给 daemon──▶ running ──汇报成功──▶ done
   ▲                        │
   └──租约过期 / daemon 掉线─┤
                            └──汇报失败──▶ failed
```

```sql
CREATE TABLE tasks (
  id          TEXT PRIMARY KEY,
  daemon_id   TEXT REFERENCES daemons(id),
  status      TEXT NOT NULL DEFAULT 'pending',
  input       TEXT NOT NULL,
  result      TEXT,
  lease_until INTEGER,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX tasks_status ON tasks(status, created_at);
```
