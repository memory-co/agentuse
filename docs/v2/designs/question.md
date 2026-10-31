# 执行中的提问

harness 回报 question 事件，服务记录 run_questions 并将对应 run 设为 waiting。问题属于 run 和 attempt，多个 agent 可以同时在同一个 session 提问。

用户经 `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` 提交 answers，服务将答案送到对应执行；收到 answered 事件后恢复 running。过期问题不能再次回答。

执行被回收时，尚未送达的答案进入共享消息，下一次执行可读取；旧 attempt 的提问与答案不能影响新执行。

见 [Sessions API](../apis/sessions.md) 与 [共享 session](session-mentions.md)。
