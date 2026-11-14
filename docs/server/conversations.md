# Conversation 接口与命名

Server 使用 conversation 表示共享对话：保存消息、消息收件人、附件以及关联的 run。Client 的 session 表示本地 harness 执行会话；一个 conversation 可以涉及多个 Client、session 和 run。

Server 对话接口统一使用 `/api/conversations`。旧的 `/api/sessions`、`/api/session` 不注册、不跳转，也不提供兼容别名。运行中 Server 的 `/api/docs` 和 `/api/openapi.json` 提供完整接口定义。

## HTTP 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET / POST | `/api/conversations` | 顶层列表 / 创建对话 |
| GET | `/api/conversations?parent_conversation_id=P` | 直接子对话列表 |
| POST | `/api/conversations/{id}/parent/messages` | 子 Agent 向创建者反馈 |
| GET / DELETE | `/api/conversations/{id}` | 读取完整快照 / 删除对话 |
| POST | `/api/conversations/{id}/messages` | 发送消息，明确指定收件人与附件 |
| POST | `/api/conversations/{id}/resolve`、`reopen` | 标记完成 / 重新打开 |
| GET | `/api/conversations/{id}/runs/{run_id}` | 读取 run 及事件 |
| POST | `/api/conversations/{id}/runs/{run_id}/cancel` | 取消 run |
| POST | `/api/conversations/{id}/runs/{run_id}/requests/{request_id}/reply` | 回答原生执行请求 |
| GET | `/api/conversations/{id}/messages/{message_id}/agents/{agent_id}/context` | Client 补齐共享历史；查询参数 `session_id` 指定实际本地 session |
| GET / POST | `/api/conversations/{id}/attachments` | 列出 / 上传附件 |
| GET / DELETE | `/api/conversations/{id}/attachments/{attachment_id}` | 附件元数据 / 删除未引用附件 |
| GET | `/api/conversations/{id}/attachments/{attachment_id}/content` | 下载附件内容 |
| GET / PUT | `/api/conversations/{id}/files/{path...}` | 按路径读写通用 conversation 文件 |

## 模式与嵌套

创建时可指定 `mode: "single" | "collaborative"`，省略默认 single，创建后不可修改。single 在当前对话中完成任务；collaborative 的 Agent 负责拆分、协调和汇总，在本机 `context conversation` 命令中创建子对话。两种模式都支持多个 Agent。

子对话必须为 single，最多两层。`parent_conversation_id` 指向协作父对话，`parent_agent_id` 由 Server 从已验证的创建 Agent 来源自动填写，不能在请求中指定。它独立于消息收件人及实际 run。人类创建顶层对话，不能手工指定父对话代建子对话。

```json
{"mode":"collaborative","title":"改进搜索","prompt":"请分工实现并汇总","to_agent_ids":["planner"]}
```

查询 `GET /api/conversations?parent_conversation_id=P` 列出直接子对话。父子关系、mode、标题和状态均位于普通 `conversation` 对象中。前端左侧导航可展开子会话，协作会话右侧独立显示子会话列表；子页面可返回父会话。“文件”按钮或消息附件打开文件管理器弹层，各会话文件树互相独立。删除有子会话的根节点必须显式 `DELETE /api/conversations/P?recursive=true`，所有节点均不能有活跃执行或待派发输入。

子会话的显式反馈使用 `POST /api/conversations/C/parent/messages`，JSON 为 `{"prompt":"需要澄清的问题"}`；最终 Agent 回复自动回传。源消息先保存，父消息按 `to_agent_ids=[parent_agent_id]` 普通入队；父 run 已结束仍可继续处理。返回 `delivery: pending/delivered/failed`，delivered 仅表示父消息已保存。

源 Message 的 `parent_delivery`、`parent_message_id`、`parent_delivery_error` 是只读投影。父消息的 `source_conversation_id/source_message_id` 用于去重和来源导航；附件留在源对话，不能跨对话复用附件 ID。临时故障会补发，重放不重复落消息。

设备协作请求使用设备密钥，并携带 `X-Agentuse-Conversation`、`X-Agentuse-Agent`、`X-Agentuse-Session`、`X-Agentuse-Run`。Server 校验已上报的真实执行来源；协作根 Agent 只可管理自己的直接子对话，子 Agent 只能读自身及向父反馈。文件读取额外允许显式访问直接相关的父/子文件树。命令自动绑定这些来源，AI 不需手填身份或凭据。

## 字段

创建结果使用 `conversation`，读取详情同样使用 `conversation`：

```json
{
  "conversation": { "id": "conversation-uuid", "status": "idle" },
  "messages": [],
  "runs": [],
  "deliveries": [],
  "questions": [],
  "events": []
}
```

Message、Attachment、Run、Delivery、Question 和 ConversationEvent 用 `conversation_id` 表示 Server 对话归属。`session_id` 只有一个，由 Client 生成；Run、Delivery 和 ConversationEvent 记录这个相同的值以关联本地执行，Client 上下文和 WebSocket 也使用同名字段。Server 不生成自己的 session，也不提供 `client_session_id` 别名。

Message 的 author_role 为 user/agent，content 是原始正文字符串；author_user_id 与 author_agent_id 按作者类型填写，Agent 同时记录固定的 author_agent_version_id。author_name 由用户或该 Agent 版本关联读取，消息不包含执行结果包装或 run_id。

发送消息可用 reply_to_message_id 回复同一 conversation 中的消息。消息通过 `to_agent_ids`、`to_user_ids` 指定收件人，`attachment_ids` 指定本条消息的文件。只有 Agent 收件人触发执行；实际 run UUID 由 Client 的 accepted 事件返回。附件操作见 [对话文件](conversation-files.md)。

## SDK

| 操作 | Go | Node.js | Python |
|---|---|---|---|
| 列表 | `ListConversations` | `listConversations` | `list_conversations` |
| 创建 | `CreateConversation` | `createConversation` | `create_conversation` |
| 详情 | `GetConversation` | `getConversation` | `get_conversation` |
| 删除 | `DeleteConversation` | `deleteConversation` | `delete_conversation` |
| 完成 | `ResolveConversation` | `resolveConversation` | `resolve_conversation` |
| 重开 | `ReopenConversation` | `reopenConversation` | `reopen_conversation` |
| 等待 | `WaitConversation` | `waitConversation` | `wait_conversation` |
| 直接子对话 | `ListChildren` | `listChildren` | `list_children` |
| 向父反馈 | `SendParentMessage` | `sendParentMessage` | `send_parent_message` |
| 删除子树 | `DeleteConversationTree` | `deleteConversationTree` | `delete_conversation_tree` |

Go 类型为 `Conversation`、`ConversationDetail`、`ConversationEvent`、`CreateConversationRequest` 和 `CreateConversationResponse`，创建和详情通过 `.Conversation` 读取对话。Node/Python JSON 使用 `.conversation` / `["conversation"]`。旧方法、类型和响应字段不保留别名。

## 存储与更新

当前数据库 schema 8 使用 `conversations` 表；消息、附件、投递、run、事件、请求和控制记录使用 `conversation_id`。原生执行归属继续使用 `session_id`。

创建对话省略 prompt 即创建空对话。mode 只接受 single/collaborative；不接受 input、defer_start 或旧消息字段；向原生 harness 投递的输入格式由 Client 负责。

不迁移或读取旧数据库；更新时重建 Server 数据库，并一起更新 Client、前端和 SDK。附件总量配置使用 `--attachment-conversation-max-bytes` / `AGENTUSE_ATTACHMENT_CONVERSATION_MAX_BYTES`。设计约束见 [Server conversation 设计](../../designs/v3/server-conversation.md)。
