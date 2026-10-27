# questions

harness 跑到一半向用户提的问题（Claude Code 的 `AskUserQuestion`），挂在当时正在回的那条 `user` [消息](messages.md) 上。怎么问、怎么答，见 [设计](../designs/question.md)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `session_id` | TEXT | 所属 [会话](sessions.md) |
| `message_id` | INTEGER | 在回哪条 `user` 消息 |
| `tool_use_id` | TEXT | harness 这次工具调用的 id，daemon 用它对上答案 |
| `questions` | TEXT | 问题和选项（JSON 数组），daemon 报上来的原样 |
| `answers` | TEXT | 用户的答案（JSON 对象，问题 → 答案），没答时为空 |
| `answered_by` | TEXT | 谁答的 [用户](users.md) |
| `status` | TEXT | `pending`（等回答）/ `answered`（答案已送到 harness）/ `expired`（超时或进程没了） |
| `created_at` | INTEGER | 时间(unix 秒) |
| `updated_at` | INTEGER | 状态最近一次变化的时间 |

- daemon 回报 `question` 时新建，会话变成 `waiting`。
- 用户回答时写 `answers` / `answered_by`，状态还是 `pending`，等 daemon 回报 `answered` 才变。
- daemon 回报 `question_expired`，或会话被收回时，`pending` 变成 `expired`。这时已经写了 `answers` 的，答案转成一条[插话](steers.md)。

```sql
CREATE TABLE questions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id  INTEGER NOT NULL,
  tool_use_id TEXT NOT NULL,
  questions   TEXT NOT NULL,
  answers     TEXT,
  answered_by TEXT REFERENCES users(id),
  status      TEXT NOT NULL DEFAULT 'pending',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX questions_session ON questions(session_id, id);
CREATE UNIQUE INDEX questions_tool_use ON questions(message_id, tool_use_id);
```
