# work_tokens

工作令牌(`wt_`):[工作](works.md)的协调者跑的时候，用本机的 `agentuse work` 命令开线程、看线程靠它([设计](../designs/work.md#协调者怎么开线程怎么收结果))。

- 服务把协调者的一条消息派给 daemon 时生成一个，明文只放在派活消息里(`work_token`),库里只存哈希。
- 只在那条消息还在跑时有效：协调者 `running` / `waiting`,并且这条消息还是它最后一条用户消息。这一轮结束(`done` / `failed`)时删掉;租约过期重派时会再发一个新的，旧的也就跟着失效了。
- 身份是工作的主人，只能调 [apis/works](../apis/works.md#工作令牌wt_) 里列的那几个接口。

| 字段 | 类型 | 说明 |
|---|---|---|
| `token_hash` | TEXT PK | 令牌的 SHA-256 |
| `work_id` | TEXT | 哪个[工作](works.md),工作删除时一起删 |
| `message_id` | INTEGER | 发令牌时派出的那条协调者[消息](messages.md) |
| `created_at` | INTEGER | 生成时间(unix 秒) |

```sql
CREATE TABLE work_tokens (
  token_hash TEXT PRIMARY KEY,
  work_id    TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX work_tokens_message ON work_tokens(message_id);
```
