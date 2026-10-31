# 插话

插话通过 `POST /api/sessions/{id}/messages` 发送，消息包含 prompt 与 target_agent_ids。

每个目标独立路由：没有活动执行时创建 run；已有 pending/running/waiting run 时记录 steer delivery。尚未启动的执行将文本补充合入初始输入，运行中的执行通过内部 steer 推送。

harness 接受插话后回报 steered。没有被接收的补充在当前执行结束后挂到后续 run，复用原用户消息，不复制消息。取消执行时关联投递也取消。

所有 agent 读取同一 session 的共享历史。详见 [共享 session](session-mentions.md)。
