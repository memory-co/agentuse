# Client 本机 API

Client 本机服务用于查看 endpoint、conversation、session、run，并通过内置 local 来源调试执行。它使用本机地址与本机登录令牌，和 [Server API](../server/api.md) 分开。

## 地址与在线文档

先运行 `agentuse start`，再用 `agentuse ui --open` 输出并打开登录链接。默认地址为 `127.0.0.1:7801`；配置方法见 [Client CLI](cli.md#本机设置)。登录后访问 `/api/docs`，或读取 `/api/openapi.json`、`/api/openapi.yaml`。JSON Schema 位于 `/api/schemas/{schema}.json`。

接口接受本机 Bearer token 或登录后的 HttpOnly cookie。本机令牌保存在 `~/.agentuse/ui.token`，无需使用 Server 用户令牌或设备密钥。

## 配置、来源和查询

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/settings` | 当前进程加载的设置 |
| GET | `/api/endpoints` | 来源列表，包含 local，不含设备密钥 |
| GET | `/api/harnesses`、`/api/local/harnesses` | 启动时固定的执行器探测结果 |
| GET | `/api/status` | 连接和执行状态 |
| GET | `/api/conversations?endpoint_id=…` | 指定来源的 conversation 列表 |
| GET | `/api/conversations/{endpoint}/{conversation}` | conversation 信息和工作目录 |
| GET | `/api/conversations/{endpoint}/{conversation}/sessions` | session 列表 |
| GET | `/api/conversations/{endpoint}/{conversation}/sessions/{session}`、同路径 `/status` | session 元数据 / 原生执行状态 |
| GET | `/api/conversations/{endpoint}/{conversation}/sessions/{session}/runs` | 当前 session 的 run 列表 |
| GET | `/api/conversations/{endpoint}/{conversation}/sessions/{session}/runs/{run}` | 指定 run 的事件及结果 |
| GET | `/api/conversations/{endpoint}/{conversation}/sessions/{session}/usage` | 保留记录的用量统计 |

列表分页使用 `limit`、`cursor`；run 列表和统计可用 `status`、`since`、`until` 筛选，时间为 Unix 毫秒。没有跨所有 endpoint/session 的全局 run 枚举接口。

## 本地执行

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/local/conversations` | 用 `{"title":"本地调试"}` 创建 conversation |
| POST | `/api/local/conversations/{conversation}/runs` | 启动执行，等待 accepted 后返回实际 Run，HTTP 202 |
| POST | `/api/local/conversations/{conversation}/sessions/{session}/send` | 向指定 session 发输入，返回 accepted 对应的 Run，HTTP 200 |
| POST | `/api/local/conversations/{conversation}/sessions/{session}/reply` | 提交 `request_id`、`answer`，成功返回 `{"ok":true}` |
| POST | `/api/local/conversations/{conversation}/sessions/{session}/cancel` | 取消该 session 当前及排队工作，成功返回 `{"ok":true}` |

启动执行的请求示例：

```json
{"harness":"codex","prompt":"检查当前目录并简要说明"}
```

还可提供 `model`、`agent_instructions`、`skill_packages`，其中技能是本机 ZIP 路径。当前内置适配器支持 Linux，要求 Python 3.9+ 及已配置的原生 CLI。实际 run UUID 来自 accepted；补充输入可能继续同一 run，也可能排队形成下一次执行。

文件读写及会话协作通过执行环境中的 `context`，见 [执行上下文](context.md)。本机 API 不替代 Server 的会话管理与文件接口。底层会话、目录与恢复模型见 [Client 设计](../../designs/v3/client-main.md)。
