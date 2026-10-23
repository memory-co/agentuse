# daemon_agents

每个 daemon 能用哪些 CLI agent。一个 daemon 一个 agent 一行。

daemon 每次连上服务时上报本机可用的 agent,服务用它覆盖该 daemon 的全部行。派任务时，服务只把 [tasks](tasks.md) 的 `agent` 在这里出现过的任务派给这台 daemon。

| 字段 | 类型 | 说明 |
|---|---|---|
| `daemon_id` | TEXT | 所属 [daemon](daemons.md) |
| `user_id` | TEXT | daemon 所属 [用户](users.md),冗余自 daemons.user_id,写入时一起写;派任务时可直接按 `(user_id, agent)` 查，不用连表 |
| `agent` | TEXT | 取 [agents](agents.md).name |
| `version` | TEXT | 本机 CLI 版本，可为空，仅供查看 |
| `updated_at` | INTEGER | 最近一次上报时间(unix 秒) |

```sql
CREATE TABLE daemon_agents (
  daemon_id  TEXT NOT NULL REFERENCES daemons(id),
  user_id    TEXT NOT NULL REFERENCES users(id),
  agent      TEXT NOT NULL REFERENCES agents(name),
  version    TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (daemon_id, agent)
);
CREATE INDEX daemon_agents_user_agent ON daemon_agents(user_id, agent);
```
