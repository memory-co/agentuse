# users

使用 agentuse 的人。角色只分两种:

- **admin**:管理员。管理内置 harness、查看和处理所有人的 daemon 与会话。
- **user**:普通用户。只能管理自己的 daemon、agent、自己注册的自定义 harness、自己的会话。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | TEXT PK | 用户 id,**由调用方在创建时传入**,一般就是接入方自己系统里的 user id,如 `u_10086`;只能用字母、数字和 `. _ - : @`,最长 128 个字符。不传时生成一个 UUID(v4)。其他表的 `user_id` / `owner_id` 都存这个值 |
| `name` | TEXT | 用户名，唯一 |
| `role` | TEXT | `admin` / `user`,默认 `user` |
| `token_hash` | TEXT | 登录令牌(`ut_` 开头)的 SHA-256,不存明文;网页 cookie 和 API 的 `Authorization: Bearer` 都用这个令牌 |
| `created_at` | INTEGER | 创建时间(unix 秒) |

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

服务第一次启动、库里没有用户时，自动建一个管理员 `admin`(id 为 UUID),把登录链接打印在日志里。之后管理员在页面上建其他用户。令牌丢了由管理员用 `agentuse-server user token <用户 id>` 重置;管理员自己的令牌丢了用 `agentuse-server start -reset-admin-token`。

归属关系:

| 表 | 字段 | 含义 |
|---|---|---|
| [daemons](daemons.md) | `user_id` | daemon 属于谁 |
| [agents](agents.md) | `user_id` | agent 属于谁 |
| [harnesses](harnesses.md) | `owner_id` | 自定义 harness 由谁注册，内置为空 |
| [sessions](sessions.md) | `user_id` | 会话属于谁(agent 的主人);只派给这个用户自己的 daemon |
| [sessions](sessions.md) | `created_by` | 会话是谁发起的(可以 @ 别人的 agent) |
