# 插话

插话通过 `POST /api/sessions/{id}/messages` 发送，消息包含 prompt 与 target_agent_ids。

每个目标独立路由：没有活动执行时创建 run；已有 pending/running/waiting run 时记录 steer delivery。尚未启动的执行将文本补充合入初始输入，运行中的执行通过内部 steer 推送。

harness 接受插话后回报 steered。没有被接收的补充在当前执行结束后挂到后续 run，复用原用户消息，不复制消息。取消执行时关联投递也取消。

所有 agent 读取同一 session 的共享历史。详见 [共享 session](session-mentions.md)。

## 内置 harness

- Claude：通过 PreToolUse / Stop hook 回调注入。
- Codex：通过 app-server 的 turn/steer 注入当前活动 turn；不创建新的 turn。expectedTurnId 校验当前执行，成功响应后回报 steered。收到 RPC 拒绝时保留投递，不能提前标为已送达。
- 自定义且未实现实时输入的 harness：结束当前执行后处理未接收的消息。

新版 daemon hello 声明 codex-steer-v1；已安装的 daemon 必须更新二进制并重启才具备该实现，单独更新服务器无法改变远端进程。

验证：`go test -count=1 -run '^TestCodexSteer$' ./test/e2e/session` 走真实服务、daemon 和假 app-server；`AGENTUSE_REAL_CODEX=1 go test -count=1 -v -run '^TestRealCodexSteer$' -timeout 5m ./test/e2e/session` 使用本机已登录的 Codex。真实测试在 shell 等待期间发送随机标记，断言只有原 run、一条最终回复、对应的 steered 确认且回复采纳随机标记。
