# 设计资料

本目录由原 `docs/` 整体迁入，保留原有版本结构、设计讨论、协议、数据表草案、历史接口说明及插图。设计稿可能包含尚未实现的目标结构，历史说明也可能与当前行为不同。

当前使用手册位于 [docs](../docs/README.md)，命令和参数以其中的 [Server CLI](../docs/server/cli.md)、[Client CLI](../docs/client/cli.md) 为准。

v1/v2 是历史存档，其中旧接口和类型不代表当前支持的入口。Server 当前仅使用 [Conversation 接口](../docs/server/conversations.md)，旧 session 路由和兼容别名已删除。

Work 已重新设计为主、子 conversation 的组织视图，支持各自的多 Agent 消息。该调整目前只更新设计稿，运行代码和使用手册仍保留旧实现；目标与删除清单见 [Work 设计](v3/server-work.md)。

| 入口 | 内容 |
|---|---|
| [Server 设计](v3/server-main.md) | 服务端业务划分及各 service 的设计 |
| [Client 设计](v3/client-main.md) | 本机配置、endpoint、session、run 和 harness 的设计 |
| [Work 设计](v3/server-work.md) | 待实现的父子 conversation 组织，复用多 Agent 消息和 conversation API |
| [Conversation 文件树](v3/conversation-files.md) | 独立路径空间、消息附件快照与目录操作 TODO |
| [Client 执行上下文](v3/client-context.md) | AI 的 context 工具、run 归属、附件快照和人类入口 |
| [Client ↔ Server 协议](v3/protocol.md) | `agentuse-client/2` 报文、受理、控制及恢复规则 |
| [v2 资料](v2/designs/main.md) | 早期架构、接口、表结构和 CLI 说明 |
| [v1 资料](v1/README.md) | 第一版设计与接口说明 |

`images/` 保存这些资料使用的插图。新设计继续放在本目录；已实现功能的操作方法写入 `docs/server/` 或 `docs/client/`。
