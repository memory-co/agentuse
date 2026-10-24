# agents

全局有哪些 CLI agent 可以用。[sessions](sessions.md).agent 和 [daemon_agents](daemon_agents.md).agent 都必须是这里登记过的 `name`。

分两类:

- **内置**(`builtin`):agentuse 自带适配，如 `claude`(Claude Code)、`codex`。服务初始化时写入。
- **自定义**(`custom`):用户自己手搓的 harness。用户先在服务上注册一个名字，再在自己的 daemon 上配置这个名字对应的本地启动命令。daemon 按统一约定调用它：每条消息从 stdin 以 JSON 传入，进度和结果从 stdout 按行输出 JSON。只要遵守这个约定，任何程序都能接进来。

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | TEXT PK | 唯一名字，如 `claude`、`codex`、`my-harness`;只用小写字母、数字和 `-` |
| `kind` | TEXT | `builtin` / `custom` |
| `display_name` | TEXT | 显示名，如 `Claude Code` |
| `description` | TEXT | 说明，可为空 |
| `owner_id` | TEXT | 注册者 [用户](users.md);内置为空，自定义为注册它的用户 |
| `created_at` | INTEGER | 注册时间(unix 秒) |

```sql
CREATE TABLE agents (
  name         TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  display_name TEXT NOT NULL,
  description  TEXT,
  owner_id     TEXT REFERENCES users(id),
  created_at   INTEGER NOT NULL
);
```

本机的启动命令不存在这里：同一个 agent 在不同电脑上装的位置不同，由各自的 daemon 配置。
