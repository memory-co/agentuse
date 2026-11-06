# harnesses

全局有哪些 CLI harness 可以用。[agents](agents.md).harness、[sessions](sessions.md).harness 和 [daemon_harnesses](daemon_harnesses.md).harness 都必須是這裏登記過的 `name`。

分兩類:

- **內置**(`builtin`):agentuse 自帶適配，如 `claude`(Claude Code)、`codex`。每個是倉庫頂層 `internal/harness/<name>/harness.yaml`,服務啓動時把名字、顯示名、說明寫入([設計](../designs/harness.md))。
- **自定義**(`custom`):用戶自己手搓的 harness。用戶先在服務上註冊一個名字，再在自己的 daemon 上配置這個名字對應的本地啓動命令。daemon 按統一約定調用它：每條消息從 stdin 以 JSON 傳入，進度和結果從 stdout 按行輸出 JSON。只要遵守這個約定，任何程序都能接進來。

| 字段 | 類型 | 說明 |
|---|---|---|
| `name` | TEXT PK | 唯一名字，如 `claude`、`codex`、`my-harness`;只用小寫字母、數字和 `-` |
| `kind` | TEXT | `builtin` / `custom` |
| `display_name` | TEXT | 顯示名，如 `Claude Code` |
| `description` | TEXT | 說明，可爲空 |
| `owner_id` | TEXT | 註冊者 [用戶](users.md);內置爲空，自定義爲註冊它的用戶 |
| `created_at` | INTEGER | 註冊時間(unix 秒) |

```sql
CREATE TABLE harnesses (
  name         TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  display_name TEXT NOT NULL,
  description  TEXT,
  owner_id     TEXT REFERENCES users(id),
  created_at   INTEGER NOT NULL
);
```

本機的啓動命令不存在這裏：同一個 harness 在不同電腦上裝的位置不同，由各自的 daemon 配置。
