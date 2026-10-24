# agents

用户自己起名字的 agent。一个 agent 映射一个 [harness](harnesses.md):名字是给人看、给人理解的(如「写代码助手」「周报小秘书」),背后用哪个 harness 跑可以随时换,agent 本身不变。

用户用 agent 开 [会话](sessions.md),不直接选 harness。任何用户都能用别人的 agent(用 id @ 对方的 agent):会话跑在 **agent 主人**的 daemon 上、用 agent 映射的 harness。只有主人能改、删 agent。

- **换 harness**:只影响之后新建的会话。已有会话在建的时候就记下了当时的 harness,续接靠的是那个 harness 自己的会话(`harness_session_id`),换不了，所以不跟着变。
- **删除 agent**:用过它的会话保留，会话的 `agent_id` 变空，照样能接着回复。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | agent id,**全局唯一**,不同用户之间用它 @ 对方的 agent(如 `@code-helper`)。建 agent 时由用户自己起，只能用小写字母、数字、`-` 和 `_`,以字母或数字开头，最长 64 个字符;不传时生成 `ag_xxx`。建好后不能改 |
| `user_id` | TEXT | 属于哪个 [用户](users.md) |
| `name` | TEXT | 显示名，任意文字，最长 64 个字;同一用户下不能重名 |
| `harness` | TEXT | 现在映射到哪个 harness,取 [harnesses](harnesses.md).name |
| `description` | TEXT | 说明，可为空 |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次修改时间 |

```sql
CREATE TABLE agents (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id),
  name        TEXT NOT NULL,
  harness     TEXT NOT NULL REFERENCES harnesses(name),
  description TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX agents_user_name ON agents(user_id, name);
```

还有 agent 映射到某个 harness 时，这个 harness 不能删。
