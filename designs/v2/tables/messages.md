# messages

會話裏的消息，按順序一條接一條，只追加不修改。

- `user`:用戶發的。`content` 是交給 harness 的輸入，內置 harness 讀 `{"prompt": "..."}`,自定義 harness 拿到原文。
- `assistant`:harness 回的，daemon 回報結果時寫入。`content` 是 `{"ok": bool, "output": "...", "error": "..."}`,內置 harness 可能多帶 `session_id`、`cost_usd`。

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | INTEGER PK | 自增 |
| `session_id` | TEXT | 所屬 [會話](sessions.md) |
| `role` | TEXT | `user` / `assistant` |
| `content` | TEXT | 內容(JSON),見上 |
| `created_at` | INTEGER | 時間(unix 秒) |

```sql
CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX messages_session ON messages(session_id, id);
```

daemon 領取 run，其主任務和補充消息由 task_message_ids 指定；每次執行讀取整個 session 的共享歷史。最終結果寫入一條署名 assistant 消息。

## 多 agent 會話方案

[session-mentions](../designs/session-mentions.md) 中，一條 user 消息可對應多個目標 run；assistant 消息記錄 agent 身份快照、run_id 和觸發消息，消息按 session 內 seq 排序。每條消息的目標投遞記錄在 message_deliveries。
