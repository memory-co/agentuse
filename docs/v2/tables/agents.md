# agents

用户自己起名字的 agent。名字是给人看、给人理解的(如「写代码助手」「周报小秘书」)。

一个 agent 绑定一个**运行时**:某台 [daemon](daemons.md) 加上它上面的某个 [harness](harnesses.md),也就是 [daemon_harnesses](daemon_harnesses.md) 里的一行。运行时可以随时切换，切换时 daemon 和 harness 一起换(比如从「笔记本上的 Claude Code」换到「台式机上的 Codex」),agent 本身不变。

消息通过 target_agent_ids @ 一个或多个 agent。每个 run 保存当时的 daemon/harness 快照；切换运行时影响后续 run，历史执行保留。删除 agent 后共享 session 和历史署名仍保留。

## 多 agent 会话方案

[session-mentions](../designs/session-mentions.md) 由每条消息的 @ 目标直接选择 agent，不建立 session-agent 参与关系。agent 仍配置自身运行时，每次 run 保存当时的运行时快照；所有 agent 执行时读取同一个 session 的消息和附件。
