# session_runs

多 agent 会话中的一次逻辑执行。session 保存共享消息和附件；run 记录本次执行者、运行时、租约和结果，不保存按 agent 的上下文或原生续接。设计见 [session-mentions](../designs/session-mentions.md)。

| 字段 | 含义 |
|---|---|
| id | sr_ 开头的执行 id |
| session_id / message_id | 所属 session 和主任务消息 |
| agent_id | 可空外键，agent 删除后置空 |
| agent_key / agent_name / agent_user_id | 当时的 agent 身份快照和主人 |
| daemon_id / harness | 创建本次执行时的运行时快照，daemon 删除后 id 为空 |
| status | pending / running / waiting / succeeded / failed / cancelled |
| attempt_id / lease_until | 当前执行代次和租约；每次领取新建 ra_ 代次 |
| context_seq | 本代次开始读取的 session 消息水位，仅用于排查 |
| result_message_id / error | 最终 assistant 消息和失败说明 |
| created_at / updated_at | unix 秒 |

部分唯一索引保证同一 `(session_id, agent_id)` 最多一条 pending/running/waiting 记录。一次消息写入及其所有目标路由在同一 SQLite 写事务中完成。

相关表：

- message_deliveries：每条消息、每个 agent 一个目标投递；关联 run_id，kind 为 new_run/steer，status 为 pending/delivered 或最终 succeeded/failed/cancelled。未送达插话可改挂后续 run，不复制 user message。prepared_attempt 表示已经进入某代次初始输入的任务，启动确认后不再重复推送。
- message_formats：记输入是否为文本 prompt，任意 JSON input 不强行拼接为插话。
- message_authors：assistant 消息的 run 和 agent 身份快照。
- run_events：带 run/attempt/event_seq 的事件，唯一约束去重。
- run_questions：带 run/attempt/tool_use_id 的问题、答案和状态，避免同一消息的多个 agent 冲突。
- session_requests：按 session/user/Idempotency-Key 保存规范化请求摘要和 message id，保证重试不重复派活。
- work_run_tokens：工作协调 run/attempt 的短期权限；终态删除，过期代次失效。
- run_notifications：按线程消息去重的工作汇总通知。

seq 当前复用 messages.id，严格递增但不保证连续。不新增 agent 参与关系或私有上下文表。sessions 没有运行时或续接字段。
