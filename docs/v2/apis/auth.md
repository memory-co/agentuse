# auth

用戶用登錄令牌(`ut_` 開頭)登錄。服務第一次啓動時自動建管理員 `admin`,在日誌裏打印登錄鏈接;其他用戶由管理員在 [users](users.md) 裏創建。管理員可以用 [`POST /api/users/{id}/token`](users.md#post-apiusersidtoken) 或 `agentuse-server user token <用戶 id>` 給別人重置令牌;管理員自己的令牌丟了，用 `agentuse-server start -reset-admin-token` 重啓服務找回。

## GET /login?token=ut_…

登錄鏈接。令牌有效就寫 cookie 並 302 到 `/`;無效就 302 到 `/?login=invalid`。

## POST /api/login

```json
{ "token": "ut_…" }
```

成功返回 200 和[用戶](users.md#用戶對象),同時寫 cookie `agentuse_token`(HttpOnly,一年有效;服務地址是 https 時帶 Secure)。令牌無效返回 401。

## POST /api/logout

清掉 cookie,返回 204。

## GET /api/me

返回當前登錄的[用戶](users.md#用戶對象)。
