# agentuse v2 总体设计

用户电脑上的 daemon 主动通过 WebSocket 连接服务，领取执行、运行本地 harness 并汇报进度和结果。

1. Agent 是可命名的执行者，绑定可更换的 daemon + harness 运行时。
2. Session 属于创建者，保存所有 agent 共用的消息和附件，不绑定执行者。
3. 用户发送消息并用 target_agent_ids @ 一个或多个 agent；空闲目标创建 run，忙碌目标收到插话，无目标时保存笔记。
4. Run 固定本次执行的运行时快照，以 attempt 和租约控制执行权。daemon 读取共享历史后启动新的原生会话，完成时写入带署名的 assistant 消息。
5. 工作包含协调者 session、线程 session 和共用说明；协调者 agent 配置属于工作，执行期间使用作用域受限的工作令牌。

SQLite 保存用户、机器、agent、工作、session、消息、执行和附件元数据；文件内容走 HTTP。旧数据库须清空重建，服务不提供旧 session 模型兼容。

详细说明：[共享 session](session-mentions.md) · [附件](attachment.md) · [接口](../apis/README.md) · [session 表](../tables/sessions.md) · [run 表](../tables/session_runs.md)。
