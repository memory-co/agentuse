# Server

`agentuse-server` 负责管理用户、设备、Agent、skills、工作和共享会话，并通过 WebSocket 向 Client 投递消息、保存 Client 上报的执行结果。

使用 [Server CLI 手册](cli.md) 启动服务、设置存储、保存管理员登录信息、管理用户和工作线程。`start` 在服务主机上运行；`login`、`user` 和 `work` 命令通过 HTTP 访问服务，可以在其他能连通 Server 的机器上运行。

服务默认监听 `:8080`。第一次启动时会创建管理员，并在日志中显示登录令牌和登录链接。登录管理页面后，访问 `/api/docs` 查看并调试 API；也可使用用户 Bearer token 读取 `/api/openapi.json` 或 `/api/openapi.yaml`。

[Client 使用文档](../client/README.md) 说明执行设备如何注册、启动及查看本机执行记录。开发和协议资料位于 [Server 设计](../../designs/v3/designs/server-main.md)。

[Work 手册](work.md) 说明跨 Client 编排、协调 Agent 的 Server 工具和工作授权。

文件交互采用两个方向：用户在 Server 上传输入供 AI 读取，AI 通过 `context attachment upload` 上传输出供用户在 Server 查看和下载。

[Conversation 接口与命名](conversations.md) 说明统一的 `/api/conversations`、SDK 方法、响应字段以及 Client 唯一的 `session_id`。

[对话文件与消息附件](conversation-files.md) 说明右侧文件树、user_input/ 路径和逐条消息的附件选择。

共享對話採用增量補齊：Client 選定實際本地 session 後，Server 用 `complete_messages` 提供上次已受理輸入之後、本次之前的消息，排除該 session 已產生的回覆；新 session 才補回先前歷史。本次正文不在補充列表重複出現。設備可通過 `GET /api/conversations/{id}/messages/{message_id}/agents/{agent_id}/context?session_id=...` 讀取此補充，權限限於已投遞給本設備的消息與 Agent。

網頁使用 `/api/browser/ws` 接收資源變更通知，並透過 HTTP 取得具體內容，不再固定輪詢。登入與 Acting as 沿用既有權限，斷線重連會補讀目前頁面。Agent 回覆與 Run details 的輸出支援 Markdown；使用者消息保留原文。

Server 按 v3 的 user/device/agent/skill/conversation/run/file/work 职责实现。设备管理统一 `/api/devices`，Client 注册和身份查询为 `/api/client/register`、`/api/client/me`；旧 daemon 接口已移除。消息正文与执行结果分开保存，文件字节共享 fileService 本地/S3 后端。
