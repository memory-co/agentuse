# daemons

登记过的 daemon。一台用户电脑一行。

用户先在页面上「添加 daemon」,得到一条带一次性安装令牌的命令;在电脑上跑这条命令，令牌换成长期密钥，注册完成。流程见 [connect](../designs/connect.md)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | daemon id,如 `dm_xxx` |
| `user_id` | TEXT | 属于哪个 [用户](users.md) |
| `name` | TEXT | 显示名，如主机名 |
| `key_hash` | TEXT | 长期密钥(`dk_` 开头)的哈希，不存明文;注册前为空 |
| `setup_token` | TEXT | 一次性安装令牌(`st_` 开头),注册前有值，注册后清空;页面要反复展示安装命令，所以存明文 |
| `status` | TEXT | `pending`(已添加、未注册)/ `online` / `offline`,WebSocket 连上、断开时更新 |
| `last_seen_at` | INTEGER | 最近一次收到消息的时间(unix 秒) |
| `created_at` | INTEGER | 登记时间 |

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

「重新生成安装命令」会清掉 `key_hash`、生成新的 `setup_token`、状态回到 `pending`,原来那台电脑的连接立即断开。删除 daemon 时它的 daemon_harnesses 一起删;绑定它的会话 `daemon_id` 变空、不能再发消息;没回完的会话回到 `idle`,并追加一条失败的 `assistant` 消息。
