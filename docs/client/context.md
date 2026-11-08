# 执行上下文：context

Client 为每个执行环境提供 `context`。AI 用它读取当前 run 的输入、补充消息和附件，并把输出文件交给 Server；人类用 `agentuse context` 调试同一份上下文，也可以进入绑定该 run 的 shell。

文件交互采用两个方向：

| 方向 | 上传 | 读取 |
|---|---|---|
| 输入文件 | 人类在 Server 页面或通过 API/SDK 上传 | Client AI 通过 context 读取 |
| 输出文件 | Client AI 通过 context 上传 | 人类在 Server 页面查看、下载 |

## 当前输入

Client 初始化 session 时注入工具说明，将 `context` 加入 harness 的 `PATH`。直接调用：

```bash
context input
```

| 字段 | 含义 |
|---|---|
| `endpoint_id` | 输入来源；本地调试为 `local` |
| `conversation_id` | Server 或本地 conversation ID |
| `session_id` | Client 本机 session ID |
| `run_id` | 适配器 accepted 事件公布的实际 run UUID |
| `agent_id` | 当前 Agent |
| `status` | `running`、`waiting` 或实际 result 的终态 |
| `workspace` | conversation 共享工作目录的绝对路径 |
| `inputs` | 归属该 run 的已受理输入，按 accepted 顺序排列 |

每条输入包含 `input_id`、`text`、`attachment_ids`；远端输入另有 `message_id`，有业务上下文时包含 `context`。命令不返回设备密钥或工作令牌。

同一个 run 可以受理多条输入。需要读取补充消息时，再调用一次 `context input`。默认由适配器公开的 status 判断当前 run；排队 run 的输入不会混入当前结果。

## 列出文件

```bash
context attachment list
```

结果为 `{"attachments": [...]}`，合并当前输入快照和本次 run 已上传的输出。附件包含：

| 字段 | 含义 |
|---|---|
| `id`、`name` | 文件 ID 和名称 |
| `direction` | `input` 为当前输入文件，`output` 为本次 run 的输出 |
| `conversation_id` | 当前 conversation |
| `run_id` | 输出文件所属的 run；输入文件不填此字段 |
| `mime_type`、`size_bytes`、`sha256` | 文件类型、大小、SHA-256 |
| `download_path` | Server 上可供用户下载的路径 |

输入范围来自投递给该 run 的各条已受理消息的固定快照，可能包含投递时会话已有的多个文件；补充输入可以扩展范围。输出按 Server 的 run 归属查询，其他排队 run 的输入或输出不会自动混入列表。

## 读取图片或其他文件

```bash
context attachment read FILE_ID
```

命令检查文件属于当前输入或本次 run 的输出，校验大小和 SHA-256 后返回元数据及 `local_path`，例如 `/绝对路径/workspace/.agentuse/attachments/at_示例/截图.png`。缓存缺失或损坏时，从当前注册的 Server 下载并重新校验。

图片用模型原生看图工具读取 `local_path`，例如 Codex 的 `view_image` 或 Claude Code 的 `Read`。文本、PDF 等文件使用模型已有的文件工具。命令返回的 JSON 本身只提供文件信息。

## 上传输出文件

生成文件后直接上传：

```bash
context attachment upload ./report.pdf
```

只接受普通文件，使用本地文件的 basename 作为名称。目标自动绑定当前 conversation/run，不需要填写 Server、token、conversation 或 run ID。结果为 JSON，例如：

```json
{
  "id": "at_示例",
  "name": "report.pdf",
  "direction": "output",
  "conversation_id": "当前conversation的UUID",
  "run_id": "当前run的UUID",
  "mime_type": "application/pdf",
  "size_bytes": 12345,
  "sha256": "文件内容的SHA256",
  "download_path": "/api/sessions/当前conversation的UUID/attachments/at_示例/content"
}
```

在最终回复中引用返回的 `download_path`，人类可在 Server 页面查看、下载该附件。上传只保存文件，不自动发消息或结束执行；AI 继续按原流程回复用户。

Server 在接收和发布文件时检查 run 的设备、conversation 和活动状态。已结束的 run 无法继续上传。连接中断时 Server 可能已经保存，先用 `list` 核对再决定是否重传；工具不会自动重复上传。输出列表以 Server 记录为准，不依赖本机另存一份上传清单。

## 人类调试上下文

Client 为本机 session 生成描述文件：

```text
<session.work_root>/<endpoint_id>/<conversation_id>/sessions/<session_id>/context.json
```

默认工作根目录是 `~/.agentuse/tmp`。在本机页面找到 endpoint、conversation、session 和 run 后，可显式查看：

```bash
agentuse context --path /绝对路径/context.json --run RUN_UUID input
agentuse context --path /绝对路径/context.json --run RUN_UUID attachment list
agentuse context --path /绝对路径/context.json --run RUN_UUID attachment read FILE_ID
agentuse context enter --path /绝对路径/context.json --run RUN_UUID
# 进入后：
context input
context attachment list
exit
```

`--path` 默认读取 `AGENTUSE_CONTEXT`；省略 `--run` 时询问适配器当前执行。`enter` 使用 `$SHELL`，未设置时使用 `/bin/sh`，工作目录切换到 conversation workspace，并通过 `AGENTUSE_CONTEXT_RUN` 固定所选 run。裸 `context` 不提供切换 Server、session 或 run 的参数。

文件保留期间可以显式读取历史输入和输出；查询输出需要 Server 可用，已经缓存的输入可以按 ID 在本机读取。历史 run 的上传会被拒绝。上下文文件和缓存随 conversation quota 回收。本地调试支持输入读取，没有 Server 上传目标。

## Server 业务工具

Work 的协调、跨设备派发和工作授权由 Server 负责。协调 Agent 按 Server 随消息提供的说明获取独立工具；Client 和 context 不判断 Work 角色。操作其他线程的业务行为见 [Server Work 手册](../server/work.md)。

远端 conversation 管理使用 `agentuse conversation`。文件操作统一为 `context attachment list/read/upload`；移除顶层 `agentuse attachment`、旧 `context attachments` 和省略 read 的入口，不保留别名。

命令成功时退出码为 `0`，执行或存储错误为 `1`，参数错误为 `2`。其他管理命令见 [Client CLI](cli.md)。
