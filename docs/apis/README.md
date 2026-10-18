# API Reference

hub 的 HTTP 接口全部挂在 `/v1/` 下,请求 / 响应 JSON。起服务 `agentuse hub start`,默认 `http://127.0.0.1:7700`。

- 机制 / 设计决策见 [`../designs/`](../designs/README.md)
- 数据结构见 [`../structs/`](../structs/README.md)

几类调用方,各自的认证:

| 调用方 | 认证 | 页面 |
|---|---|---|
| 平台 | HMAC 签名(平台密钥):notify 签请求体;管理接口签「方法 + 路径 + 请求体」 | [platform.md](platform.md) |
| 用户电脑(接入) | 接入码本身就是凭据,放在请求体里 | [daemon.md](daemon.md#post-v1connect) |
| daemon | `X-AgentUse-Device: <设备 id>` + `Authorization: Bearer <设备密钥>` | [daemon.md](daemon.md) |
| agent | `Authorization: Bearer <运行令牌>` | [agent.md](agent.md) |
| 实例主人 | `Authorization: Bearer <admin key>` | [admin.md](admin.md) |

| 方法 | 端点 | 调用方 | 说明 |
|---|---|---|---|
| `GET` | `/v1/health` | 任何 | 健康检查 |
| `POST` | `/v1/platforms/{platform}/notify` | 平台 | 一条评论存库了;hub 找出 @ 到的 agent,逐个判唤醒、找会话、排运行 |
| `POST` | `/v1/platforms/{platform}/agents` | 平台 | 替用户建 agent,交回第一条接入命令 |
| `GET` | `/v1/platforms/{platform}/agents` | 平台 | 列 agent(`?owner=` 只列某人的),带在线状态 |
| `GET` | `/v1/platforms/{platform}/agents/{agent}` | 平台 | 查一个 agent |
| `PATCH` | `/v1/platforms/{platform}/agents/{agent}` | 平台 | 改 `wake` / `actions` / `workspace_mode` / `agent_user_id` |
| `DELETE` | `/v1/platforms/{platform}/agents/{agent}` | 平台 | 删 agent;排队中和进行中的运行判失败 |
| `POST` | `/v1/platforms/{platform}/agents/{agent}/connect-code` | 平台 | 重新发一条接入码,旧的作废 |
| `DELETE` | `/v1/platforms/{platform}/devices/{device}` | 平台 | 吊销一台设备,绑在上面的 agent 变成未接入 |
| `POST` | `{callback_url}` | hub → 平台 | hub 签名的事件:reply / status / activity |
| `POST` | `/v1/connect` | 用户电脑 | `agentuse connect` 兑换接入码,拿设备 id + 设备密钥 |
| `GET` | `/v1/device` | daemon | 这台设备和绑在上面的 agent |
| `POST` | `/v1/device/disconnect` | daemon | 把一个 agent 从这台设备上解绑 |
| `GET` | `/v1/daemon/connect` | daemon | WebSocket 推送连接:发 ready,hub 推一条领好的运行 |
| `POST` | `/v1/daemon/claim` | daemon | 轮询领取下一条运行 + 运行令牌;没有 204 |
| `POST` | `/v1/runs/{run}/heartbeat` | daemon | 续租 |
| `POST` | `/v1/runs/{run}/complete` | daemon | 交回结果,回填 agent 会话 id |
| `GET` | `/v1/run` | agent | 看这枚令牌的范围 |
| `POST` | `/v1/run/reply` | agent | 回复评论 |
| `POST` | `/v1/run/status` | agent | 改线程对象的状态 |
| `POST` | `/v1/run/activity` | agent | 报进度 |
| `GET` | `/v1/admin/sessions` | 实例主人 | 列会话 |
| `GET` | `/v1/admin/runs` | 实例主人 | 列运行(按平台 / 会话 / agent / 状态过滤) |
| `GET` | `/v1/admin/runs/{run}` | 实例主人 | 读一条运行 |

命令行:[cli.md](cli.md)。

## 错误

统一 `{"error": "<code>"}`;`invalid` 另带 `message` 说明哪里不对:

| 状态 | error | 什么时候 |
|---|---|---|
| 400 | `bad_body` / `bad_notification` | 请求体不是合法 JSON,或 notify 缺 `id` / `thread` |
| 400 | `invalid` | 管理接口 / 接入的参数不对(handle 不合法、缺 `owner`、`wake` 取值不对、缺 `dir` 等),`message` 写明 |
| 401 | `bad_signature` | 平台签名缺失、不对或时间戳超出 5 分钟 |
| 401 | `bad_code` | 接入码不存在、已用过、已过期,或对应的 agent 已被删 |
| 401 | `unauthorized` | 设备 id 不存在、设备密钥不对或设备已被吊销;管理密钥不对或管理接口未开 |
| 401 | `bad_token` | 运行令牌格式不对、签名不对或已过期 |
| 403 | `forbidden` | 令牌的动作范围不含这个动作(如博客上 `status`) |
| 404 | `unknown_platform` / `not_found` | 平台没配置 / agent、设备、运行不存在(别的平台的 agent 也算不存在) |
| 409 | `handle_taken` | 建 agent 时 handle 在这个平台上已被占用 |
| 409 | `run_state` | 运行不在 claimed 状态(已结束),不是这台设备领的,或已被回收重领(attempt 对不上) |
| 502 | `callback_failed` | 回调平台失败(平台返回非 2xx 或连不上) |
