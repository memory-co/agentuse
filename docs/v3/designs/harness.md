# harnessService：将不同 Harness 收敛为标准执行协议

> v3 目标设计，尚未据此修改代码或数据库。YAML 的管理和解析在 server；client 只实现标准执行协议，不内置具体 harness 的识别逻辑。关联字段不声明数据库外键，由 service 校验。

## 职责与边界

harnessService 负责理解不同 harness 的探测、启动、输入输出、插话、提问和取消方式，并将其转换为统一的执行描述及交互动作。

| Server 的 harnessService | 设备上的通用 client |
|---|---|
| 管理、解析、校验 harness YAML | 不读取 YAML，不内置 harness 名称列表 |
| 将配置编译为标准执行描述 | 按描述准备目录、环境和进程 |
| 生成探测命令并解释探测结果 | 执行探测，返回 exit code、stdout、stderr |
| 将业务动作映射到具体输入或协议请求 | 读写进程流、传递协议帧、承接本地回调 |
| 将具体输出解释成统一进度、结果、问题和用量 | 回传原始输出、帧、回调以及通用执行状态 |
| 确定能力和业务动作是否被执行端接受 | 不因本地写入成功就声称业务输入已被消费 |

目标是 **server 理解 harness，client 提供通用执行能力**。Client 处理 command、参数、通道和协议帧，不依据 `codex / claude` 等名称选择分支；名称和配置版本是 server 的配置管理信息。

## 标准执行描述

YAML 是 server 的配置来源，不是下发给 client 的运行时协议。harnessService 将其编译为版本化 JSON 执行描述，包含：

- 要启动的程序、argv、环境变量、工作目录和准备文件。
- 通道类型：进程 stdin/stdout/stderr、JSONL、JSON-RPC、本地 HTTP/hook 回调。
- 通用的建连、帧编码、请求/响应关联、数据转发和清理规则。
- 所需执行协议版本及通用执行能力。

Client 使用通用执行器解释这些描述。持续连接、JSON-RPC 请求关联和 hook 回调接入也是标准原语，不留给某个 harness 的专用适配器。需要新的交互方式时，先定义可复用的协议原语；不能以新增 harness 为由在 client 增加名称判断。

业务输出规则、工具消息含义和动作映射由 server 掌握。标准执行描述表达通用动作；需要本地桥接时，可以携带接入包里版本化的 hook/脚本资源。client 只按资源清单准备并运行它们，不在自身代码中新增某个 harness 的分支。

## 统一业务交互

下面是标准动作的语义，不是要求所有 harness 都提供全部能力。

| 动作 | 统一结果 | Server 如何适配 |
|---|---|---|
| probe | capabilities / unavailable | 下发探测描述，解释原始结果，登记可用性和版本 |
| start | started → progress / result / error | 下发执行描述及任务输入，把原始输出归一化 |
| steer | accepted / rejected | 转换补充输入；获得可验证的接入点接受证据后才 accepted，并记录确认范围 |
| question → answer | question → answered / expired | 将具体工具提问转成标准问题，将用户答案转回具体协议 |
| cancel | cancelled / error | 请求终止或结束协议会话，确认执行结束 |

交互消息通过 `run_id + attempt_id` 定位当前执行，插话再关联 run_prompts 的 ID，答案关联当前问题。旧 attempt 的消息不能影响新执行。

**通道已接收、请求已发送、业务已消费是三个状态。** 比如 stdin 写入成功或 RPC 发出，只表示传输成功；收到对应接入确认才能把 steer 标为 accepted（确认范围见后文）。确认范围不能验证的 harness 不声明对应插话能力，由 runService 安排后续执行。

## Client 需要的通用能力

Client 支持启动/结束进程、持续读写流、JSONL 帧、JSON-RPC 双向请求响应、本地 HTTP/hook 回调及转发、文件准备和清理。Client 上报的是这些协议能力及版本，不是写死的 harness 支持列表。

本地 callback 可由执行描述生成配置，并携带当前 run/attempt 的关联信息；client 接到后转交 server，等待标准响应再回给本机程序。持续进程和本地连接对象保存在 client，具体工具状态由 server 的动作映射处理。

这使 Claude 风格 hook 和 Codex 风格持续 RPC 都能落到同一套原语上。统一协议不要求每个 harness 具有相同功能；可用动作由 YAML 声明能力与 client 支持的协议能力共同决定。

## 探测与执行流程

1. Client 连接设备入口，上报执行协议版本、通用能力和 ready 状态。
2. harnessService 为需要探测的配置生成标准 probe 描述；deviceService 交给对应 client。
3. Client 原样执行并回传探测结果；harnessService 解释结果，deviceService 更新 device_harnesses。Client 不自行遍历内置 harness YAML。
4. Agent 选择 device 和 harness；runService 建立任务时固定配置版本和编译后的执行描述。
5. Client 领取 run，收到标准执行描述、任务输入、上下文与文件引用，不接收 YAML；按通用原语执行并持续回传输出和交互。
6. harnessService 将原始交互转换为标准业务事件，runService 推进 run 状态。插话、答案和取消按相反方向转换成执行动作，再交 client 完成。

Client 不支持描述所要求的版本或原语时明确拒绝；接入包版本改变后，按新版本重新探测，不能沿用旧版本的可用结论。排队任务和执行重试使用建立 run 时固定的执行描述。

## 接入包：YAML、Hook、桥接程序和 Skill 各做什么

**YAML 不能凭空创造 harness 没有的交互能力。** 一个完整接入包可以同时包含配置、运行资源和行为说明，按需提供：

```text
harness-package/
├── harness.yaml       探测、启动、协议映射、能力和资源清单
├── hooks/             本地接入点的桥接程序，可为空
├── skills/            业务操作说明及辅助资源，可为空
└── instructions/      注入本次执行的工作规则，可为空
```

| 部分 | 解决什么 | 不承担什么 |
|---|---|---|
| YAML | 声明命令/参数、消息模板、输出匹配、握手步骤、能力和资源挂载 | 不靠描述让普通 CLI 获得实时 RPC 能力 |
| hook / 桥接程序 | 在设备上承接原生 hook、权限/工具回调，封装输入输出并转发标准消息 | 不决定任务分配或写服务端业务表 |
| skill | 告诉 agent 如何上传/下载文件、引用 attachments、创建/读取工作线程 | 不保证模型必定调用工具，不确认插话消费，不取消进程 |
| 指令文件 | 提供会话共享历史的读取方式、当前工作说明、文件路径和交付要求 | 不成为每个 agent 的独立服务端上下文 |

资源由 server 的接入包管理，harnessService 编译资源清单；client 只负责准备文件、注入环境和运行资源。接入包可有少量原生桥接脚本，这与在 client 二进制中写 `if harness == ...` 是不同的边界。

`harness_revision` 标识整个接入包的版本，包括 YAML 及资源内容。runs.harness_config_yaml 保存配置；execution_spec 中固定资源清单、目标相对路径、内容摘要及内容/版本化下载引用。资源按摘要保存，不能因包升级覆盖仍被旧 run 引用的版本。小型文本可随执行描述下发；不为这些资源新增业务表。

![接入包与标准执行协议](../../images/harness-integration.svg)

### 资源怎样落到设备

Client 按描述把桥接程序、配置和 skill 准备到当前 run/attempt 的独立资源目录，并按摘要验证，原生程序通过本次启动参数引用。Claude 的 hook 配置可以用本次 settings 文件指定；Codex 的 skill 可以用显式输入指向已准备的路径。也可以用目标版本支持的独立插件目录加载 Claude skill。

同 session 可能同时有多个 agent，不能让它们互相覆盖 hook 配置或已有 AGENTS.md/CLAUDE.md。接入包必须声明每个文件的目标路径和加载方式，client 只执行这些通用文件动作；程序结束时清理回调和临时资源，不删除 session 文件空间。

## 每个功能分别怎么接

下表是 v3 选定的接入设计。方法名、字段和具体能力必须用探测到的程序版本验证；“能启动”不自动等于“能插话/提问”。

| 功能 | Codex | Claude Code | 配合资源 |
|---|---|---|---|
| 探测 | 执行版本命令并验证 app-server 所需接口 | 执行版本命令并验证 stream-json、所用 hook 和工具交互能力 | YAML；功能探测不能只看版本字符串 |
| 启动 | 启动 app-server，以持久双向 RPC 进行初始化、建立 thread、启动 turn | 启动 print 模式的持续输入/输出，准备本次配置与 hook | YAML、执行描述；Claude 需要 hook 配置 |
| 输入/上下文 | 将 run_prompts、共享历史和文件引用组织为 turn 输入/指令 | 将相同材料组织为 user 输入和工作说明 | YAML + 指令文件；不是 skill 保证上下文完整 |
| 输出/进度 | 将 item/文本/工具通知映射为 progress | 将 assistant 文本和 tool_use 映射为 progress | server 的输出映射 |
| 计划 | 原生计划通知归一化 | 任务工具调用归一化 | 输出映射；skill 可提示使用计划工具，不能制造计划事件 |
| 最终结果 | 以 turn 的终态确认完成，并选取最终回复 | 以 result 及进程状态确认完成 | 输出映射，不能只看到一段文本就判成功 |
| 用量 | 解析 token usage 通知 | 解析 result 中的 usage/cost | 输出映射；缺失值为空，不伪造为零 |
| 插话 | 向当前 turn 发原生 steer，请求/响应关联 | 在 PreToolUse 或 Stop 注入补充输入 | Codex 不需要 hook；Claude 需要 hook 或等价控制回调 |
| 提问/回答 | 接住 app-server 发起的用户输入请求并回复同一 RPC | 接住 AskUserQuestion，在交互宿主或 PreToolUse 桥接中取得答案 | 双向 RPC / hook 桥接；skill 只指导何时提问 |
| 工具审批 | 原生 approval 请求交给 server 决策 | 权限宿主/权限 hook 转交 server | 与业务提问分开处理，不能所有请求统一 allow |
| 取消 | 原生 interrupt 后等待 turn 结束；超时再终止进程树 | 若所选控制协议支持则请求 interrupt，否则通用终止进程树 | 通用 RPC、进程控制；不依赖 skill |
| 文件交付 | 同一套标准文件操作，经 fileService | 同左 | 业务 skill + 通用文件操作；文件路径写入消息 attachments |
| 工作线程 | 同一套 session/work 操作 | 同左 | 业务 skill/工具入口；工作令牌由 workService 提供 |

### Codex：以 App Server RPC 为主

Codex 使用 app-server 的持续双向连接；主路径不需要 Claude 风格 hook。`thread` / `turn` 是本次原生执行对象，不替代 agentuse 的共享 session。一个新 run 创建新的原生上下文，历史由 session 提供。

Server 的接入规则管理握手、thread/turn 关联、事件解释、交互请求和错误映射；client 只转发关联的 RPC 帧、管理本地进程。

- 启动链：`initialize → initialized → thread/start → turn/start`。
- 插话：`turn/steer` 必须携带当前 thread 与 expectedTurnId；仅匹配请求且返回目标 turnId 的成功响应才能确认该输入被原生协议接受。错误、超时或 turn 已结束都不能当成功。
- 提问：具体版本的用户输入请求方法以生成的协议 schema 为准（文档涉及 `tool/requestUserInput` / `item/tool/requestUserInput`）；保留原请求 ID，转标准 question，答案回到同一请求。实验能力需要显式启用且实际探测通过。本机 codex-cli 0.165.0 生成的 schema 中，方法是 `item/tool/requestUserInput`，返回 `answers[questionId].answers` 字符串数组；不能拿 Claude 的按问题文本映射格式直接回复。
- 取消：`turn/interrupt` 的 RPC 响应只表示请求被接受，等待 `turn/completed` 的 interrupted 终态才结束任务。

上面的方法语义依据 [Codex App Server 官方文档](https://learn.chatgpt.com/docs/app-server)。仓库当前映射为：item/started 与 item/completed 提取文本/工具进度，turn/plan/updated 提取计划，thread/tokenUsage/updated 提取用量，turn/completed 确认终态。字段细节以目标版本 schema 为准；不把同一 thread 的旧 turn 用量累计误当成本 run 新增用量。

技能可以安装为项目级 `.agents/skills/<name>/SKILL.md`，或由执行描述提供显式 skill 输入；它服务于文件、工作线程等业务指导，原生 RPC 插话不靠 skill。[Codex skills 官方说明](https://learn.chatgpt.com/docs/build-skills)

### Claude Code：流式输出 + 原生 Hook 接入点

启动仍采用 `claude -p` 和 stream-json 输入/输出。具体 flags 由配置版本固定；hook、指令文件和业务 skill 必须在启动前准备。不能直接使用跳过这些资源发现的 bare 配置，再假设 hook 已加载。[Claude Code 程序化运行说明](https://code.claude.com/docs/en/headless)

v3 优先用公开 hook 接口配合包内桥接程序。输入是 stream-json 的 user 消息；stdout 的 assistant 文本/工具块映射进度，TaskCreate/TaskUpdate 等调用映射计划，result 的结果、错误和 usage 映射统一终态与用量。输出路径不靠 skill 自报成功。

具体交互：

1. **PreToolUse 插话**：hook 把当前执行和工具信息发到 client 本地通用回调入口；server 决定是否有待注入 run_prompts，返回该版本所需的上下文输出。
2. **Stop 插话**：结束前再检查补充输入，有待处理输入时返回继续执行的 hook 决策；无输入则正常结束。只对新补充输入继续，避免 Stop 无限循环。
3. **AskUserQuestion**：匹配该工具的 hook/交互宿主将问题交给 server，等待用户答案，再把原问题和答案按所选版本的工具输入格式返回。不能把“有一个 hook”当成已经有可用的提问宿主。

具体 hook 回包结构及无终端环境中的提问条件参见 [Claude Code hooks 官方说明](https://code.claude.com/docs/en/hooks)。配置需验证 hook timeout 能覆盖等待窗口；超时、取消、断线要结束当前回调，不能无限占用进程。

可以选 command hook（包内桥接程序用 stdin/stdout）或所选版本支持的 HTTP hook。两条路径统一成标准 callback；包内程序只做本地封装/回传，server 持有工具语义与注入决策。

**不要直接把仓库历史参数当作最新公开契约。** 当前实现曾使用 `--permission-prompt-tool stdio` 和 stream-json 的 control_request/control_response 注册/处理回调；v3 若继续使用，必须锁定通过测试的版本。当前公开 CLI 对 permission-prompt-tool 的说明是 MCP 工具入口，不能假设任意版本仍支持 stdio 值。[Claude Code CLI 官方说明](https://code.claude.com/docs/en/cli-reference)

业务 skill 放到该执行环境的 `.claude/skills/<name>/SKILL.md`；工作规则写入本次准备的 CLAUDE.md。Skill 用于指导调用标准文件/工作操作，不承担 hook 的传输或确认职责。[Claude Code skills 官方说明](https://code.claude.com/docs/en/skills)

![Codex 原生插话与 Claude Hook 插话](../../images/harness-interactions.svg)

## 确认、降级与当前实现差距

统一 `accepted` 要记录接入方式和确认依据，不等于模型已经理解、遵守或完成该输入：

- **Codex** 有匹配当前 turn 的原生 RPC 成功响应，作为 native-input 接受凭据。
- **Claude** 的输入在 hook 边界交付，不是任意时刻主动注入。桥接程序拿到内容、写完 stdout 不等于原生程序确认解析成功；必须在该版本的接入测试中验证交付与继续执行证据，按 hook-delivery 记录确认范围，不能伪称有 Codex 式原生 ACK。
- 只有网络或通道回执时仍保持 pending。无法验证交付、不支持所需操作或 run 已结束，保留输入供后续 run 处理，不丢弃、不报成功。
- 原生程序退出后，桥接脚本、回调等待和子进程也必须收尾；skill 不能保证这些生命周期动作。

当前仓库尚未实现 v3 通用执行器。现有 Codex 启动、插话、进度和取消有专用代码；其用户输入类 server request 目前被拒绝，提问是待补能力。Claude 的 hook/提问处理也还在 client 专用代码中；现有 Claude skill 提及旧附件 CLI，需要随 v3 文件操作一起更新。上面的目标设计不能作为这些能力已经完成的声明。

实现时每个接入包至少验证：探测、首个任务、连续事件、最终结果、执行中插话、结束前插话、提问与回答、提问超时、取消、错误退出、资源安装、文件交付。包升级后重新运行对应检查，再声明能力。

## 配置与表结构

YAML 只由 server 保存和解析；包内 hook、skill、指令资源由 server 管理并下发通用资源清单。共享部分只保留标准执行描述、命令、事件和协议版本的数据结构。

`harnesses` 保存配置定义；`runs.execution_spec` 保存编译后的 JSON 描述。harness_revision 固定原接入包版本，使 server 仍可按该 run 的配置解释交互；执行期间不使用最新同名配置替换旧规则。harnesses 当前版本更新时，旧 YAML 必须随对应 run 一并留存，保存在 runs.harness_config_yaml，仅供 server 解释，不下发 client。

```sql
CREATE TABLE harnesses (
    name TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT,
    owner_id TEXT,
    config_yaml TEXT NOT NULL,
    config_revision TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
```

name 是 server 侧唯一配置标识，kind 为 builtin/custom，owner_id 是自定义配置归属。config_yaml 是完整正文，config_revision 是整个接入包的内容摘要，包括 YAML 及版本化资源。

## 相关设计

[Server 总览](server.md) · [deviceService](device.md) · [agentService](agent.md) · [runService](run.md)
