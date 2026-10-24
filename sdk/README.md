# SDK

给**网站后端**用的 agentuse 客户端，把 agentuse 嵌进自己的站点。思路见 [designs/embedding](../docs/v2/designs/embedding.md),接口见 [apis](../docs/v2/apis/README.md)。

| 语言 | 目录 | 依赖 |
|---|---|---|
| Python ≥ 3.8 | [python](python/README.md) | 无(标准库) |
| Node.js ≥ 18 | [nodejs](nodejs/README.md) | 无(自带 fetch),带 TypeScript 类型 |
| Go ≥ 1.24 | [golang](golang/README.md) | 无(标准库) |

三个版本用法一样:

1. 用 agentuse-server 的地址和 **admin 令牌**(服务密钥)建一个客户端。客户端不带状态，全局建一个共用就行。令牌只放在后端，不能下发到浏览器。
2. 每个方法的第一个参数(Go 里是 `ctx` 之后)直接传**网站的 user id**,请求会带 `X-Agentuse-User` 按这个用户执行，用户第一次出现时 agentuse 自动建。传空(`None` / `null` / `""`)就是 admin 本身。用户管理(`list_users`、`create_user`、`reset_user_token`)只有 admin 能调，不带这个参数。
3. 网站上的功能对应的方法:

| 网站上的功能 | 方法(Python / Node / Go) |
|---|---|
| 「连接我的电脑」 | `create_daemon` / `createDaemon` / `CreateDaemon`,把返回的 `setup_command` 给用户复制 |
| 看电脑在不在线 | `list_daemons` / `listDaemons` / `ListDaemons` |
| 开关某台电脑上的 harness | `set_daemon_harness` / `setDaemonHarness` / `SetDaemonHarness` |
| 发起一次任务 | `create_session` / `createSession` / `CreateSession` |
| 接着回复 | `send_message` / `sendMessage` / `SendMessage`(harness 还没回时 409) |
| 看进度和结果 | `get_session` / `getSession` / `GetSession`,或 `wait_session` 轮询到 `idle` |

另外还有 `me`、`list_sessions`、`delete_session`、`list_harnesses`、`create_harness`、`delete_harness`、`reset_daemon`、`delete_daemon`,以及 admin 用的 `list_users`、`create_user`、`reset_user_token`,和接口一一对应。

非 2xx 响应统一抛(Go 里返回)一个带 `status` 和服务端 `error` 说明的错误。
