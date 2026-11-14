# 设计资料

本目录由原 `docs/` 整体迁入，保留原有版本结构、设计讨论、协议、数据表草案、历史接口说明及插图。v3 描述当前实现及明确标记的 TODO；v1/v2 保留历史设计和接口，不作为当前使用说明。

当前使用手册位于 [docs](../docs/README.md)，命令和参数以其中的 [Server CLI](../docs/server/cli.md)、[Client CLI](../docs/client/cli.md) 为准。

v1/v2 是历史存档，其中旧接口和类型不代表当前支持的入口。Server 当前仅使用 [Conversation 接口](../docs/server/conversations.md)，旧 session 路由和兼容别名已删除。

嵌套 conversation 已实现，主、子会话各自支持多 Agent。子会话整轮结束后由 Server 生成 child_conversation 通知，协调者按来源会话与轮次读取结果。关系、轮次和表结构统一见 [conversation 设计](v3/server-conversation.md#父子對話)；[嵌套对话设计](v3/server-conversation-nest.md)说明 context CLI、派发和按轮次通知的闭环。

| 入口 | 内容 |
|---|---|
| [Server 设计](v3/server-main.md) | 服务端业务划分及各 service 的设计 |
| [Client 设计](v3/client-main.md) | 本机配置、endpoint、session、run 和 harness 的设计 |
| [嵌套对话设计](v3/server-conversation-nest.md) | context CLI、子对话派发、按轮次通知及澄清后续轮次；表结构规范由 conversation 稿维护 |
| [Conversation 文件树](v3/conversation-files.md) | 独立路径空间、消息附件快照与目录操作 TODO |
| [Client 执行上下文](v3/client-context.md) | AI 的 context 工具、run 归属、附件快照和人类入口 |
| [Client ↔ Server 协议](v3/protocol.md) | `agentuse-client/2` 报文、受理、控制及恢复规则 |
| [v2 资料](v2/designs/main.md) | 早期架构、接口、表结构和 CLI 说明 |
| [v1 资料](v1/README.md) | 第一版设计与接口说明 |

`images/` 保存这些资料使用的插图。新设计继续放在本目录；已实现功能的操作方法写入 `docs/server/` 或 `docs/client/`。
