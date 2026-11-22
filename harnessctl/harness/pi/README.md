# Pi 适配器

AgentUse 设备执行器名称为 `pi`。在运行 Client 的 Linux 设备上安装 Pi，使 `pi` 位于 Client 进程的 PATH 中，并配置好模型和认证。适配器使用 Python 3.9+ 标准库，Pi 自身需要符合其版本要求的 Node.js。

```sh
npm install -g @earendil-works/pi-coding-agent
pi --version
```

需要 Pi 1.6.0 或更新的正式版本。适配器通过 Pi 的 JSONL RPC 发送消息，收到 `agent_settled` 后才完成 run，以覆盖 Pi 自动重试、压缩和后续执行。使用 `--model provider/model` 可指定模型。执行结果使用 Pi 实际报告的模型 ID 和 token 用量。

```sh
./harnessctl init /path/to/events.jsonl --cwd /path/to/project --model provider/model
./harnessctl send /path/to/events.jsonl '检查当前项目'
./harnessctl status /path/to/events.jsonl
```

- 忙时发送的消息按顺序创建后续 run。这些 run 共享同一个 Pi 会话。
- Pi 扩展发起的确认对话框通过 `reply` 回复 `allow` 或 `deny`。选择和文本输入通过 `reply` 回复 JSON 对象，例如 `{"value":"所选选项"}`。答复属于原来等待的 run。
- 附加指令和技能通过 Pi 的 `--append-system-prompt` 和 `--skill` 使用初始化时的私有快照。
- Pi 会话文件保存在事件文件旁边的 `.d/pi-sessions/` 内。保留事件文件和整个 `.d/` 目录，后台进程重建后才能继续原会话。

[harnessctl 协议](../../README.md) · [Pi RPC 文档](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md)
