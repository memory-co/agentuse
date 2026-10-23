# daemons

登记过的 daemon。一台用户电脑一行。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | daemon id,如 `dm_xxx` |
| `name` | TEXT | 显示名，如主机名 |
| `key_hash` | TEXT | 认证密钥的哈希，不存明文 |
| `status` | TEXT | `online` / `offline`,WebSocket 连上、断开时更新 |
| `last_seen_at` | INTEGER | 最近一次收到消息的时间(unix 秒) |
| `created_at` | INTEGER | 登记时间 |

```sql
CREATE TABLE daemons (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  key_hash     TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'offline',
  last_seen_at INTEGER,
  created_at   INTEGER NOT NULL
);
```
