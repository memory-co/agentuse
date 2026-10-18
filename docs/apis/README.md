# API Reference

hub 的 HTTP 接口全部挂在 `/v1/` 下,请求 / 响应 JSON。起服务 `agentuse hub start`,默认 `http://127.0.0.1:7700`。

- 机制 / 设计决策见 [`../designs/`](../designs/README.md)
- 数据结构见 [`../structs/`](../structs/README.md)

三类调用方,三种认证:

| 调用方 | 认证 | 页面 |
|---|---|---|
| 平台 | 请求体 HMAC 签名(平台密钥) | [platform.md](platform.md) |
| daemon | `X-AgentUse-Daemon` + `Authorization: Bearer <daemon key>` | [daemon.md](daemon.md) |
| agent | `Authorization: Bearer <运行令牌>` | [agent.md](agent.md) |

| 方法 | 端点 | 调用方 | 说明 |
|---|---|---|---|
| `GET` | `/v1/health` | 任何 | 健康检查 |
| `POST` | `/v1/platforms/{platform}/notify` | 平台 | 一条评论存库了;hub 判唤醒、找会话、排运行 |
| `POST` | `{callback_url}` | hub → 平台 | hub 签名的事件:reply / status / activity |
| `POST` | `/v1/daemon/claim` | daemon | 领取下一条运行 + 运行令牌;队列空 204 |
| `POST` | `/v1/runs/{run}/complete` | daemon | 交回结果,回填 agent 会话 id |
| `GET` | `/v1/run` | agent | 看这枚令牌的范围 |
| `POST` | `/v1/run/reply` | agent | 回复评论 |
| `POST` | `/v1/run/status` | agent | 改线程对象的状态 |
| `POST` | `/v1/run/activity` | agent | 报进度 |

命令行:[cli.md](cli.md)。

## 错误

统一 `{"error": "<code>"}`:

| 状态 | error | 什么时候 |
|---|---|---|
| 400 | `bad_body` / `bad_notification` | 请求体不是合法 JSON,或 notify 缺 `id` / `thread` |
| 401 | `bad_signature` | 平台签名缺失、不对或时间戳超出 5 分钟 |
| 401 | `unauthorized` | daemon id 未登记或密钥不对 |
| 401 | `bad_token` | 运行令牌格式不对、签名不对或已过期 |
| 403 | `forbidden` | 令牌的动作范围不含这个动作(如博客上 `status`) |
| 404 | `unknown_platform` / `not_found` | 平台没配置 / 运行不存在 |
| 409 | `run_state` | 运行不在 claimed 状态(已结束),或不是这个 daemon 领的 |
| 502 | `callback_failed` | 回调平台失败(平台返回非 2xx 或连不上) |
