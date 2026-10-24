# sessions

会话，对齐各 harness 自己的 session(如 Claude Code 的 session、Codex 的 thread)。一个会话是一段多轮对话：用户发一条，agent 回一条，可以一直往下回复。每一轮记在 [turns](turns.md)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 会话 id,如 `ss_xxx` |
| `user_id` | TEXT | 创建它的 [用户](users.md);只派给这个用户自己的 daemon |
| `agent` | TEXT | 用哪个 CLI agent,取 [agents](agents.md).name;建会话时定下，之后不变 |
| `daemon_id` | TEXT | 跑这个会话的 daemon。第一轮被领走时写入，之后每一轮都只派给它(harness 的会话存在那台电脑上) |
| `cwd` | TEXT | daemon 本机的工作目录，可为空;为空时在 `~/.agentuse/work/<session_id>/`。建会话时定下，每轮都在这里跑 |
| `harness_session_id` | TEXT | harness 自己的会话 id(Claude Code 的 `session_id`、Codex 的 `thread_id`),daemon 跑完第一轮回报;之后每轮带上它续接。没有就为空 |
| `title` | TEXT | 标题，默认取第一轮 prompt 的前 50 个字 |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一轮的变化时间 |

会话本身不存状态，看最后一轮:[turns](turns.md) 的 `status`。

```sql
CREATE TABLE sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id),
  agent              TEXT NOT NULL REFERENCES agents(name),
  daemon_id          TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  cwd                TEXT,
  harness_session_id TEXT,
  title              TEXT NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id, updated_at);
```

daemon 被删后 `daemon_id` 变空，会话只能看、不能再回复(harness 的会话在原来那台电脑上，换电脑续不上)。
