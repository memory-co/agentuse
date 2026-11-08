# Conversation 接口与命名

Server 使用 conversation 表示共享对话：保存消息、消息收件人、附件以及关联的 run。Client 的 session 表示本地 harness 执行会话；一个 conversation 可以涉及多个 Client、session 和 run。

Server 对话接口统一使用 `/api/conversations`。旧的 `/api/sessions`、`/api/session` 不注册、不跳转，也不提供兼容别名。运行中 Server 的 `/api/docs` 和 `/api/openapi.json` 提供完整接口定义。

## HTTP 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET / POST | `/api/conversations` | 列表 / 创建对话 |
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

Work 的线程是 conversation。`/api/works/{work_id}/threads` 是工作范围内的操作，返回同一套 conversation 模型。

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
| 工作线程 | `ListWorkConversations` | `listWorkConversations` | `list_work_conversations` |

Go 类型为 `Conversation`、`ConversationDetail`、`ConversationEvent`、`CreateConversationRequest` 和 `CreateConversationResponse`，创建和详情通过 `.Conversation` 读取对话。Node/Python JSON 使用 `.conversation` / `["conversation"]`。旧方法、类型和响应字段不保留别名。

## 存储与更新

当前数据库 schema 7 使用 `conversations` 表；消息、附件、投递、run、事件、请求和控制记录使用 `conversation_id`。原生执行归属继续使用 `session_id`。

创建对话省略 prompt 即创建空对话。不接受 mode、input、defer_start 或旧消息字段；向原生 harness 投递的输入格式由 Client 负责。

不迁移或读取旧数据库；更新时重建 Server 数据库，并一起更新 Client、前端和 SDK。附件总量配置使用 `--attachment-conversation-max-bytes` / `AGENTUSE_ATTACHMENT_CONVERSATION_MAX_BYTES`。设计约束见 [Server conversation 设计](../../designs/v3/designs/server-conversation.md)。
