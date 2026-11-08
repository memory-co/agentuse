# Server

`agentuse-server` 负责管理用户、设备、Agent、skills、工作和共享会话，并通过 WebSocket 向 Client 投递消息、保存 Client 上报的执行结果。

使用 [Server CLI 手册](cli.md) 启动服务、设置存储、保存管理员登录信息、管理用户和工作线程。`start` 在服务主机上运行；`login`、`user` 和 `work` 命令通过 HTTP 访问服务，可以在其他能连通 Server 的机器上运行。

服务默认监听 `:8080`。第一次启动时会创建管理员，并在日志中显示登录令牌和登录链接。登录管理页面后，访问 `/api/docs` 查看并调试 API；也可使用用户 Bearer token 读取 `/api/openapi.json` 或 `/api/openapi.yaml`。

[Client 使用文档](../client/README.md) 说明执行设备如何注册、启动及查看本机执行记录。开发和协议资料位于 [Server 设计](../../designs/v3/designs/server-main.md)。

[Work 手册](work.md) 说明跨 Client 编排、协调 Agent 的 Server 工具和工作授权。

文件交互采用两个方向：用户在 Server 上传输入供 AI 读取，AI 通过 `context attachment upload` 上传输出供用户在 Server 查看和下载。

[对话文件与消息附件](conversation-files.md) 说明右侧文件树、user_input/ 路径和逐条消息的附件选择。
