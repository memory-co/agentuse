# sessions

会话，对齐各 harness 自己的 session(如 Claude Code 的 session、Codex 的 thread)。一个会话是一段多轮对话：用户发一条消息,agent 回一条，可以一直往下回复。消息记在 [messages](messages.md)。

派给 daemon 的单位是会话：用户发了消息，会话变成 `pending`,daemon 领走后 `running`,agent 回完回到 `idle`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 会话 id,如 `ss_xxx` |
| `user_id` | TEXT | 创建它的 [用户](users.md);只派给这个用户自己的 daemon |
| `agent` | TEXT | 用哪个 CLI agent,取 [agents](agents.md).name;建会话时定下，之后不变 |
| `daemon_id` | TEXT | 跑这个会话的 daemon。第一次被领走时写入，之后只派给它(harness 的会话存在那台电脑上) |
| `cwd` | TEXT | daemon 本机的工作目录，可为空;为空时在 `~/.agentuse/work/<session_id>/`。建会话时定下 |
| `harness_session_id` | TEXT | harness 自己的会话 id(Claude Code 的 `session_id`、Codex 的 `thread_id`),daemon 回报结果时带回;之后每次带上它续接 |
| `title` | TEXT | 标题，默认取第一条消息 prompt 的前 50 个字 |
| `status` | TEXT | 见下方状态 |
| `lease_until` | INTEGER | 租约到期时间(unix 秒),派出时为 60 秒后,daemon 每 15 秒心跳续租;过期没续，回到 `pending` |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次变化时间 |

状态:

```
idle ──用户发消息──▶ pending ──派给 daemon──▶ running ──agent 回复(成功或失败)──▶ idle
                        ▲                        │
                        └──租约过期 / daemon 掉线─┘
```

- 只有 `idle` 时能发下一条消息。
- agent 失败也回到 `idle`,失败信息记在它回的那条消息里，可以接着发。

```sql
CREATE TABLE sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id),
  agent              TEXT NOT NULL REFERENCES agents(name),
  daemon_id          TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  cwd                TEXT,
  harness_session_id TEXT,
  title              TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'pending',
  lease_until        INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX sessions_status ON sessions(status, updated_at);
CREATE INDEX sessions_user ON sessions(user_id, updated_at);
```

daemon 被删后 `daemon_id` 变空，会话只能看、不能再发消息(harness 的会话在原来那台电脑上，换电脑续不上)。
