# attachments

> 设计见 [attachment](../designs/attachment.md)，接口见 [apis/attachments](../apis/attachments.md)。

一个 session 下的文件元数据，内容存服务本地磁盘。附件不归属某条消息，不存 BLOB、绝对路径或公开 URL。内容和元数据只创建、读取、删除，不更新。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 服务生成的附件 id，如 `at_xxx` |
| `session_id` | TEXT | 所属 session，删除会话时级联删除 |
| `filename` | TEXT | 规范化后的原始文件名，只用于展示和下载 |
| `content_type` | TEXT | 服务检测出的 MIME 类型 |
| `size_bytes` | INTEGER | 文件原始字节数，允许 0 |
| `sha256` | TEXT | 完整文件的 SHA-256，小写十六进制 |
| `created_by` | TEXT | 请求的用户身份；daemon 上传时是 daemon 主人 |
| `source` | TEXT | `user` / `daemon` / `work` |
| `daemon_id` | TEXT | daemon 上传时记录来源，其他来源为空；删除 daemon 后置空 |
| `created_at` | INTEGER | unix 秒 |

```sql
CREATE TABLE attachments (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  filename     TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256       TEXT NOT NULL CHECK (length(sha256) = 64),
  created_by   TEXT NOT NULL REFERENCES users(id),
  source       TEXT NOT NULL CHECK (source IN ('user', 'daemon', 'work')),
  daemon_id    TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  created_at   INTEGER NOT NULL
);
CREATE INDEX attachments_session ON attachments(session_id, created_at, id);
```

同名文件、同内容文件均不去重。磁盘路径按 session_id 和 id 推导；`download_path` 是 API 的派生字段，不入库。上传时的 `message_id` 只用于检查 daemon 执行权，不作为附件归属字段持久化。

会话配额按该 session 全部附件的 `SUM(size_bytes)` 核算，与插入放在同一写事务中。删除附件后即可释放逻辑配额，磁盘清理由服务补偿任务完成。

## daemon 附件能力

能力单独存在 `daemon_capabilities(daemon_id, capability)` 表，两个字段共同构成主键，`daemon_id` 外键指向 daemons 并级联删除。每次 hello 在事务里替换这台 daemon 的能力集，旧 hello 没有能力字段就清空。daemon API 的 `capabilities` 数组由该表生成。
