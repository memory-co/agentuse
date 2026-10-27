# daemon_harnesses

每个 daemon 能用哪些 CLI harness、用户开启了哪些。一个 daemon 一个 harness 一行。

- **available**:daemon 每次连上服务时上报本机检测到的 harness。上报里有的标 `1`(没有就新建一行),没上报的标 `0`。行不删，这样用户的开关不会因为重连丢掉。
- **enabled**:用户在页面上为这台 daemon 打开的 harness。新检测到的默认关闭，要用户自己选。

一行就是一个**运行时**(daemon + harness),[agent](agents.md) 绑定的就是它，只能绑 `available = 1 AND enabled = 1` 的。派活时，服务只把 `daemon_id` 是这台、`harness` 在这里 `available = 1 AND enabled = 1` 的 [会话](sessions.md) 派给这台 daemon。

| 字段 | 类型 | 说明 |
|---|---|---|
| `daemon_id` | TEXT | 所属 [daemon](daemons.md) |
| `user_id` | TEXT | daemon 所属 [用户](users.md),冗余自 daemons.user_id,写入时一起写;派活时可直接按 `(user_id, harness)` 查，不用连表 |
| `harness` | TEXT | 取 [harnesses](harnesses.md).name |
| `version` | TEXT | 本机 CLI 版本，可为空，仅供查看 |
| `available` | INTEGER | 本机最近一次上报里有没有它(0/1) |
| `enabled` | INTEGER | 用户是否开启(0/1),默认 0 |
| `updated_at` | INTEGER | 最近一次上报时间(unix 秒) |

```sql
CREATE TABLE daemon_harnesses (
  daemon_id  TEXT NOT NULL REFERENCES daemons(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id),
  harness    TEXT NOT NULL REFERENCES harnesses(name) ON DELETE CASCADE,
  version    TEXT,
  available  INTEGER NOT NULL DEFAULT 1,
  enabled    INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (daemon_id, harness)
);
CREATE INDEX daemon_harnesses_user_harness ON daemon_harnesses(user_id, harness);
```
