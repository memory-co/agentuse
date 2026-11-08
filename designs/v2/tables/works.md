# works

工作：一個協調者會話 + 若干線程會話 + 一份共用說明，照 Claude Projects 的樣子([設計](../designs/work.md))。協調者和線程都是 [sessions](sessions.md),用 `sessions.work_id` 指向工作;哪個是協調者看 `coordinator_id`。

工作屬於建它的用戶，只有主人(和 admin)能看、改、刪。

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | TEXT PK | 工作 id,如 `wk_xxx` |
| `user_id` | TEXT | 屬於哪個 [用戶](users.md) |
| `name` | TEXT | 名字，必填，最長 64 個字 |
| `instructions` | TEXT | 說明，協調者和線程每條消息都帶上(寫進會話目錄的 CLAUDE.md / AGENTS.md);可爲空 |
| `coordinator_id` | TEXT | 協調者會話，建工作時一起建，不能換 |
| `created_at` | INTEGER | 創建時間(unix 秒) |
| `updated_at` | INTEGER | 最近一次變化：改了工作本身，或者協調者、線程有新消息 |

```sql
CREATE TABLE works (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id),
  name           TEXT NOT NULL,
  instructions   TEXT NOT NULL DEFAULT '',
  coordinator_id TEXT NOT NULL UNIQUE,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX works_user ON works(user_id, updated_at);
```

刪除工作時，協調者和線程一起刪(應用裏先刪會話再刪工作)。
