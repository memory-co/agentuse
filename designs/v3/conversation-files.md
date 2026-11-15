# Conversation 文件树与消息附件

每个 conversation 是一个独立文件命名空间。attachments.relative_path 保存规范化的相对文件路径，完整规范化路径的 SHA-256 保存在 path_hash，(conversation_id, path_hash) 唯一；实际字节通过 fileService 保存在 conversations/<conversation_id>/<relative_path>，与通用 conversation 文件共用本地或 S3 后端。附件 ID 只标识消息引用的业务元数据。文件树从路径派生，不读取或暴露 Server/Client 的主机目录。

## 路径

- 用户上传进入保留目录 user_input/，支持目录层级。
- AI 通过 context 上传时只接受 workspace 内的普通文件，并保留相对路径。附件 HTTP API 省略 path 时使用上传文件名，不提供 Client 本机外部路径读取能力。
- AI 不能写入 user_input/。
- 路径不接受绝对地址、反斜线、空目录段、`.`、`..` 或控制字符。
- 同路径上传在同一发布事务内分配编号路径，不覆盖旧内容；文件与目录不能占用同一位置。
- conversation 之间不共享路径或目录状态。

## 消息关联

HTTP message 请求显式携带 attachment_ids；省略表示空，不继承整个 conversation 的文件。Server 在提交消息的同一事务内校验归属，并把附件元数据快照存入 messages.attachments。

Server 首次投递该消息时直接使用已保存快照。随后上传到文件树但未被消息选择的文件不会出现在 Client 输入中。多个 Agent 接收同一消息时使用同一份附件集合；多个输入归属同一 run 时，每条消息保留自己的附件快照；AI 通过 conversation 文件工具按路径读取，Client 不另存合并后的附件清单。

最终 result 事务在写入 AI 回复时，关联该 run 已上传的输出文件。不额外创建 task 或文件交付实体。输入和输出的卡片都可以打开文件管理器弹层，自动定位并预览该文件。

已被消息引用的附件不单独删除，避免历史引用和待执行输入失效；未引用的上传可以删除。删除整个 conversation 沿用既有文件清理流程。

## 页面

文件管理器通过“文件”按钮按需打开居中弹层，不使用对话侧栏。桌面布局为顶部路径栏、左侧文件树、右侧内容预览，树和预览独立滚动。小屏幕在同一弹层内切换文件列表与预览。输入框提供上传按钮、待发送文件卡片和上传进度，成功发送后清空选择；失败时保留已上传文件的 ID，重试不重复上传。

新会话和后续消息的输入框均支持直接粘贴剪贴板图片，图片进入相同的附件草稿，显示缩略图并允许移除；发送时才上传到 user_input/ 并关联本条消息。支持多张图片及仅图片消息，沿用每条消息最多 64 个附件的限制。普通文字粘贴保持浏览器原有行为，发送期间不接收新图片。

路径栏支持返回根目录或任一祖先目录；文件树按相对路径显示目录，支持搜索、图片/文本预览、下载及添加到消息。选择消息附件时自动展开父目录并定位文件；添加到消息后关闭弹层并将焦点返回输入框，关闭或 Escape 恢复到打开入口。右侧会话栏专门显示协作模式的子会话标题及状态，小屏幕由“子会话”按钮打开列表。附件元数据请求和预览沿用用户/管理员代为身份，使用同一个权限范围。

## 存储与版本

遷移基線 `0001_initial` 使用 conversations 表和 conversation_id；attachments 保存 relative_path，messages 保存 attachments JSON 快照。附件 API 和内容地址均位于 /api/conversations 下。无旧数据迁移、旧字段别名或旧路由回退。

## attachments 表

下列 SQL 是 SQLite 表达的逻辑结构；GORM 在 MySQL/PostgreSQL 中映射相应字段类型。path_hash 避免 MySQL 索引长度限制，真实路径保持最长 4096 字节且不截短。

附件元数据属于 conversationService；只在本稿定义，messages 与 conversation 的 SQL 见 [conversation 设计](server-conversation.md#表結構)。created_by_run_id 记录设备产物的执行来源，created_by_agent_id 由 run 关联读取，是 DTO 投影，不重复存入表。

```sql
CREATE TABLE attachments (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    path_hash TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
    sha256 TEXT NOT NULL CHECK(length(sha256)=64),
    created_by TEXT NOT NULL,
    source TEXT NOT NULL CHECK(source IN ('user','device')),
    device_id TEXT,
    created_by_run_id TEXT,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX attachments_path ON attachments(conversation_id,path_hash);
CREATE INDEX attachments_conversation ON attachments(conversation_id,created_at,id);
```

## TODO

- [ ] 创建空目录，并定义独立目录元数据。
- [ ] 移动文件与目录，保持历史消息引用稳定。
- [ ] 重命名文件与目录，定义同名冲突处理。
- [ ] 目录上传与拖放，保留用户文件的目录层级。
- [ ] 目录级权限与批量操作的交互设计。
