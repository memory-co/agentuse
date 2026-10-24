# 接口

agentuse 服务对外的全部接口。一组接口一个文件:

| 文件 | 内容 | 谁调用 |
|---|---|---|
| [auth](auth.md) | 登录、退出、当前用户 | 网页 / 脚本 |
| [users](users.md) | 用户管理(仅 admin) | 网页 / 脚本 |
| [harnesses](harnesses.md) | harness 列表、注册自定义 harness | 网页 / 脚本 |
| [daemons](daemons.md) | 添加 daemon、安装命令、开关 harness | 网页 / 脚本 |
| [sessions](sessions.md) | 开会话、发消息、看消息和回报 | 网页 / 脚本 |
| [setup](setup.md) | `setup.sh` 和二进制下载 | 用户电脑上的 curl |
| [daemon](daemon.md) | daemon 注册、自检、WebSocket 消息 | daemon |

## 约定

- 请求和响应都是 JSON(`Content-Type: application/json`),时间都是 unix 秒。
- **认证**:`/api/*` 里除了 `/api/login`、`/api/logout` 和 `/api/daemon/*`，都要带用户登录令牌(`ut_` 开头):网页用 cookie `agentuse_token`,脚本用 `Authorization: Bearer ut_…`。见 [auth](auth.md)。
- **代为用户**:admin 令牌可以再带 `X-Agentuse-User: <用户 id>`,按那个用户的身份调用，用户不存在就先建;普通用户令牌带这个头返回 403。嵌入网站时网站后端就这么用，见 [designs/embedding](../designs/embedding.md)。
- **权限**:普通用户只能看和改自己的 daemon、会话、自定义 harness;admin 能看和改所有人的。看不到的东西一律返回 404。
- **错误**:非 2xx 时响应体是 `{"error": "说明"}`。

| 状态码 | 含义 |
|---|---|
| 400 | 参数不对(说明在 `error` 里) |
| 401 | 没带令牌 / 令牌无效 |
| 403 | 需要管理员 |
| 404 | 不存在，或不属于你 |
