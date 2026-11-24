# Server

`agentuse-server` 负责管理用户、设备、Agent、skills 和共享会话，并通过 WebSocket 向 Client 投递消息、保存 Client 上报的执行结果。

使用 [Server CLI 手册](cli.md) 启动服务、设置存储、保存管理员登录信息、管理用户。`start` 在服务主机上运行；`login` 和 `user` 命令通过 HTTP 访问服务，可以在其他能连通 Server 的机器上运行。

在 Kubernetes 上可以直接拉取预构建的 Server 镜像，通过 YAML 或 Helm 部署。[Kubernetes 部署](kubernetes.md)

[Server 自升级](upgrades.md) 支持管理员从页面或 CLI 异步下载、校验、替换和重启，并保存升级历史。

在 Server 主机的 `~/.agentuse/settings.yaml` 写入 `public_url: https://agentuse.example.com` 并重启，即可固定登录链接和安装命令使用的公开地址。`--public-url`、`AGENTUSE_PUBLIC_URL` 可临时覆盖文件值。

服务默认监听 `:8080`。安装脚本支持可选的管理员密码设置，输入密码后后台启动或重启脚本管理的 Server；回车跳过则保持手动启动。未设置密码时，首次启动会创建管理员，并在日志中显示登录令牌和登录链接。登录管理页面后，访问 `/api/docs` 查看并调试 API；也可使用用户 Bearer token 读取 `/api/openapi.json` 或 `/api/openapi.yaml`。

[API 手册](api.md)集中说明鉴权、设备、Agent、Skill、用户和 WebSocket 入口。[DTO 源码索引](api.md#dto-源码索引)可以直接查 Go 定义、接口绑定和前端生成类型。

[Client 使用文档](../client/README.md) 说明执行设备如何注册、启动及查看本机执行记录。开发和协议资料位于 [Server 设计](../../designs/v3/server-main.md)。

[对话协作](conversations.md#模式与嵌套) 说明单体/协作模式、子会话及结果回传。

文件交互采用两个方向：用户在 Server 上传输入供 AI 读取，AI 通过 `context attachment upload` 上传输出供用户在 Server 查看和下载。

[Conversation 接口与命名](conversations.md) 说明统一的 `/api/conversations`、SDK 方法、响应字段以及 Client 唯一的 `session_id`。

[对话文件与消息附件](conversation-files.md) 说明文件管理器弹层、user_input/ 路径和逐条消息的附件选择；协作模式右侧显示子会话列表。

共享对话按 Client 实际 session 增量补齐历史。接口使用场景、accepted 边界与多 Agent 示例见 [Client 共享历史补齐](conversations.md#client-共享历史补齐)。

網頁使用 `/api/browser/ws` 接收資源變更通知，並透過 HTTP 取得具體內容，不再固定輪詢。登入與 Acting as 沿用既有權限，斷線重連會補讀目前頁面。Agent 回覆與 Run details 的輸出支援 Markdown；使用者消息保留原文。

Server 按 v3 的 user/device/agent/skill/conversation/run/file 职责实现。设备管理统一 `/api/devices`，Client 注册和身份查询为 `/api/client/register`、`/api/client/me`；旧 daemon 接口已移除。消息正文与执行结果分开保存，文件字节共享 fileService 本地/S3 后端。

数据库默认使用 SQLite，也支持 MySQL、PostgreSQL，均无需 CGO。连接配置、首次切换和后续版本迁移见[数据库配置与升级](database.md)。
