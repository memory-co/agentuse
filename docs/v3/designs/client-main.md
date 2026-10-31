# Client 设计

> 本文定义 v3 client 的目标业务划分，尚未据此完整重构代码。Client 通过多个中心领取 run，并在设备本地 session 中执行。服务端总览见 [server-main.md](server-main.md)。

## 一、整体关系

Client 负责本地连接、领取、原生 session 和执行；server 负责 conversation、任务分配及业务状态。两端都按 controller / service / store 组织，但本机 controller 仅提供只读页面，执行服务由 CLI 启动。

![Client Service 关系](../../images/client-main.svg)

- **CLI**：整体装配、注册中心、配置管理、启动常驻程序、查看状态与本机页面。
- **service**：endpointService、settingService、sessionService、runService、harnessService，各自管理清楚的业务对象。
- **文件读写层**：按 service 归属读写 YAML/JSON，不使用 SQLite、数据库表或持久化索引；内存索引可从文件重建。
- **controller / frontend**：只读查询本机记录，不通过网页启动、取消任务或修改配置。

每个 endpoint 的连接循环属于 runService 的内部连接机制；进程、流、RPC、hook 等原语属于 harnessService 的本地执行机制，不再和业务 service 平铺为另一组顶层服务。

## 二、有哪些 Service

| Service | 负责什么 | 主要拥有的数据 |
|---|---|---|
| [endpointService](client-endpoint.md) | 管已注册 server 的地址、device 身份和凭据 | `~/.agentuse/endpoints.yaml` |
| [settingService](client-setting.md) | 管端口、session 共享工作根目录和容量回收策略 | `~/.agentuse/setting.yaml` |
| [sessionService](client-session.md) | 管理原生会话复用、执行锁、历史位置及空闲工作目录回收 | 每个原生会话的 `session.yaml` 与 conversation 的 `workspace.yaml` |
| [runService](client-run.md) | 读取 endpoints 和设置，自行领取/执行、上报事件并触发清理 | 每个执行尝试的 `run.json` |
| [harnessService](client-harness.md) | 本地接入包探测、原生 session 操作、启动执行、RPC/hook/skill、输出解释 | `~/.agentuse/harness/` 下的适配器与资源文件 |

Client 共五个 service，全部以各自文件作为唯一持久化事实，不设本机数据库。Server 仍使用自己的业务数据库，不受本次调整影响。

### 2.1 endpointService：注册信息

endpointService 只读写 endpoints.yaml，记录 client 已向哪些 server 注册，以及对应地址、device_id、device_key 和启用状态。CLI 完成注册后保存记录，runService 读取这些记录。

endpointService 不创建中心连接，不领取任务，不管理槽位，也不向 runService 派活。配置变更通知只提示消费者重读数据。

### 2.2 settingService：本地运行配置

settingService 读取 setting.yaml，管理本机 HTTP 端口、sessionService 使用的统一临时工作根目录，以及容量限制和自动清理策略。它提供校验后的配置，不领任务、不直接删目录。

配置容量超限时，由 runService 触发 sessionService 回收空闲受管工作目录；正在执行或等待答案的目录受保护。声明 quota 即启用自动清理，不额外配置 cleanup。根目录容量属于清理/准入策略，不是会强杀进程的硬配额。

### 2.3 sessionService：原生执行上下文

Client session 直接对应原生 harness 的会话：Codex 的 thread、Claude Code 的 session。**一个 session 包含若干个从 server 领取的 run。** 首次创建原生会话，后续任务可以继续使用；run 完成不删除 session。

默认按 endpoint、conversation、agent、harness 找当前可用会话，并检查包版本及恢复条件。同 session 同时只执行一个 run，多个中心或不同 agent 不共用原生上下文。状态写入 session.yaml，目录位置来自 settingService；空闲目录被回收后不能假装原工作区仍可恢复。

### 2.4 runService：一次领来的任务

runService 从 endpointService 读取启用的注册信息，自行连接各 server，维护心跳、重连和对账，并在本机共享容量内轮询领取任务。领取预留与并发调度由 runService 内部管理，不向用户暴露执行数量配置；不同 session 可同时运行，不能向多个 endpoint 同时广播长期 ready。

runService 不在本机另造业务任务。run_id/attempt_id 来自 server，本地用 `(endpoint_id, run_id, attempt_id)` 区分执行，将它绑定到本机 session.yaml 的 id 后执行。

runService 取得 run_prompts、共享历史和文件，调用 sessionService 与 harnessService 完成执行，记录进度、问题、插话确认、用量和终态到 run.json。结果返回原中心，本机状态不代替 server 提交成功。

### 2.5 harnessService：本地原生接入

harnessService 解析本地接入包，执行 create/resume/run/steer/answer/cancel 等原生操作，并将输出解释为标准业务事件。Codex RPC 和 Claude hook 的差异全部留在设备上。

YAML、hook、桥接程序、skill 与指令资源本地管理；server 不下发执行命令、脚本、执行描述或原生协议帧。接入包升级先探测再上报，旧任务不静默换用新版本。

## 三、Conversation、Session 和 Run

| 对象 | 归属 | 关系 |
|---|---|---|
| conversation | server | 共享消息和文件，含多个 agent 的任务 |
| session | client / 原生 harness | 一个持久原生执行上下文，可以先后执行多个 run |
| run | server 分配，client 执行 | 从指定中心领取，在一个本地 session 中完成本次尝试 |

```text
Server Conversation C
  ├─ Agent coder    → R1、R2 → Client Session S1 → Codex Thread T1
  └─ Agent reviewer → R3、R4 → Client Session S2 → Claude Session H1
```

一个 conversation 的多个 agent 共享 server 历史，在 client 各自有原生 session，但使用同一个 conversation 工作目录。一个 server run 的不同 attempt 可以有多条本地执行记录，每条绑定实际使用过的 session。

首次执行准备共享历史；恢复 session 时以历史增量和当前 run_prompts 组织输入，不重复把完整 conversation 灌进已有原生上下文。细节见 sessionService 设计。

## 四、Service 之间怎样协作

| 业务 | 主要调用关系 |
|---|---|
| 保存注册信息 | CLI 注册流程 → endpointService → endpoints.yaml |
| 读取运行设置 | settingService → setting.yaml；CLI/各服务读取配置快照 |
| 连接各 server | runService 读取 endpointService → 各 endpoint 连接循环 |
| 本地探测与能力上报 | runService → harnessService；探测结果由 runService 的各连接上报 |
| 领取并执行任务 | runService 预留容量并领取 → sessionService / harnessService |
| 创建/恢复原生会话 | sessionService → harnessService；保存原生会话 ID 和当前选择 |
| 任务进度/结果 | harnessService → 标准事件回调 → runService → 所属中心连接 |
| 用户答案/补充输入/取消 | 所属中心连接 → runService → 当前 session 内的原生执行 |
| 执行收尾 | runService 结束本次占用，sessionService 保留原生历史，runService 释放槽位 |
| 容量清理 | runService 按设置触发 → sessionService 回收空闲受管目录 |
| 本机只读查询 | controller → 文件 Reader；不调用执行 service |

回调传递标准业务事件，不要求 harnessService 反向管理中心配置或直接访问 server 数据库。Session 的跨 run 生命周期与当前 run 的进程/回调清理分开管理。

## 五、从启动到一个 Run 结束

1. CLI 加载 setting.yaml 和 endpoints.yaml，装配五个 service，按设置启动可选的本机 HTTP。
2. CLI 注册流程将成功注册的信息存入 endpointService；runService 读取 endpoints，独立握手和对账。
3. harnessService 本地探测程序与包版本；runService 向各 server 上报可用性和能力。
4. runService 自行预留槽位并领取任务，校验 endpoint、attempt、版本和本机容量。
5. runService 取得输入与文件，sessionService 选择或创建原生 session，并建立本地运行关联。
6. harnessService 在该 session 中执行；后续 run 恢复同一会话或在既有会话继续，不默认新建。
7. 标准事件原子更新本机 run.json，再回报原中心。文件产物经 fileService 上传，相对路径进入消息 attachments。
8. run 完成后释放会话锁和槽位，按配置检查临时目录容量；session 状态文件保留，未被回收的原生上下文供后续 run 使用。

## 六、文件是唯一持久化事实

```text
~/.agentuse/
├── endpoints.yaml
├── setting.yaml
├── harness/<harness>/<revision>/...
├── state/
│   └── workspaces/<endpoint_id>/<conversation_id>/workspace.yaml
├── tmp/<endpoint_id>/<conversation_id>/
│   ├── workspace/    所有 Codex / Claude Code 的共享启动目录
│   └── sessions/<session_id>/
│       ├── session.yaml
│       └── runs/<run_id>/<attempt_id>/run.json
└── ui.token
```

tmp 是默认父目录，可通过 setting.yaml 的 session.work_root 配置。sessionService 按 endpoint/conversation 绑定唯一工作目录，同一 conversation 的 Codex、Claude Code 等原生 session 都从该目录下的 workspace/ 启动，并允许并行运行；sessions/ 与 workspace/ 同级保存元数据，session.yaml 不保存 work_root。配置、workspace 映射和适配器不放入可回收目录；session.yaml、run.json 和执行资源按 conversation/session 层级放入工作目录，随目录一起回收；server fileService 负责上传下载和交付。

| 文件 | 唯一负责写入的 Service |
|---|---|
| endpoints.yaml | endpointService |
| setting.yaml | settingService（CLI 通过它修改） |
| session.yaml / workspace.yaml | sessionService |
| run.json | runService |
| 接入包和原生资源 | harnessService 与本地包安装流程 |

单个文件采用同目录临时文件、完整写入后原子替换；CLI 修改配置使用文件锁，执行实例和会话选用使用排他保护，避免并发覆盖。同一业务记录不再同时维护 YAML、JSON 和数据库三份状态。

文件通知只用于提醒重读。列表、会话选择和用量统计的内存索引必须能通过扫描状态文件完整重建；不增加落盘数据库或持久化索引作为第二个来源。

多文件更新不等于数据库事务。崩溃后读取状态文件，与 server 租约和原生状态核对；无法确认时记中断或建立新 session，不能仅凭旧 running 标记重跑。

本地 session UUID、原生 session ID、server conversation UUID、server run/attempt 各自独立；路径包含由规范化 server 地址推导的 endpoint_id（如 https---company.example.com），避免多个平台返回相同 ID 时串目录或结果。

## 七、目录容量与清理

settingService 提供 session 的默认父目录及统一容量策略；sessionService 按 conversation 回收带受管标记的空闲共享目录，每个目录只计量一次，按其所有 session 的最近使用时间清理至不超过 quota。任一关联 session 正在运行、等待问题或已被任务锁定时，整个目录不能删。

目录回收会删除其中的 session.yaml、run.json 和执行资源，但保留 workspace.yaml、server 文件和原生工具的全局历史。后续不得直接恢复旧工作区；有任务时从共享历史和文件重建新会话。没有可清理空间时暂停新启动，不强杀在用任务。

本机执行记录随工作目录保留与回收，不另设固定保留期或归档目录。仍在执行、等待答案或结果尚未获 server 确认的记录保护整个目录。正文默认保留前 200 字，用量未知留空。

## 八、断线、重启和只读页面

网络断开时，runService 只重连对应 endpoint；sessionService 保持仍受控的本地会话状态。失去有效租约则结束旧执行；client 重启先对账，不因文件中有旧 session 或 run 就自行继续工作。

run 取消或进程退出不必删除 session；由本地适配确认是否可恢复。原生历史丢失则标记旧 session 不可用，后续有效任务可建立新会话，旧记录保留。

本机 controller 只使用 session/run 文件的只读 Reader，不访问中心凭据，不调用五个业务 service。Reader 只暴露查询，不提供写文件或清理目录方法；不再依赖 SQLite 只读连接保证权限。

页面的监听地址、端口和开关由 settingService 提供，默认 `127.0.0.1:7801`，仅允许 GET，使用本机令牌与 Host 校验，不开放 CORS。

CLI 负责配置管理和提供页面链接。页面展示执行、所属 session 和用量统计，不提供中心写入或执行控制。

## 相关设计

[settingService](client-setting.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [runService](client-run.md) · [harnessService](client-harness.md) · [Server 总览](server-main.md)
