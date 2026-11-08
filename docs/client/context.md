# 执行上下文：context

Client 为每个执行环境提供 `context` 命令。AI 用它读取当前 run 的输入、补充消息和输入附件；人类用 `agentuse context` 选择同一份上下文，也可以进入绑定该 run 的 shell。

它与远端会话管理用途不同：发送消息、等待执行、取消等操作使用 `agentuse conversation`，上传交付文件使用 `agentuse attachment`。AI 的默认工具说明只介绍当前上下文需要的能力。

## AI 读取当前输入

Client 初始化 session 时注入工具说明，将 `context` 加入 harness 的 `PATH`。执行中直接调用：

```bash
context input
```

输出包含：

| 字段 | 含义 |
|---|---|
| `endpoint_id` | 输入来源；本地调试为 `local` |
| `conversation_id` | Server conversation 或本地 conversation 的 ID |
| `session_id` | Client 本机 session ID |
| `run_id` | 适配器 accepted 事件公布的实际 run UUID |
| `agent_id` | 当前 Agent |
| `status` | `running`、`waiting` 或实际 result 的终态 |
| `workspace` | 该 conversation 共享工作目录的绝对路径 |
| `inputs` | 归属该 run 的已受理输入，按 accepted 顺序排列 |

每条输入包含 `input_id`、`text`、`attachment_ids`；远端输入另有 `message_id`，有业务上下文时包含 `context`。本地输入没有 Server message ID。命令不会返回设备密钥或工作令牌。

同一个 run 可以受理多条输入。需要读取执行期间的补充消息时，再调用一次 `context input`。已经排队、尚未开始的其他 run 的输入不会混入其中。默认由适配器公开的 status 判断当前 run；没有活跃 run 时会报错，不会自动选择最近结束的一次执行。

## AI 读取图片或其他文件

先列出当前输入允许读取的附件：

```bash
context attachments
```

输出形如：

```json
{
  "attachments": [
    {
      "id": "at_示例",
      "name": "截图.png",
      "mime_type": "image/png",
      "size_bytes": 12345,
      "sha256": "文件内容的SHA256"
    }
  ]
}
```

取得文件路径：

```bash
context attachment at_示例
```

输出相同的元数据，并增加 `local_path`，例如 `/绝对路径/workspace/.agentuse/attachments/at_示例/截图.png`。Client 校验大小和 SHA-256；缓存缺失或内容损坏时，从当前注册的 Server 下载并重新校验。

对于图片，把 `local_path` 传给模型可用的原生看图工具，例如 Codex 的 `view_image` 或 Claude Code 的 `Read`。命令返回的 JSON 是文件信息，原生看图工具才会把图片内容读给模型。文本、PDF 等文件按当前模型已有的文件工具读取。

附件范围来自投递给该 run 的各条输入的固定快照，可能包含投递时会话已有的多个文件。同一 run 新受理的补充输入可以增加可读附件；其他排队 run 收到的新附件不会提前出现在当前工具视图中。`context attachments` 不重新列举整个 Server 会话；传入不属于当前输入快照的文件 ID 会报错。

## 人类选择或进入上下文

Client 为本机 session 生成描述文件，位置为：

```text
<session.work_root>/<endpoint_id>/<conversation_id>/sessions/<session_id>/context.json
```

默认工作根目录是 `~/.agentuse/tmp`。可以在本机页面找到对应 endpoint、conversation、session 和 run，再选择其描述文件：

```bash
agentuse context --path /绝对路径/context.json --run RUN_UUID input
agentuse context --path /绝对路径/context.json --run RUN_UUID attachments
agentuse context --path /绝对路径/context.json --run RUN_UUID attachment FILE_ID
```

`--run` 必须是该 session 实际受理过的 run UUID。文件仍在时，可以查看已结束的历史 run；没有指定 run 时，命令询问适配器当前正在执行哪个 run。`--path` 也可由环境变量 `AGENTUSE_CONTEXT` 提供。

进入该 run 的 shell：

```bash
agentuse context enter --path /绝对路径/context.json --run RUN_UUID
# shell 的工作目录已切到 conversation workspace，context 固定读取刚选中的 run。
context input
context attachments
exit
```

`enter` 使用 `$SHELL`，未设置时使用 `/bin/sh`。shell 通过 `AGENTUSE_CONTEXT_RUN` 固定所选 run，后续其他执行开始也不会改变选择。裸 `context` 不提供选择其他 Server、session、run 或管理 Client 的参数；需要切换时由人类重新运行 `agentuse context`。

这些文件随 conversation 记录一起受 quota 回收；描述文件和历史输入不保证永久保存。

## Server 业务工具

Work 的协调、跨设备派发和工作授权由 Server 负责。协调 Agent 按 Server 随消息提供的说明获取并调用独立工具；Client 和 `context` 不判断 Work 角色，也不签发或管理工作令牌。使用方法见 [Server Work 手册](../server/work.md)。

`context` 命令成功时退出码为 `0`，执行或存储错误为 `1`，参数错误为 `2`。更完整的管理命令与环境变量见 [Client CLI](cli.md)。
