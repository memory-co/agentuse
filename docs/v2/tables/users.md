# users

使用 agentuse 的人。角色只分兩種:

- **admin**:管理員。管理內置 harness、查看和處理所有人的 daemon 與會話。
- **user**:普通用戶。只能管理自己的 daemon、agent、自己註冊的自定義 harness、自己的會話。

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | TEXT PK | 用戶 id,**由調用方在創建時傳入**,一般就是接入方自己系統裏的 user id,如 `u_10086`;只能用字母、數字和 `. _ - : @`,最長 128 個字符。不傳時生成一個 UUID(v4)。其他表的 `user_id` / `owner_id` 都存這個值 |
| `name` | TEXT | 用戶名，唯一 |
| `role` | TEXT | `admin` / `user`,默認 `user` |
| `token_hash` | TEXT | 登錄令牌(`ut_` 開頭)的 SHA-256,不存明文;網頁 cookie 和 API 的 `Authorization: Bearer` 都用這個令牌 |
| `created_at` | INTEGER | 創建時間(unix 秒) |

```sql
CREATE TABLE users (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  role       TEXT NOT NULL DEFAULT 'user',
  token_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX users_token ON users(token_hash);
```

服務第一次啓動、庫裏沒有用戶時，自動建一個管理員 `admin`(id 爲 UUID),把登錄鏈接打印在日誌裏。之後管理員在頁面上建其他用戶。令牌丟了由管理員用 `agentuse-server user token <用戶 id>` 重置;管理員自己的令牌丟了用 `agentuse-server start -reset-admin-token`。

歸屬關係:

| 表 | 字段 | 含義 |
|---|---|---|
| [daemons](daemons.md) | `user_id` | daemon 屬於誰 |
| [agents](agents.md) | `user_id` | agent 屬於誰 |
| [harnesses](harnesses.md) | `owner_id` | 自定義 harness 由誰註冊，內置爲空 |
| [sessions](sessions.md) | `user_id` | 會話屬於誰(agent 的主人);只派給這個用戶自己的 daemon |
| [sessions](sessions.md) | `created_by` | 會話是誰發起的(可以用別人的 agent) |
