# agents

用戶自己起名字的 agent。名字是給人看、給人理解的(如「寫代碼助手」「週報小祕書」)。

一個 agent 綁定一個**運行時**:某臺 [daemon](daemons.md) 加上它上面的某個 [harness](harnesses.md),也就是 [daemon_harnesses](daemon_harnesses.md) 裏的一行。運行時可以隨時切換，切換時 daemon 和 harness 一起換(比如從「筆記本上的 Claude Code」換到「臺式機上的 Codex」),agent 本身不變。

消息通過 to_agent_ids @ 一個或多個 agent。每個 run 保存當時的 daemon/harness 快照；切換運行時影響後續 run，歷史執行保留。刪除 agent 後共享 session 和歷史署名仍保留。

## 多 agent 會話方案

[session-mentions](../designs/session-mentions.md) 由每條消息的 @ 目標直接選擇 agent，不建立 session-agent 參與關係。agent 仍配置自身運行時，每次 run 保存當時的運行時快照；所有 agent 執行時讀取同一個 session 的消息和附件。
