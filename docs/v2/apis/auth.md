# auth

用户用登录令牌(`ut_` 开头)登录。服务第一次启动时自动建管理员 `admin`,在日志里打印登录链接;其他用户由管理员在 [users](users.md) 里创建。令牌丢了用 `agentuse user token <用户名>` 重置。

## GET /login?token=ut_…

登录链接。令牌有效就写 cookie 并 302 到 `/`;无效就 302 到 `/?login=invalid`。

## POST /api/login

```json
{ "token": "ut_…" }
```

成功返回 200 和[用户](users.md#用户对象),同时写 cookie `agentuse_token`(HttpOnly,一年有效;服务地址是 https 时带 Secure)。令牌无效返回 401。

## POST /api/logout

清掉 cookie,返回 204。

## GET /api/me

返回当前登录的[用户](users.md#用户对象)。
