# Server 设计

> 本文定义 v3 的目标业务划分。service 名称和 device 命名是设计约定，现有源码、表和接口尚未全部按此调整。本稿先说明业务关系，不逐文件展开实现。

Server 保存对话、组织任务并跟踪执行。用户的一台执行设备称为 **device**，设备上运行的程序称为 **client**。Client 从 server 领取 run，按标准执行描述在设备上启动命令并转发交互；具体 harness 的差异由 server 理解。

## 一、整体关系

![Server 业务关系](../../images/server-topology.svg)

Server 分为三层：

- **controller**：HTTP / WebSocket 入口，处理身份、请求和响应，把业务交给 service。
- **service**：按业务对象划分职责，决定如何保存对话、分配任务、管理设备和执行配置。
- **store**：表、查询和事务；各 service 通过它保存自己的业务数据。

设备的在线连接由连接管理组件维护。Service 通过通知接口让它派活、推送插话、取消或答案；连接本身不承担 session/run 的业务规则。

## 二、有哪些 Service

| Service | 负责什么 | 主要拥有的表或数据 |
|---|---|---|
| [userService](user.md) | 用户、角色、访问令牌和身份校验 | `users` |
| [deviceService](device.md) | 执行设备的登记、凭据、在线状态、可用 harness 和执行能力 | `devices`、`device_harnesses`、`device_capabilities` |
| [agentService](agent.md) | Agent 的名称、说明、归属，以及选择哪台 device、哪种 harness 执行 | `agents` |
| [harnessService](harness.md) | 在 server 解析 YAML，将探测、执行及各种交互转换为标准协议 | `harnesses` 和版本化接入包（YAML、hook、skill、指令） |
| [sessionService](session.md) | 清晰、持续的对话：创建会话、保存消息、读取共享历史；调用 runService 分配任务，接收结果事件并保存回复 | `sessions`、`messages` |
| [runService](run.md) | 建立任务、关联任务消息、派发与领取、跟踪执行、插话、提问、取消和执行恢复 | `runs`、`run_prompts`、`run_events`、`run_questions` |
| [fileService](file.md) | 按 session 隔离文件空间，支持本地和 S3 的上传、下载 | 本地目录或 S3 对象前缀，无数据库表 |
| [workService](work.md) | 把多个 session 组织成一项工作，管理协调 agent、线程关系和结果汇总 | `works`、`work_run_tokens`、`run_notifications` |

这里的“拥有”指主要维护职责，不表示其他 service 不能读取。跨业务修改通过所属 service 完成；由编排操作统一事务，不能把各次调用做成互不相关的独立提交。

各 service 的详细职责、操作和完整目标 SQL 见上表链接；15 张业务表各有一篇负责维护；fileService 不使用数据库表。表结构中的 device 命名，以及 harness 配置快照、回复去重约束都是目标设计，尚未应用到运行中的数据库。

所有表不声明数据库外键，只保存关联 ID。创建/修改时由 service 检查目标存在、归属和状态；删除时协调相关 service 显式清理或清空关联字段，并在同一事务提交。数据库保留主键、唯一索引和必要的 CHECK 约束，不依赖自动级联。

sessionService 负责发送消息的业务编排，调用 runService 建立任务分配。runService 完成任务后发出结果事件，由 sessionService 接收并保存回复；runService 不反向调用 sessionService。

### 2.1 userService、deviceService、agentService

这三个对象分别回答“谁使用系统”“在哪里执行”“由哪个 agent 承担任务”。

- 一个用户可以有多台 device 和多个 agent。
- 一台 device 可以运行多种 harness；可用与是否允许使用由 deviceService 维护。
- 一个 agent 选择 device 和 harness，形成默认执行配置。
- run 创建时记录本次执行选择；修改 agent 的配置，不应改写已经存在的 run 的历史。

**Device 是执行位置，agent 是任务角色，harness 是执行方式。** 三者不能合并成一个概念。

### 2.2 harnessService

harnessService 在 server 管理和解析 YAML，把不同 harness 的探测、启动、输入输出、插话、提问、取消收敛为标准执行协议。Client 不持有 YAML，也不需要知道某个命令属于哪种 harness。

| Server 负责 | Client 负责 |
|---|---|
| 解析 YAML，生成版本化标准执行描述 | 执行命令、准备环境和文件 |
| 生成探测动作，解释设备返回的结果 | 执行探测并返回原始结果 |
| 将插话、答案和取消映射成具体动作 | 提供流、JSON-RPC、本地 HTTP/hook 等通用交互原语 |
| 将原始输出归一化为业务事件 | 回传输出、协议帧和本地回调 |

统一业务语义是 probe、start、progress/result/error、steer accepted/rejected、question/answer、cancel。能力可以缺失；比如不支持可确认插话时，由 runService 回退到后续任务，而不是 client 增加某个 harness 的特殊分支。

runService 下发标准执行描述，不下发 YAML。执行时的规则和配置版本固定；共享代码只保留标准协议数据结构；按需的 hook/桥接程序、skill 和指令作为接入包资源下发，client 不为其增加专用分支。详情见 [harnessService](harness.md)。

### 2.3 sessionService 与 runService

- **sessionService 管对话与消息编排**：保存用户消息，调用 runService 分配任务；接收结果事件，把最终回复追加到同一 session。
- **runService 管任务执行**：哪些消息交给哪个 agent，在哪个 device 上执行，目前执行到哪一步。

发送消息时，sessionService 统一事务，消息与任务分配一并提交。结束任务时，runService 保存执行终态和待处理的结果事件，由 sessionService 消费；runService 在事务内检查并绑定 runs.result_message_id 来去重，确保重试通知不会重复追加；messages 不保存 run_id。

进度、计划、问题和插话确认属于 run 的执行记录；需要作为最终回复展示的结果进入 messages。读取会话时可以组合这些数据，但不能把执行事件全当作普通聊天消息。

### 2.4 fileService、workService

每个 session 对应一个文件目录或 S3 对象前缀，同一会话里的执行共享其中的文件。fileService 只管上传、下载和文件空间，不写文件数据库记录；sessionService 管对话，client 在执行时使用这些文件。

Work 是多个 session 的组织方式。workService 维护协调 session、工作线程和协调 agent，在线程任务完成后汇总结果，再经 sessionService 给协调 session 发消息。它复用同一套消息与 run 机制，不另建一种执行模型。

## 三、Session、Message 和 Run 的关系

**一个 session 包含若干个 message 和若干个 run；message 与 run 是 N:M。**

| 关系 | 含义 |
|---|---|
| session → messages：1:N | 一段会话保存多条共享消息 |
| session → runs：1:N | 一段会话可以先后或同时存在多个执行任务 |
| message ↔ run：N:M | 一条消息可以交给多个 agent 的 run；一个 run 也可以处理多条任务消息 |
| message → run_prompts → run | `run_prompts` 为每个目标 run 保存一份消息副本，连接消息和执行 |

Session 和 message 的 ID 统一使用 UUID；消息按独立的 seq 排序。每次发送创建新消息，不设计请求去重字段。

这个 N:M 指的是**任务输入关系**。Run 的最终结果可以成为一条 agent message，但对话关系始终是 message 回复 message：messages.reply_to_message_id 指向被回复的消息，不保存 run_id。一条用户消息触发多个 run，各个 agent 产生的回复都可以指回同一条原始消息。执行层内部通过 runs.result_message_id 追踪结果。

例子：

```text
Session S
  M1：@coder @reviewer 修复并评审登录问题
    → P1（M1 副本）→ R1（coder）
    → P2（M1 副本）→ R2（reviewer）

  M2：@coder 注意移动端兼容
    → P3（M2 副本）→ R1（coder 仍在执行，作为补充消息）
```

这里 M1 对应 R1、R2；R1 同时处理 M1、M2。Session 不绑定某个 agent，每条消息的目标决定任务分配。

`runs.first_message_id` 表示触发 run 的第一条消息，不能拿它表达完整 N:M；完整输入关系由 run_prompts 承载。没有目标的消息可以只进入对话，不创建 run。

Run 是一次业务执行任务。设备故障导致重试时，用 attempt 区分同一个 run 的不同执行尝试；尚未消费的补充任务转入后续执行时，可以建立新的 run。具体恢复规则不在本篇展开。

## 四、Service 之间怎么协作

| 业务 | 主要调用关系 |
|---|---|
| 登录和身份检查 | controller → userService |
| 登记设备、上报在线能力 | controller / 连接入口 → deviceService；deviceService 核对 harnessService 的定义 |
| 配置 agent | controller → agentService；agentService 核对用户、device 和 harness |
| 发送会话消息 | controller → sessionService → runService |
| 建立 run | runService 读取 agentService 的执行选择，核对 deviceService 的能力和 harnessService 的配置 |
| client 领取任务 | 连接入口 → runService；返回统一任务与标准执行描述，不下发 YAML |
| client 上报执行事件 | 连接入口 → runService；结果事件 → sessionService 保存回复 |
| 上传和读取附件 | controller → fileService；结合 user/session/run 的访问范围检查权限 |
| 组织工作、汇总线程结果 | controller 或任务完成通知 → workService → sessionService |

调用方使用的是 service 提供的业务操作，不直接修改其他 service 所属的表。需要发送给 device 的网络通知发生在业务提交之后，避免 client 收到数据库里尚不存在的任务。

## 五、用户消息进来以后

1. **controller 接收。** 校验身份，解析消息、session 和目标 agent，交给 sessionService。
2. **sessionService 保存对话。** 检查会话权限，保存一条 user message；共享 messages 只保存一条，各目标 run 分别写 run_prompts 副本。
3. **runService 建立分配。** 对每个目标 agent，创建 run 或把消息加入已有活跃 run，写入当前 run 的消息副本 run_prompts；读取 agent/device/harness 的执行配置。
4. **统一提交。** 消息和任务分配一并成功或失败。提交后通知在线 device 有任务或插话，HTTP 返回保存结果。
5. **client 领取并执行。** runService 确定当前执行身份；client 读取共享消息和文件，按标准执行描述启动命令并转发协议交互。
6. **上报与呈现。** client 上报进度、问题和结果；runService 更新执行记录并发出结果事件，sessionService 消费后追加最终回复；workService 按需汇总线程结果。

对话是否已保存、任务是否已领取、补充消息是否已接收、执行是否已完成是不同的状态。页面可以把它们放在同一会话里展示，数据模型仍各自清楚。

## 六、Device 命名约定

v3 统一使用 device，不再使用 daemon 表示用户机器。

| 名称 | 含义 |
|---|---|
| device | 一台登记在系统中的用户执行设备 |
| client | 运行在设备上的程序，连接 server、领任务、调用 harness |
| deviceService | Server 侧的设备业务管理 |
| devices | 设备身份、归属、凭据和状态 |
| device_harnesses | 每台设备上各 harness 的可用情况 |
| device_capabilities | 每台设备支持的协议与执行能力 |

代码、API、配置和协议字段在实现 v3 时按此统一命名。概念上始终区分设备这个资源和设备上运行的 client 程序。
