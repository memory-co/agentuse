# 使用文档

这里说明当前已实现功能的使用方法。设计方案、协议设计和历史资料位于 [designs](../designs/README.md)。

AgentUse 包含两个独立程序：

| 程序 | 用途 | 文档 |
|---|---|---|
| `agentuse-server` | 启动中心服务，以及通过 HTTP 管理用户和工作线程 | [Server](server/README.md) · [CLI](server/cli.md) |
| `agentuse` | 在执行设备上运行 Client、连接 Server、调用本机 harness，并提供会话、附件和执行上下文命令 | [Client](client/README.md) · [CLI](client/cli.md) |

部署服务从 [Server CLI 的快速开始](server/cli.md#快速开始) 读起；接入执行设备或进行本地调试，从 [Client CLI 的快速开始](client/cli.md#快速开始) 读起。文中的地址、令牌和资源 ID 均为示例，执行时替换为自己的值。

两端运行后均提供 Huma 自动生成的 API 文档：登录对应服务，再访问其 `/api/docs`。OpenAPI 位于 `/api/openapi.json`、`/api/openapi.yaml`。
