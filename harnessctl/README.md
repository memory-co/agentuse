# harnessctl/0：本机 Harness CLI 协议

harnessctl 把不同编码 agent 的启动、持续会话、输入投递、取消和审批收敛为命令与 JSONL 事件文件。协议参考 [agentctl/0 设计](https://twwy.net/blog/agent-cli-protocol/)，本文是本目录实现的规范；与原草案的差异明确列在下文。

本目录可以整体独立分发。运行只依赖 Linux、Python 3.9+ 标准库及用户安装并配置好的原生 `codex` 或 `claude`，不需要 Go、Node.js 包、AgentUse 二进制、SDK、服务或数据库。原生 CLI 自身的运行依赖仍须满足。当前仅声明 Linux 支持。

## 1. 分发与调用边界

```text
harnessctl/
├── README.md                    协议规范
├── LICENSE                      Apache-2.0
├── package.py                   独立打包工具
├── lib/
│   ├── runtime.py               CLI、会话进程、IPC、队列和事件写入
│   ├── codex.py                 app-server JSON-RPC 适配
│   └── claudecode.py            stream-json 控制协议适配
├── harness/
│   ├── codex/
│   │   ├── harnessctl           可执行入口
│   │   └── manifest.json
│   └── claudecode/
│       ├── harnessctl
│       └── manifest.json
└── tests/                       仓库内测试，不进入发布包
```

**分发单位是整个 `harnessctl/`，不能只复制某个适配器子目录**，两个入口复用本目录的 lib。每个入口都叫 harnessctl，调用方通过路径选择，不把两个适配器目录同时加入 PATH。包可位于任意路径，调用时 cwd 可以在包外；资源通过入口自身位置定位。包目录按只读资源使用，升级需先关闭使用旧包的会话。

manifest.json 包含 name、version、protocol、entrypoint、platforms、requires、delivery、requests、resume 和投递确认方式。tested_backend_version 是实测基线，不是对所有后续版本的兼容保证。doctor 检查平台、可执行程序与版本命令，不调用模型、不证明所有交互能力可用。安装时可先静态读取 manifest，解压不执行程序。

协议不规定内部必须用 shell、socket 还是常驻进程。本实现每个会话有一个 Python 后台进程，持有原生子进程 stdin/stdout，通过本机 Unix socket 接收命令。socket 使用事件路径摘要寻址，并检查调用方 UID；无需 HTTP 监听端口。原生协议及控制帧始终留在本机。

harnessctl 不认识 AgentUse endpoint、conversation、run、租约、工作令牌或平台文件。上层决定工作目录、准备上下文和文件，并把事件映射到自己的业务对象；依赖方向只能是上层调用 harnessctl。

## 2. 快速使用

```sh
A=/path/to/harnessctl/harness/codex/harnessctl
# 换为 harness/claudecode/harnessctl 后调用方式相同。
"$A" doctor
"$A" start /tmp/review.jsonl --cwd /path/to/repo --id task-1 "检查当前改动，先给出建议"
tail -f /tmp/review.jsonl

# 在另一个终端中：
"$A" send /tmp/review.jsonl --id extra-1 "重点检查分页逻辑"
"$A" send /tmp/review.jsonl --delivery queue "下一轮补上测试"
"$A" status /tmp/review.jsonl
"$A" cancel /tmp/review.jsonl --drop-queued
"$A" close /tmp/review.jsonl
```

相对事件路径以调用命令的 cwd 为基准转换为绝对路径；后续调用应使用同一个绝对路径，不使用不同符号链接别名寻址。start 的事件文件和 `<events>.d/` 必须都不存在，父目录必须存在。不会截断旧文件，也不会自动重新激活退出的会话。

## 3. 命令及退出码

```text
harnessctl doctor
harnessctl start  <events.jsonl> [--cwd DIR] [--resume NATIVE_ID]
                 [--model MODEL] [--backend-arg=ARG ...] [--id ID] <prompt>
harnessctl send   <events.jsonl> [--delivery steer|queue|interrupt]
                 [--if-idle start|fail] [--id ID] <prompt>
harnessctl cancel <events.jsonl> [--drop-queued]
harnessctl reply  <events.jsonl> <request> allow|deny|<native-answer-json>
harnessctl close  <events.jsonl>
harnessctl status <events.jsonl>
```

prompt 是一个非空文本参数；以 `-` 开头时在正文前加 `--`。扩展原生参数使用重复的 `--backend-arg=VALUE`，每次只传一个 argv 元素，不经过 shell。例如 Codex 可传 `--backend-arg=-c --backend-arg='sandbox_mode="workspace-write"'`。本实现不自动跳过权限或关闭 sandbox。环境变量由原生进程继承，认证由用户的 CLI 配置管理，不通过协议传密钥。

- start / send / cancel / reply / close 是动作命令，受理后快速返回，成功时 stdout 为空，错误说明写 stderr；不等待模型或一轮执行完成。start 只等待本机运行进程可联系，最多约 5 秒；原生初始化可能随后失败，须读取事件。
- status / doctor 是查询命令，stdout 恰好一行 JSON。
- 0：命令已受理；doctor 表示基础检查通过。
- 1：适配器、依赖、文件或后端错误；doctor 检查失败。
- 2：用法错误、重复 start、跨适配器使用事件文件，或 input ID 内容冲突。
- 4：会话不存在、已退出、运行进程不可联系，或请求不存在/已回复/已过期。
- 5：send 指定 `--if-idle fail`，受理时没有活动轮次。

**退出码 0 不是完成或成功投递的证明。** 命令超时或 IPC 断开也不保证命令未受理；调用方应检查事件。send 的 `--id` 可在同一个存活的会话进程内去重：相同 ID、正文、delivery、if-idle 重试不重复执行，内容不同返回 2。未提供 ID 则生成 UUID，不保证自动去重。start 也可指定首条输入 ID。去重不跨运行进程恢复；进程退出后只能使用新的事件地址。

## 4. 会话、轮次与输入

一个事件文件代表一个可联系的适配器会话，可容纳多个 turn。Codex 对应一个 native thread，Claude Code 对应一个 native session。一次 start 不等于只能执行一轮；一轮完成后保持进程和输入通道，等待下一条 send。

start 的 `--resume` 从既有原生 session/thread 恢复，仍创建新的事件文件和适配器进程。不可同时用两个进程恢复并写同一个原生 session。原生历史是否存在、模型兼容与恢复权限由后端判断。`native_session` 通过 session.started/status 暴露；它不同于事件地址。

turn 是协议内部轮次标识。Codex 采用 turn/start 回执中的原生 ID；Claude 没有等价的 turn ID，使用适配器生成的 ID，不能把它传回原生 CLI 当作 session ID。Claude 的 turn.started 会标明 adapter_submission、assistant_output 或 result 证据，不表示模型已读输入。

## 5. 投递与取消

| delivery | 语义 | Codex | Claude Code |
|---|---|---|---|
| steer（默认） | 请求追加到当前轮；空闲按 if-idle 处理 | turn/steer + expectedTurnId | 用户消息 priority=next |
| queue | 在适配器队列等待，空闲时发起新轮 | 适配器发 turn/start | 适配器写首条用户消息 |
| interrupt | 先请求中断当前轮，再用新输入开启下一轮 | turn/interrupt，终态后启动 | control_request interrupt，result 后启动 |

`--if-idle start`（默认）在空闲时开启一轮；fail 返回 5，不受理输入。活动 turn 尚未取得 ID 时输入暂存在适配器队列，不向未知轮次 steer。

Codex steer 若遇到活动轮刚结束的错误，输入先出现 input.rejected，再根据 if-idle 排队开启下一轮或 input.dropped。其他错误不会伪装成成功，也不会盲目新开一轮。injected 仅由匹配 expectedTurnId 的成功 RPC 回执确认。

Claude next 没有与轮次关联的插话回执。用户回显只能确认收到，不能证明模型已经消费或输入并入哪一轮，因此记录 input.observed；回显后的轮次结束时可以记录 `input.delivered outcome=unknown`，不冒充 injected。原生 next 在当前轮结束后还可能顺延，混用 steer 与 queue 时也不能从 result/回显顺序推断严格的原生轮次归属。需要严格轮次边界时使用适配器 queue，并避免同时发送未确认的 steer。这个能力差异会出现在 manifest 中。

cancel 只请求中断当前轮，会话继续存在。默认保留适配器队列；`--drop-queued` 删除适配器队列中的输入并产生 input.dropped，Claude 同时把 cancel_queued=true 传给原生控制协议。已经写入原生进程但投递未确认的输入不能仅凭本地取消推定被丢弃；native 响应保留原始细节。

close 结束适配器会话：未投递输入被标记 dropped，活动轮结束为 interrupted，原生进程组先 TERM，最多等待 2 秒再 KILL，追加 session.exited。动作命令本身仍快速返回。外部进程自行脱离进程组不在此保证内。

## 6. 事件文件

UTF-8，每行一个完整 JSON 对象，以换行结束，只追加。每个事件有 type 和 UTC ISO 8601 时间 ts；由原生帧归一化的事件包含 native，完整保留来源帧。适配器自行产生的事件可不含 native。允许新增字段和未知事件类型，调用方应忽略不认识的类型，按完整行读取；字节流的半行要等待后续数据，不当成损坏 JSON。

游标为已读完整行数。所有写入经过同一文件锁，一条事件写完后 flush/fsync；不承诺模型副作用、IPC 回执和事件落盘构成跨进程事务，也不承诺崩溃时恰好执行一次。文件不自动截断、轮转或删除，归档由调用方在 close 后进行。

| 事件 | 关键字段/含义 |
|---|---|
| session.started | adapter、native_session |
| session.exited | reason：closed / terminated / backend_exited / error / lost |
| turn.started | turn；Claude 额外给出 evidence |
| turn.finished | turn、status：completed / interrupted / failed；可带 output |
| message.delta / message | turn、text |
| tool.started / tool.finished | turn、tool、title、status（如有） |
| plan / usage | 原生计划或用量；不伪造缺失数据 |
| input.accepted | input、delivery；表示适配器受理 |
| input.queued | input；进入适配器等待队列 |
| input.observed | input（可关联时）、evidence、native；收到输入的证据 |
| input.delivered | input、outcome：injected / started_turn / unknown、evidence；turn 仅在可确认时给出 |
| input.rejected | input、reason；原生一次投递失败，可能继续转队列 |
| input.dropped | input、reason；不再尝试该条未投递输入 |
| request.opened | request、kind：approval / question、summary、payload、native |
| request.replied | request；已向原生协议写入回复，不等于后端确认完成 |
| request.expired | request、reason；对应轮次已结束，不能再回复 |
| control.response | operation、native；保留原生 interrupt 回执，包括 Claude 的 still_queued（如有） |
| error | message、native（有来源时） |

input.delivered 的 evidence 必须保留。injected 只说明原生协议接受进入所指轮次，不保证模型已按指令行动。unknown 表示无法确认轮次；调用方不应据此无条件重发，以免重复生效。usage 保留原生结构，两家 token 字段不在本协议强行统一，缺失用量不能当作 0。

与 agentctl/0 草案相比，本实现增加 unknown、input.observed、request.replied、request.expired、control.response、plan、usage，以及 start 的 resume/model/backend-arg/id。没有原生确认时不生成 request.resolved，也不根据 Claude 用户回显与 result 的先后关系宣称 injected。

## 7. 审批与提问

```sh
"$A" reply /tmp/review.jsonl request-7 deny
"$A" reply /tmp/review.jsonl request-8 '{"answers":{"q1":{"answers":["Go"]}}}'
```

读取 request.opened 后，使用 request 字段原样回复，不能用 turn 或工具 ID 代替。审批只接受 allow / deny，由适配器转换：Codex command/file approval 使用 accept / decline；Claude can_use_tool 使用 behavior=allow/deny，allow 同时保留原始工具输入。不会自动批准未知请求。

question 的回答是后端原生响应 JSON，不是统一的答案字典；调用方须结合 payload/native。比如 Claude AskUserQuestion 经 can_use_tool 回复时需要原生 behavior/updatedInput 结构。未知原生请求也以 question 暴露，由调用方决定如何回答，不自动套用审批格式。

成功写入只产生 request.replied；没有第二次 native ack 时不声称 resolved。已回复、过期或不存在的 request 返回 4。不同会话的 request 不可互用。

## 8. 状态、故障和文件归属

```text
/path/review.jsonl
/path/review.jsonl.d/
├── config.json           cwd、启动选项与首条输入，私有运行状态
├── status.json           最近状态快照，事件文件不因它覆盖/重写
├── process.json          进程诊断信息
├── runtime.lock          独占运行/失联检测锁
├── events.lock           事件追加锁
├── runtime.stderr.log    适配器诊断
└── backend.stderr.log    原生进程诊断
```

新建目录/文件默认 0700/0600，不把状态写进分发包或用户业务工作区的固定配置文件，不覆盖 AGENTS.md/CLAUDE.md。上层自行决定是否准备 skill、hook 或系统指令。本实现通过原生双向协议处理审批/插话，不依赖 AgentUse 的 hook 桥接。

status 返回 state（idle / running / awaiting_reply / exited）、turn、queued、open_requests、native_session、adapter 和 pid。queued 仅统计适配器自己的队列，不假称能知道原生内部队列。正常轮次失败不会关闭会话；后端进程退出或协议初始化错误则产生 session.exited，后续动作返回 4。

status 优先联系运行进程；联系失败时尝试获取独占运行锁，确认运行进程已消失才补写一次 session.exited reason=lost，并保存 exited 快照。若进程仍持有锁则报错，不误记失联。SIGTERM/INT 执行正常清理；kill -9、主机掉电不保证能写退出事件或清理原生子进程，需要 status 检测并由外部进程管理处理孤儿。本协议不自动重启运行进程、不自动重放未确认输入。

## 9. 测试与分发

```sh
python3 -m unittest discover -s harnessctl/tests -v
python3 harnessctl/package.py
cd harnessctl/dist
sha256sum -c harnessctl-0.1.0-linux.tar.gz.sha256
```

打包工具只读取本目录，产出完整 tar.gz 和 SHA-256 文件，不执行适配器。压缩包使用固定时间与属主元数据，未变更的内容可重复构建。解压后保留目录结构与入口的可执行权限即可调用，无需安装 Python 包。

测试复制完整目录到仓库外，以模拟原生后端验证持续会话、投递、竞态、并发事件写入、审批、取消与退出；不会使用模型或外网。真实 CLI 的模型认证、版本差异和原生工具行为需要另行端到端验证。

本机端到端验证基线（2026-11-01）：

| 原生程序 | 启动/回复 | 同一进程第二轮 | 活动轮 steer | 原生 interrupt |
|---|---|---|---|---|
| Codex CLI 0.165.0 | 通过 | 通过 | RPC 确认 injected | 通过，终态 interrupted |
| Claude Code 2.1.316，使用本机 Kimi 配置 | 通过 | 通过 | 写入 next 输入；无轮次确认，不声明 injected | 通过，终态 interrupted |

真实审批/提问尚未逐项端到端验证；其报文转换、等待回复和过期处理已用模拟原生后端测试。测试基线不代表其他原生版本或模型提供方也具备相同行为。
