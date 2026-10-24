# turns

会话里的一轮：用户的一条输入，加上 agent 跑完给出的结果。派给 daemon 的单位是「轮」,租约、状态都在这里。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 轮 id,如 `tn_xxx` |
| `session_id` | TEXT | 所属 [会话](sessions.md) |
| `seq` | INTEGER | 会话里的第几轮，从 1 开始 |
| `status` | TEXT | 见下方状态 |
| `input` | TEXT | 这一轮的输入(JSON)。内置 agent 读 `{"prompt": "..."}`;自定义 harness 拿到原文 |
| `result` | TEXT | 结果(JSON),完成或失败时写入:`{"ok": bool, "output": "...", "error": "..."}` |
| `lease_until` | INTEGER | 租约到期时间(unix 秒),派出时为 60 秒后，daemon 每 15 秒心跳续租;过期没续，回到 `pending` |
| `created_at` | INTEGER | 创建时间 |
| `updated_at` | INTEGER | 最近一次状态变化时间 |
| `finished_at` | INTEGER | 进入 `done` / `failed` 的时间，未结束为空 |

状态:

```
pending ──派给 daemon──▶ running ──汇报成功──▶ done
   ▲                        │
   └──租约过期 / daemon 掉线─┤
                            └──汇报失败──▶ failed
```

- 一个会话同一时间只有一轮没结束：上一轮 `done` / `failed` 之后才能回复下一轮。
- 失败的一轮不影响会话，可以接着回复(harness 会话已经建起来的话照样续接)。

```sql
CREATE TABLE turns (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq         INTEGER NOT NULL,
  status      TEXT NOT NULL DEFAULT 'pending',
  input       TEXT NOT NULL,
  result      TEXT,
  lease_until INTEGER,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  finished_at INTEGER,
  UNIQUE (session_id, seq)
);
CREATE INDEX turns_status ON turns(status, created_at);
```
