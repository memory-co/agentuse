# daemon_harnesses

每個 daemon 能用哪些 CLI harness、用戶開啓了哪些。一個 daemon 一個 harness 一行。

- **available**:daemon 每次連上服務時上報本機檢測到的 harness。上報裏有的標 `1`(沒有就新建一行),沒上報的標 `0`。行不刪，這樣用戶的開關不會因爲重連丟掉。
- **enabled**:用戶在頁面上爲這臺 daemon 打開的 harness。新檢測到的默認關閉，要用戶自己選。

一行就是一個**運行時**(daemon + harness),[agent](agents.md) 綁定的就是它，只能綁 `available = 1 AND enabled = 1` 的。派活時，服務只把 `daemon_id` 是這臺、`harness` 在這裏 `available = 1 AND enabled = 1` 的 [會話](sessions.md) 派給這臺 daemon。

| 字段 | 類型 | 說明 |
|---|---|---|
| `daemon_id` | TEXT | 所屬 [daemon](daemons.md) |
| `user_id` | TEXT | daemon 所屬 [用戶](users.md),冗餘自 daemons.user_id,寫入時一起寫;派活時可直接按 `(user_id, harness)` 查，不用連表 |
| `harness` | TEXT | 取 [harnesses](harnesses.md).name |
| `version` | TEXT | 本機 CLI 版本，可爲空，僅供查看 |
| `available` | INTEGER | 本機最近一次上報裏有沒有它(0/1) |
| `enabled` | INTEGER | 用戶是否開啓(0/1),默認 0 |
| `updated_at` | INTEGER | 最近一次上報時間(unix 秒) |

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
