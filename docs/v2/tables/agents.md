# agents

用户自己起名字的 agent。名字是给人看、给人理解的(如「写代码助手」「周报小秘书」)。

一个 agent 绑定一个**运行时**:某台 [daemon](daemons.md) 加上它上面的某个 [harness](harnesses.md),也就是 [daemon_harnesses](daemon_harnesses.md) 里的一行。运行时可以随时切换，切换时 daemon 和 harness 一起换(比如从「笔记本上的 Claude Code」换到「台式机上的 Codex」),agent 本身不变。

开 [会话](sessions.md) 时由接口传 `agent_id` 指定用哪个 agent,可以是别人的。会话跑在 agent 绑定的运行时上。只有主人能改、删 agent。

- **运行时只能选主人自己的**:`daemon_id` 必须是 agent 主人的 daemon,`(daemon_id, harness)` 必须在 daemon_harnesses 里有这一行(这台电脑上报过这个 harness)。
- **切换运行时**:只影响之后新建的会话。已有会话在建的时候就记下了当时的 daemon 和 harness,续接靠的是那台电脑上那个 harness 自己的会话(`harness_session_id`),换不了，所以不跟着变。
- **daemon 被删**:绑定它的 agent 的 `daemon_id` 变空,agent 没有运行时，开不了新会话，要先切换到别的运行时。
- **删除 agent**:用过它的会话保留，会话的 `agent_id` 变空，照样能接着回复。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | agent id,**全局唯一**,如 `code-helper`。建 agent 时由用户自己起，只能用小写字母、数字、`-` 和 `_`,以字母或数字开头，最长 64 个字符;不传时生成 `ag_xxx`。建好后不能改 |
| `user_id` | TEXT | 属于哪个 [用户](users.md) |
| `name` | TEXT | 显示名，任意文字，最长 64 个字;同一用户下不能重名 |
| `daemon_id` | TEXT | 运行时的 daemon,取 [daemons](daemons.md).id;daemon 被删后为空 |
| `harness` | TEXT | 运行时的 harness,取 [harnesses](harnesses.md).name;和 `daemon_id` 一起换 |
| `description` | TEXT | 说明，可为空 |
| `created_at` | INTEGER | 创建时间(unix 秒) |
| `updated_at` | INTEGER | 最近一次修改时间 |

```sql
CREATE TABLE agents (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id),
  name        TEXT NOT NULL,
  daemon_id   TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  harness     TEXT NOT NULL REFERENCES harnesses(name),
  description TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX agents_user_name ON agents(user_id, name);
```

还有 agent 用着某个 harness 时，这个 harness 不能删。

## 多 agent 会话方案（待实现）

[session-mentions](../designs/session-mentions.md) 由每条消息的 @ 目标直接选择 agent，不建立 session-agent 参与关系。agent 仍配置自身运行时，每次 run 保存当时的运行时快照；所有 agent 执行时读取同一个 session 的消息和附件。
