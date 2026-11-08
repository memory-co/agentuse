# Work：跨 Client 的协调与执行

Work 由 Server 管理，将一个协调 conversation 和多个线程 conversation 组织在一起。协调 Agent 可以在 Client A 执行，线程中的 Agent 可以分别位于 Client B、C；Server 保存关系、校验权限、向目标设备派发消息，并把线程结果汇总回协调 conversation。

Client 接收 conversation message、准备 Agent、驱动 run 并上报事件。`context` 提供当前 run 的输入与文件读取。Work 业务工具由 Server 维护和授权。

## 人类管理

在管理页面创建 Work、选择协调 Agent、填写工作说明。管理线程时使用：

```bash
agentuse-server work agents
agentuse-server work threads --work WORK_ID
agentuse-server work new --work WORK_ID --agents coder "实现这个功能"
agentuse-server work send --work WORK_ID --agents reviewer THREAD_ID "评审结果"
agentuse-server work read --work WORK_ID THREAD_ID
agentuse-server work resolve --work WORK_ID THREAD_ID
```

命令调用 Server HTTP API，输出 JSON。用户使用自己的令牌，管理员可以管理其他用户的工作；地址和凭据的优先级、空线程和附件操作见 [Server CLI](cli.md#work工作与线程)。不需要把协调者的工作令牌交给人类使用。

## 协调 Agent 使用 Server 工具

Server 给协调消息附带工具使用说明。Agent 按说明下载工具到本轮执行的临时目录：

```bash
agentuse_work_tool_dir=$(mktemp -d)
curl --fail --silent --show-error \
  "$AGENTUSE_SERVER/api/agent-tools/work.py" \
  -o "$agentuse_work_tool_dir/work.py"

python3 "$agentuse_work_tool_dir/work.py" agents
python3 "$agentuse_work_tool_dir/work.py" new --agents coder "实现这个功能"
```

工具需要 Python 3.9+，由 Server 提供。执行设备无需安装 `agentuse-server`。Client 不安装 Work 子命令，也不分析协调者角色。

| 工具命令 | 作用 |
|---|---|
| `agents` | 列出工作所有者可用的 Agent，包括其他 Client 上的 Agent |
| `threads` | 列出本工作的线程 |
| `new --agents IDS [--users IDS] "消息"` | 创建线程并给指定 Agent 派发输入 |
| `new --defer-start` | 创建空线程，返回 `session.id` |
| `upload THREAD_ID FILE` | 在本工作范围内上传线程输入文件 |
| `send --agents IDS [--users IDS] THREAD_ID "消息"` | 向线程发送或补充输入 |
| `read THREAD_ID` | 读取线程消息和执行状态 |
| `resolve THREAD_ID` | 将空闲线程标记完成 |

结果为 JSON；`--agents`、`--users` 支持逗号分隔。空线程上传文件后再 `send` 指定执行 Agent。结果完成后，Server 会向协调 conversation 发回消息，协调者按消息继续处理。

## 来源与授权

工具通过通用 `context input` 取得当前 conversation、message、agent 和实际 run ID，然后使用执行设备身份向 Server 申请工作授权。设备身份由 Client 的通用运行环境提供，Client 不接收或处理 Work 令牌。

Server 确认消息确实投递给当前设备、conversation 属于该 Work 的协调对话、Agent 是当前协调者，且来源输入仍在处理中。实际 run 尚未上报时，使用既有消息投递关系授权；上报后核对 run ID。工作令牌只在工具与 Server API 之间使用，不输出到工具结果，也不写入 Client 环境。

普通 run、工作线程、其他设备和已结束的协调输入不能取得协调权限；已有授权在来源输入完成、失败、取消或被拒绝后失效。Server 对每次操作校验 Work 范围。下一轮协调执行重新读取当前身份并申请授权。

## API 与职责

| 接口 | 使用者 |
|---|---|
| `/api/works` 及 `/{id}` | 用户通过页面或 API 创建、查看、修改、删除 Work |
| `/api/works/{work_id}/threads` 及线程消息、读取、完成接口 | 人类管理 CLI，按用户权限校验指定工作 |
| `GET /api/agent-tools/work.py` | 获取 Server 工具；代码不包含凭据 |
| `POST /api/agent-tools/work/authorize` | 工具以执行设备及当前来源身份申请授权 |
| 现有 Agent、conversation、attachment API | 工具持授权操作，由 Server 限定工作范围 |

接口格式见登录后的 `/api/docs`。Client、Server 需一起更新；已移除的 `agentuse work`、`context work` 和 `AGENTUSE_WORK` 不提供兼容入口。
