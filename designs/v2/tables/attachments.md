# attachments

> 設計見 [attachment](../designs/attachment.md)，接口見 [apis/attachments](../apis/attachments.md)。

一個 session 下的文件元數據，內容存服務本地磁盤。附件不歸屬某條消息，不存 BLOB、絕對路徑或公開 URL。內容和元數據只創建、讀取、刪除，不更新。

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | TEXT PK | 服務生成的附件 id，如 `at_xxx` |
| `session_id` | TEXT | 所屬 session，刪除會話時級聯刪除 |
| `filename` | TEXT | 規範化後的原始文件名，只用於展示和下載 |
| `content_type` | TEXT | 服務檢測出的 MIME 類型 |
| `size_bytes` | INTEGER | 文件原始字節數，允許 0 |
| `sha256` | TEXT | 完整文件的 SHA-256，小寫十六進制 |
| `created_by` | TEXT | 請求的用戶身份；daemon 上傳時是 daemon 主人 |
| `source` | TEXT | `user` / `daemon` / `work` |
| `daemon_id` | TEXT | daemon 上傳時記錄來源，其他來源爲空；刪除 daemon 後置空 |
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

同名文件、同內容文件均不去重。磁盤路徑按 session_id 和 id 推導；`download_path` 是 API 的派生字段，不入庫。上傳時的 `message_id` 只用於檢查 daemon 執行權，不作爲附件歸屬字段持久化。

會話配額按該 session 全部附件的 `SUM(size_bytes)` 覈算，與插入放在同一寫事務中。刪除附件後即可釋放邏輯配額，磁盤清理由服務補償任務完成。

## daemon 附件能力

能力單獨存在 `daemon_capabilities(daemon_id, capability)` 表，兩個字段共同構成主鍵，`daemon_id` 外鍵指向 daemons 並級聯刪除。每次 hello 在事務裏替換這臺 daemon 的能力集，舊 hello 沒有能力字段就清空。daemon API 的 `capabilities` 數組由該表生成。
