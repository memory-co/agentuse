# SDK

给**网站后端**用的 agentuse 客户端，把 agentuse 嵌进自己的站点。思路见 [Server 身份](../designs/v3/server-user.md),接口见 [apis](../docs/server/conversations.md)。

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
| 「连接我的电脑」 | `create_device` / `createDevice` / `CreateDevice`,把返回的 `setup_command` 给用户复制 |
| 看电脑在不在线 | `list_devices` / `listDevices` / `ListDevices` |
| 开关某台电脑上的 harness | `set_device_harness` / `setDeviceHarness` / `SetDeviceHarness` |
| 建一个 agent(起名字，绑定运行时：某台电脑上的某个 harness,以后可以切换) | `create_agent` / `createAgent` / `CreateAgent`;修改配置用 `publish_agent_version` / `publishAgentVersion` / `PublishAgentVersion`，提交完整配置与 `base_version_id` |
| 用某个 agent 发起一次任务 | `create_conversation` / `createConversation` / `CreateConversation` |
| 接着回复 | `send_message` / `sendMessage` / `SendMessage`；由原生适配器决定插话或排队 |
| 看进度和结果 | `get_conversation` / `getConversation` / `GetConversation`,或 `wait_conversation` 轮询到 `idle` |

另外还有 `me`、`list_agents`、`get_agent`、`delete_agent`、`list_conversations`、`delete_conversation`、`list_harnesses`、`reset_device`、`delete_device`,以及 admin 用的 `list_users`、`create_user`、`reset_user_token`,和接口一一对应。

非 2xx 响应统一抛(Go 里返回)一个带 `status` 和服务端 `error` 说明的错误。

## 会话附件

设计见 [conversation 文件](../designs/v3/conversation-files.md)，HTTP 约定见 [attachments](../docs/server/conversations.md)。三种 SDK 同步增加 `Attachment` 类型和以下方法，仍按第一个用户参数发送 `X-Agentuse-User`：

| 操作 | Python / Node / Go |
|---|---|
| 上传一个文件流、指定文件名 | `upload_attachment` / `uploadAttachment` / `UploadAttachment` |
| 分页列出 conversation 附件，接收 limit/cursor | `list_attachments` / `listAttachments` / `ListAttachments` |
| 取附件元数据 | `get_attachment` / `getAttachment` / `GetAttachment` |
| 下载附件，返回可关闭的响应流 | `download_attachment` / `downloadAttachment` / `DownloadAttachment` |
| 删除附件 | `delete_attachment` / `deleteAttachment` / `DeleteAttachment` |

以上方法均显式传 conversation_id；get/download/delete 再传 attachment_id。列表返回 `{attachments, next_cursor}`，upload/get 返回 Attachment，delete 不返回内容。Go upload 接收 `io.Reader` 和 filename、download 返回 `io.ReadCloser`（另带响应头）；Python 接收二进制文件对象、返回可关闭的二进制响应；Node 接收文件流、返回含 headers 的流响应，调用方负责消费或关闭。传输取消与错误处理沿用各 SDK 约定。upload 必须流式生成 multipart，不能先缓冲整个文件或转 base64；download 不自动落盘。

创建请求省略 prompt 即创建空 conversation，返回中的 message 可缺省；调用者先上传输入附件，再发送消息选择 attachment_ids。创建时 mode 为 single（默认）或 collaborative；不提供 defer_start 或原生 input 包装字段。

## 多 agent 共享 conversation

所有 conversation 使用同一个共享消息模型，mode 只在创建时选择单体执行或协作拆分。发送消息用 to_agent_ids 和 to_user_ids 指定 Agent 与用户收件人，返回 message、runs、deliveries；先建立输入分配，实际 run 由 client accepted 决定。只有 Agent 收件人会触发执行；用户必须已有会话读取权限。两组省略时均返回 []，只保存笔记，不默认调用上次 agent。

三个 SDK 增加 get/cancel/wait run、wait message runs、reply run request 方法。等待一条消息完成要跟踪 deliveries（accepted 前 run_id 为空），不能只等最初返回的新 runs。WaitConversation 对新模式默认等全部执行结束；returnOnWaiting / return_on_waiting / Go WaitOptions 可选择在提问时返回。

Agent 和共享 skill 独立版本化：创建 agent 会发布 v1，后续配置修改调用版本发布 API，旧版本不可变。先上传根目录含 SKILL.md 的 ZIP，再在 agent 配置的 skills 中固定 skill_id 与 skill_version_id；skill 发布新版本不会自动升级 agent。各 SDK 均提供版本查询、skill 上传和固定版本下载方法。完整规则见 [agentService](../designs/v3/server-agent.md) 和 [skillService](../designs/v3/server-skill.md)。

Message 使用 author_role=user/agent/child_conversation 和纯字符串 content。用户与 Agent 分别填写 author_user_id / author_agent_id；子会话通知填写 author_conversation_id / author_round_seq，不带 Agent 作者。reply_to_message_id 引用同一 conversation 的消息，执行结果在 Run 中。session_id 只由 Client 生成，Server 记录同一个值；不存在 client_session_id。

协作根 Agent 使用 context conversation 创建 single 子对话。每个子会话整轮结束后，Server 自动向创建者 parent_agent_id 发送 child_conversation 通知，包含 author_conversation_id 与 author_round_seq；各 Agent 结果保留在子对话。SDK 提供 ListChildren/listChildren/list_children、GetConversationRound/getConversationRound/get_conversation_round 和显式删除子树方法。创建/发送 Agent 输入会返回 round；详情含 rounds。设备工具使用实际执行 Source/source，不增加工作令牌。详见 [Conversation](../docs/server/conversations.md#模式与嵌套)。
