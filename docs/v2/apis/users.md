# users

用户管理，只有 admin 能调用，普通用户返回 403。表结构见 [tables/users](../tables/users.md)。

## 用户对象

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

列出所有用户，按创建时间排序。

## POST /api/users

```json
{ "id": "u_10086", "name": "alice", "role": "user" }
```

- `id`:用户 id,直接填你们系统里已有的 user id,之后 daemon、任务都归属到它。只能用字母、数字和 `. _ - : @`,最长 128 个字符。可省略，省略时生成一个 UUID。
- `role` 可省略，默认 `user`。

返回 201:

```json
{
  "user": { "id": "u_10086", "name": "alice", "role": "user", "created_at": 1792799993 },
  "token": "ut_…",
  "login_url": "https://agentuse.example.com/login?token=ut_…"
}
```

`token` 只在这里出现一次，库里只存哈希。id 或用户名重复、id 格式不对，都返回 400。

命令行等价:`agentuse-server user create alice -id u_10086 [-role admin]`。

## POST /api/users/{id}/token

给用户换一个新的登录令牌，旧的立即失效。没有请求体。返回 200,格式同上:

```json
{ "user": { "id": "u_10086", "…": "…" }, "token": "ut_…", "login_url": "https://…/login?token=ut_…" }
```

用户不存在返回 404。命令行等价:`agentuse-server user token u_10086`。
