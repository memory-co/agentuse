# users

用戶管理，只有 admin 能調用，普通用戶返回 403。表結構見 [tables/users](../tables/users.md)。

## 用戶對象

```json
{
  "id": "u_10086",
  "name": "alice",
  "role": "user",
  "created_at": 1792799993
}
```

`role` 是 `admin` 或 `user`。

## GET /api/users

列出所有用戶，按創建時間排序。

## POST /api/users

```json
{ "id": "u_10086", "name": "alice", "role": "user" }
```

- `id`:用戶 id,直接填你們系統裏已有的 user id,之後 daemon、會話都歸屬到它。只能用字母、數字和 `. _ - : @`,最長 128 個字符。可省略，省略時生成一個 UUID。
- `role` 可省略，默認 `user`。

返回 201:

```json
{
  "user": { "id": "u_10086", "name": "alice", "role": "user", "created_at": 1792799993 },
  "token": "ut_…",
  "login_url": "https://agentuse.example.com/login?token=ut_…"
}
```

`token` 只在這裏出現一次，庫裏只存哈希。id 或用戶名重複、id 格式不對，都返回 400。

命令行等價:`agentuse-server user create alice -id u_10086 [-role admin]`。

## POST /api/users/{id}/token

給用戶換一個新的登錄令牌，舊的立即失效。沒有請求體。返回 200,格式同上:

```json
{ "user": { "id": "u_10086", "…": "…" }, "token": "ut_…", "login_url": "https://…/login?token=ut_…" }
```

用戶不存在返回 404。命令行等價:`agentuse-server user token u_10086`。
