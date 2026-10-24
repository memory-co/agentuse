# users

用户管理，只有 admin 能调用，普通用户返回 403。表结构见 [tables/users](../tables/users.md)。

## 用户对象

```json
{
  "id": "3f2b8c1e-9a4d-4e7b-b5a2-1c6d8e0f4a93",
  "name": "alice",
  "role": "user",
  "created_at": 1792799993
}
```

`role` 是 `admin` 或 `user`。

## GET /api/users

列出所有用户，按创建时间排序。

## POST /api/users

```json
{ "name": "alice", "role": "user" }
```

`role` 可省略，默认 `user`。返回 201:

```json
{
  "user": { "id": "…", "name": "alice", "role": "user", "created_at": 1792799993 },
  "token": "ut_…",
  "login_url": "https://agentuse.example.com/login?token=ut_…"
}
```

`token` 只在这里出现一次，库里只存哈希。用户名重复返回 400。
