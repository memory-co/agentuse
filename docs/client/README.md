# Client

`agentuse` 在执行设备上运行，连接一个或多个 Server，接收发给 Agent 的消息，并通过本机 harness 执行。收到消息后，Client 按固定版本取得所需配置和 skills，在本地准备或复用 session，再把实际 run ID 和执行事件上报 Server。

AI 在每次执行中可以直接使用 `context status`、`context attachment list/download/upload` 查询执行身份与状态、取得图片等附件的本地路径，并把输出文件上传到 Server。人类用 `agentuse context` 显式选择 run，或通过 `enter` 进入其 shell。操作方法见 [执行上下文](context.md)。

使用 [Client CLI 手册](cli.md) 注册设备、启动 Client、打开本机页面，以及通过 `agentuse conversation` 操作远端会话。

没有注册 Server 时也可以运行 `agentuse start`，再用 `agentuse ui --open` 打开本地调试页面。默认地址为 `127.0.0.1:7801`，可在页面中选择已安装的 harness 实际执行。

本机页面启动后，先通过 `agentuse ui` 输出的链接登录，再访问本机 `/api/docs` 查看和调试 Client API。Server 的管理页面和 Client 的本机页面分别使用各自的登录凭据。

[Server 使用文档](../server/README.md) 说明中心服务如何部署。开发资料位于 [Client 设计](../../designs/v3/designs/client-main.md)，适配器接口见 [harnessctl](../../harnessctl/README.md)。

跨 Client 的 Work 编排与协调工具见 [Server Work](../server/work.md)。
