# Server

`agentuse-server` 负责管理用户、设备、Agent、skills、工作和共享会话，并通过 WebSocket 向 Client 投递消息、保存 Client 上报的执行结果。

使用 [Server CLI 手册](cli.md) 启动服务、设置存储、保存管理员登录信息及管理用户。`start` 在服务主机上运行；`login` 和 `user` 命令通过 HTTP 访问服务，可以在其他能连通 Server 的机器上运行。

服务默认监听 `:8080`。第一次启动时会创建管理员，并在日志中显示登录令牌和登录链接。登录管理页面后，访问 `/api/docs` 查看并调试 API；也可使用用户 Bearer token 读取 `/api/openapi.json` 或 `/api/openapi.yaml`。

[Client 使用文档](../client/README.md) 说明执行设备如何注册、启动及查看本机执行记录。开发和协议资料位于 [Server 设计](../../designs/v3/designs/server-main.md)。
