# sessions

会话，对齐各 harness 自己的 session(如 Claude Code 的 session、Codex 的 thread)。一个会话是一段多轮对话：用户发一条消息,harness 回一条，可以一直往下回复。消息记在 [messages](messages.md)。

会话用一个 [agent](agents.md) 开，由接口传入的 `agent_id` 指定，可以是自己的，也可以是别人的。会话属于 agent 的主人,`created_by` 记是谁发起的;主人和发起人都能看、能接着回复、能删。建的时候把 agent 当时的运行时(daemon + harness)记进 `daemon_id` 和 `harness`,之后不再跟着 agent 变：之后 agent 切换了运行时，这个会话也还在原来的 daemon 和 harness 上(续接只能用原来那台电脑上那个 harness 的会话)。

派给 daemon 的单位是会话：用户发了消息，会话变成 `pending`,daemon 领走后 `running`,harness 回完回到 `idle`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 会话 id,如 `ss_xxx` |
| `user_id` | TEXT | 会话属于哪个 [用户](users.md),就是 agent 的主人 |
| `created_by` | TEXT | 发起人 [用户](users.md);用自己的 agent 时和 `user_id` 相同 |
| `agent_id` | TEXT | 用哪个 [agent](agents.md) 开的;agent 删除后变空 |
| `harness` | TEXT | 用哪个 CLI harness,建会话时取 agent 运行时的 harness,之后不变 |
| `daemon_id` | TEXT | 跑这个会话的 daemon,建会话时取 agent 运行时的 daemon,之后只派给它(harness 的会话存在那台电脑上);daemon 被删后为空 |
| `cwd` | TEXT | daemon 本机的工作目录，可为空;为空时在 `~/.agentuse/work/<session_id>/`。建会话时定下 |
| `harness_session_id` | TEXT | harness 自己的会话 id(Claude Code 的 `session_id`、Codex 的 `thread_id`),daemon 回报结果时带回;之后每次带上它续接 |
| `title` | TEXT | 标题，默认取第一条消息 prompt 的前 50 个字 |
| `status` | TEXT | 见下方状态 |
| `lease_until` | INTEGER | 租约到期时间(unix 秒),派出时为 60 秒后,daemon 每 15 秒心跳续租;过期没续，回到 `pending` |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次变化时间 |

状态:

```
idle ──用户发消息──▶ pending ──派给 daemon──▶ running ──harness 回复(成功或失败)──▶ idle
                        ▲                        │
                        └──租约过期 / daemon 掉线─┘
```

- 只有 `idle` 时能发下一条消息。TODO:插话(harness 跑的过程中追加消息)以后用 harness 的 hook 来做。
- harness 失败也回到 `idle`,失败信息记在它回的那条消息里，可以接着发。

```sql
CREATE TABLE sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id),
  created_by         TEXT NOT NULL REFERENCES users(id),
  agent_id           TEXT REFERENCES agents(id) ON DELETE SET NULL,
  harness            TEXT NOT NULL REFERENCES harnesses(name),
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
CREATE INDEX sessions_created_by ON sessions(created_by, updated_at);
```

daemon 被删后 `daemon_id` 变空，会话只能看、不能再发消息(harness 的会话在原来那台电脑上，换电脑续不上)。
