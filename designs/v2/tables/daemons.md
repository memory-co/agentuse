# daemons

登記過的 daemon。一臺用戶電腦一行。

用戶先在頁面上「添加 daemon」,得到一條帶一次性安裝令牌的命令;在電腦上跑這條命令，令牌換成長期密鑰，註冊完成。流程見 [connect](../designs/connect.md)。

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | TEXT PK | daemon id,如 `dm_xxx` |
| `user_id` | TEXT | 屬於哪個 [用戶](users.md) |
| `name` | TEXT | 顯示名，如主機名 |
| `key_hash` | TEXT | 長期密鑰(`dk_` 開頭)的哈希，不存明文;註冊前爲空 |
| `setup_token` | TEXT | 一次性安裝令牌(`st_` 開頭),註冊前有值，註冊後清空;頁面要反覆展示安裝命令，所以存明文 |
| `status` | TEXT | `pending`(已添加、未註冊)/ `online` / `offline`,WebSocket 連上、斷開時更新 |
| `last_seen_at` | INTEGER | 最近一次收到消息的時間(unix 秒) |
| `created_at` | INTEGER | 登記時間 |

```sql
CREATE TABLE daemons (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id),
  name         TEXT NOT NULL,
  key_hash     TEXT,
  setup_token  TEXT,
  status       TEXT NOT NULL DEFAULT 'pending',
  last_seen_at INTEGER,
  created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX daemons_key ON daemons(key_hash);
CREATE UNIQUE INDEX daemons_setup_token ON daemons(setup_token);
```

「重新生成安裝命令」會清掉 `key_hash`、生成新的 `setup_token`、狀態回到 `pending`,原來那臺電腦的連接立即斷開。刪除 daemon 時它的 daemon_harnesses 一起刪;綁定它的 [agent](agents.md) `daemon_id` 變空(要切換運行時才能再開會話);綁定它的會話 `daemon_id` 變空、不能再發消息;沒回完的會話回到 `idle`,並追加一條失敗的 `assistant` 消息。
