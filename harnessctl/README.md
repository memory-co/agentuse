# harnessctl/2：本机 Harness CLI 协议

上层只需要向一个事件文件地址发送消息、取消执行、查询状态和回复请求。首次启动、运行中补充输入、排队、原生会话恢复以及后台进程回收，全部由适配器决定。

协议参考 [agentctl 的设计](https://twwy.net/blog/agent-cli-protocol/)，但公开接口收敛为 **send、cancel、status、reply**。本版取代原 harnessctl/0 和 harnessctl/1，不保留 start、doctor、close、delivery、if-idle、resume、backend-arg 或调用方指定 input ID 等接口。

## 1. 独立分发包

```text
harnessctl/
├── README.md                       协议规范
├── package.py                      仓库打包工具，不是运行依赖
├── harness/
│   ├── codex/                      完整独立包
│   │   ├── harnessctl
│   │   ├── manifest.json
│   │   ├── README.md
│   │   ├── LICENSE
│   │   └── lib/{runtime.py,codex.py}
│   └── claudecode/                 完整独立包
│       ├── harnessctl
│       ├── manifest.json
│       ├── README.md
│       ├── LICENSE
│       └── lib/{runtime.py,claudecode.py}
└── tests/                          仓库测试，不进入发布包
```

**每个 `harness/<name>/` 目录就是一个分发包。** 单独复制其中一个目录即可运行，不依赖父目录、其他适配器、AgentUse 模块或服务。入口只加载包内 lib，不使用包外符号链接或运行时下载补齐依赖。包按只读使用，运行状态保存在调用方的事件文件旁边。

当前依赖 Linux、Python 3.9+ 标准库以及用户安装并配置认证的原生 codex 或 claude。无 Go/Node.js 包依赖。原生 CLI 自身的依赖仍需满足。manifest 声明协议版本、四个命令、stdin 支持、平台、基础依赖和实测后端版本，不让上层选择原生传输或投递策略。认证沿用原生 CLI 的配置和继承环境，不通过本协议传密钥。

## 2. 公开命令

```text
harnessctl send   <events.jsonl> [--cwd DIR] [--model MODEL] [<prompt>]
harnessctl cancel <events.jsonl>
harnessctl status <events.jsonl>
harnessctl reply  <events.jsonl> <request_id> [<answer>]
```

入口按包路径选择，例如 `/opt/harness/claudecode/harnessctl`。多个入口同名，不把不同适配器目录同时加入 PATH。

send、cancel、reply 是动作命令，受理后快速返回，成功时 stdout 为空，错误信息写 stderr；不等待模型或执行完成。status 返回一行 JSON。send 需要启动本地运行进程时，可能等待几秒完成本机就绪握手，但不等待模型回答。

退出码：0 已受理或查询成功；1 运行、文件、后端或恢复错误；2 参数、输入或绑定冲突；4 地址不存在、请求已过期/已回复或失效。退出码 0 不保证模型已消费输入，也不表示执行成功。超时/连接中断可能发生在受理之后，因此不能据此盲目重发；本版不提供调用方幂等键，不自动重放不确定输入。

## 3. stdin 输入

send 省略 prompt、reply 省略 answer 时，从 stdin 读取 UTF-8 内容直到 EOF。参数正文和 stdin 正文都限制为 256 KiB，空白输入和无效 UTF-8 会报错。正文内容（包括换行）原样保留，不经过 shell 展开或拼接。只有权限答案 allow/deny 会忽略首尾空白。

```sh
A=/path/to/claudecode/harnessctl
printf '%s' "$prompt" | "$A" send /tmp/review.jsonl --cwd /path/to/repo
"$A" send /tmp/review.jsonl < prompt.txt
"$A" send /tmp/review.jsonl '重点检查错误处理'

printf '%s' 'deny' | "$A" reply /tmp/review.jsonl req-1
printf '%s' '{"q1":"Go"}' | "$A" reply /tmp/review.jsonl req-2
```

交互终端中省略正文会明确报错，避免意外阻塞等待输入。以 `-` 开头的参数正文使用 `--` 分隔；直接走 stdin 无需处理此类转义。

## 4. 唯一会话身份与配置绑定

**规范化后的绝对事件文件路径就是会话的唯一身份。** 相对路径以命令的 cwd 解析，并消解 `..` 和符号链接。没有单独的公开 session ID，也不要求调用方获取或传入原生 session/thread ID。

第一次 send 自动创建事件文件与私有状态，事件文件父目录必须存在。不会截断已有数据：已有事件文件但缺少匹配的适配器状态时明确报错。一个地址绑定一个适配器，不允许 Codex 和 Claude Code 交叉使用同一个地址。

- cwd 首次采用 `--cwd`，省略则采用调用进程的 cwd；解析为绝对真实路径后绑定此地址。
- model 首次采用 `--model`，省略则使用原生 CLI 的默认配置。
- 后续省略 cwd/model 表示沿用绑定值；显式提供相同值可以，不同值报错。切换工作区或显式模型应使用新的事件地址。

适配器内部会持久化原生恢复标识，但不在公开状态中提供另一个会话 ID。路径与其 `.d/` 状态需要一起保留。只复制 JSONL 而丢失私有恢复信息，不保证能继续原生上下文，也不能默默当成新会话执行。

## 5. send：由适配器决定如何投递

首次 send 自动启动所需进程并建立原生上下文。空闲时继续该上下文；忙碌时尽量向当前执行追加输入，不可直接追加时由适配器保留后续处理。调用方不选择 start、steer、queue 或 interrupt。

Codex 适配器在内部使用 app-server RPC，原生回执确认插入当前交互后才公开该输入的 run_id；遇到交互刚结束的竞态，则内部安排下一次交互并分配新 UUID。Claude Code 缺少可靠的插话轮次回执，当前在忙时内部排队，空闲后通过 stream-json 提交。原生协议不同，不保证两家在完全相同的时机消费补充输入。

等待外部回复、取消过程中或原生初始化尚未完成时，新输入先由适配器保留；cancel 可以取消这些待处理输入。未获得确定证据时，适配器不能宣称输入已经进入某一轮或被模型消费。

accepted 是统一受理事件，包含完整输入、适配器生成的 input_id 和确认的 run_id。每次新交互分配 UUID，确认插入当前交互的补充输入沿用同一个 UUID；排到下一次交互的输入分配新 UUID。使用方不能通过参数指定 run_id。原生投递回执只作 log 诊断，终态通过 result 判断。

## 6. cancel：停止本次工作，保留对话

cancel 请求中断当前执行，并清空适配器尚未处理的输入，过期当前待回复请求。已写入原生队列的补充输入也尽量通过原生取消能力清除；不能确认丢弃的内容不虚报已撤回，更不能保证能撤销已经执行的工具副作用。

cancel 成功受理后状态可暂为 cancelling，待原生执行结束后回到 idle。取消期间新 send 的输入属于新请求，由适配器在取消完成后处理，不与刚取消的旧队列混淆。空闲时 cancel 成功且保持空闲。

cancel 不删除对话历史，也不要求用户关闭进程；之后对同一地址 send，继续使用原有上下文。

## 7. reply：统一权限和问题回答

需要外部决定时追加 state=pending 的 request；status 的 requests 中也能看到当前待回复请求。该请求及后续状态快照都携带所属 run_id。

权限请求：

```json
{"type":"request","ts":"...","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","state":"pending","kind":"permission","message":"允许执行 npm test？"}
```

```sh
"$A" reply /tmp/review.jsonl req-1 allow
"$A" reply /tmp/review.jsonl req-1 deny
```

问题请求包含统一的问题列表：

```json
{"type":"request","ts":"...","run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-2","state":"pending","kind":"question","message":"请回答问题","questions":[{"id":"q1","text":"选择语言","header":"语言","multiple":false,"options":[{"label":"Go","description":"Go 语言"}]}]}
```

问题答案是以公开 question id 为键的 JSON 对象，值为字符串或字符串数组；必须完整回答该请求的所有问题，不允许未知 ID。非多选问题只能给出一个答案，字符串必须非空：

```sh
printf '%s' '{"q1":"Go"}' | "$A" reply /tmp/review.jsonl req-2
```

适配器负责把统一的答案转换为 Codex answers 或 Claude updatedInput/control_response，调用方不构造原生报文。未知、不支持的原生请求明确报错，不默认批准或暴露无法回答的原生 JSON 请求。

request_id 只定位一项待回复请求，不是会话身份；由适配器分配，防止不同轮次复用原生 request ID 时串答。已回复、已取消、执行结束或运行进程失联后的请求返回 4；回复输入无效返回 2，且保留请求供修正后重试。成功写入回复后可靠记录 state=replied 的 request 完整快照及统一答案；过期时记录 state=expired。它们不在缺少原生确认时声称工具已经执行。

## 8. status：业务状态，不是进程状态

```json
{"state":"awaiting_reply","run_id":"550e8400-e29b-41d4-a716-446655440000","queued":0,"requests":[{"run_id":"550e8400-e29b-41d4-a716-446655440000","request_id":"req-1","state":"pending","kind":"permission","message":"执行测试？"}],"cwd":"/path/to/repo","model":null}
```

state 为 idle、running、awaiting_reply、cancelling 或 failed；queued 仅统计适配器可确认的待处理输入数，requests 是统一的待回复请求。公开结果不返回 PID、原生 session/thread ID 或要求上层关联的 turn ID。

运行进程已被正常回收时仍可返回 idle；运行进程存在也可能是 idle。status 本身不启动模型或创建新原生会话，不根据快照中一个 PID 就认定进程活着。失联检测通过本机 IPC 和独占锁完成；确认失联后记录失败、过期待回复请求，不把未确认输入重新执行。事件地址不存在返回 4。

## 9. 进程生命周期和恢复

启动和停止属于适配器内部策略。本实现按需启动每个地址的本地运行进程及原生子进程，空闲约 60 秒后回收；有活动执行、待处理输入、待回复请求或未完成控制握手时不因空闲回收。上层不调用 close，也不需要提供 resume。send 在空闲时发现调用环境改变，会内部回收进程并恢复同一原生上下文，让新的任务身份及凭据生效；忙时拒绝更换环境。只持久化环境摘要，不保存环境变量明文。

下一次 send 自动用私有状态恢复相同的原生 thread/session，不创建新的对话替代旧历史。首次执行已受理但原生恢复信息丢失时，明确拒绝静默重建。原生历史被用户删除、版本不兼容或恢复失败时记录错误，不自动清空对话重新开始。

正常回收会结束原生进程组并保存状态；SIGTERM/INT 也执行清理。kill -9 或主机掉电不保证有机会写事件或清理原生子进程，status 检测后不重放旧输入。外部进程管理需要处理孤儿进程；适配器不能在无法确认旧原生执行已停止时重复恢复执行。

## 10. 事件和私有文件

公共事件仅有五种，完整格式见 [EVENT.md](EVENT.md)，机器格式为 [event.schema.json](event.schema.json)。两个独立包自带相同契约和写前校验器。

| 类型 | 用途 | 可靠性 |
|---|---|---|
| accepted | 完整输入及确认的 run_id/input_id | 可靠落盘后 send 才返回成功 |
| message | 完整正文快照，按 message_id 更新 | 不依赖流式日志恢复正文 |
| request | 权限/问题请求的 pending/replied/expired 完整快照 | 不能丢，包含统一答案或失效原因 |
| result | run 的终态、覆盖的 input_ids、完整输出与计量 | 不能丢，取消/失败也必须表达 |
| log | 流式增量、工具进度、原生控制及进程诊断 | 可丢、可合并、可限流，不承担业务状态 |

文件是 UTF-8 JSONL，只追加。关键事件校验后加锁并 flush/fsync；log 不要求 fsync，其校验或写入失败不改变工作结果。丢掉所有 log 后，仍能重建输入、完整正文、待回复请求及每个 run 的结果。失联恢复按关键事件确认哪些已受理输入仍没有结果，不根据可丢日志猜测，不自动重放。

run_id 由适配器使用 UUID 分配，使用方不能指定。插话归属以原生确认结果为准，accepted 公布后不再改变；不同 run 的事件可以交错出现，按 run_id 关联，而不是按连续行号分段。内部进程回收只写 log，不等于对话结束。

```text
review.jsonl
review.jsonl.d/
├── config.json              已绑定的适配器、cwd 和 model
├── native.json              适配器私有恢复信息
├── status.json              最近状态快照
├── process.json             私有进程诊断
├── control.lock             创建、恢复与动作串行化
├── runtime.lock             运行进程独占锁与失联检测
├── events.lock              事件写入锁
├── runtime.stderr.log
└── backend.stderr.log
```

新建目录/文件默认为 0700/0600。包内不保存运行状态，不覆盖工作区 AGENTS.md/CLAUDE.md。事件和状态不自动删除或轮转，容量管理由调用方完成；删除时须确保原生执行已停止。

## 11. 验证与打包

```sh
python3 -m unittest discover -s harnessctl/tests -v
python3 harnessctl/package.py
python3 harnessctl/package.py --adapter claudecode
cd harnessctl/dist
sha256sum -c harnessctl-codex-0.3.0-linux.tar.gz.sha256
sha256sum -c harnessctl-claudecode-0.3.0-linux.tar.gz.sha256
```

每个压缩包只包含对应的独立适配器目录，使用固定时间和属主元数据，可重复构建。安装/解压不自动执行代码。测试仅复制独立目录到仓库外，并隐藏其他适配器；覆盖 stdin、配置绑定、并发首次 send、忙时补充、取消与恢复、统一回复、失联与空闲回收、独立归档。

## 默认执行权限

内置适配器默认自动放行普通工具操作。Codex 在 thread/start 和 thread/resume 均设置 approvalPolicy=never、sandbox=danger-full-access；Claude Code 使用 --permission-mode bypassPermissions，并保留 stdio 问答通道。这些设置由适配器负责，client/server 不增加权限策略判断。

AskUserQuestion / requestUserInput 等真正需要用户答案的问题仍产生 request(kind=question)，由 reply 回答。若原生在该配置下仍发出显式权限请求，保留 request(kind=permission) 作为例外；不把所有回调一律自动批准，也不把组织策略拒绝、认证错误或执行失败改写成确认请求。
