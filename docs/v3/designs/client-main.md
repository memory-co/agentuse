# Client 设计

> 本文定义 v3 client 的目标业务划分，尚未据此完整重构代码。Client 通过多个中心领取 run，并在设备本地 session 中执行。服务端总览见 [server-main.md](server-main.md)。

## 一、整体关系

Client 负责本地连接、领取、原生 session 和执行；server 负责 conversation、任务分配及业务状态。两端都按 controller / service / store 组织，但本机 controller 仅提供只读页面，执行服务由 CLI 启动。

![Client Service 关系](../../images/client-main.svg)

- **CLI**：整体装配、注册中心、配置管理、启动常驻程序、查看状态与本机页面。
- **service**：manageService、sessionService、runService、harnessService，各自管理清楚的业务对象。
- **store**：本机 SQLite，保存中心配置、原生 session 记录和执行账本。
- **controller / frontend**：只读查询本机记录，不通过网页启动、取消任务或修改配置。

每中心连接循环属于 manageService 的内部连接机制；进程、流、RPC、hook 等原语属于 harnessService 的本地执行机制，不再和业务 service 平铺为另一组顶层服务。

## 二、有哪些 Service

| Service | 负责什么 | 主要拥有的数据 |
|---|---|---|
| [manageService](client-manage.md) | 管理连接哪些中心、各自凭据与连接实例、共享领取容量与轮询调度 | `centers`、`manage_settings` |
| [sessionService](client-session.md) | 管理原生会话创建/恢复/复用、执行锁与历史同步位置 | 本机 `sessions` |
| [runService](client-run.md) | 接收从中心领来的 run，绑定本地 session，组织输入、路由交互、上报事件与记录执行 | 本机 `runs` 账本 |
| [harnessService](client-harness.md) | 本地接入包探测、原生 session 操作、启动执行、RPC/hook/skill、输出解释 | 本地 YAML、接入包和原生资源，不新增业务表 |

四个 service 共用本机 SQLite 的存储层；本机共四张业务表，和 server 的表不是同一份数据库。关联字段不声明外键，跨服务修改通过业务操作及本机事务完成。

### 2.1 manageService：从哪些中心领任务

manageService 为每个中心保存独立地址、device 身份和凭据，创建各自的连接循环，处理上线、心跳、重连和对账。所有中心共用本机执行容量，默认总并发 1。

领取前预留槽位，轮询某中心一次有界领取，明确返回 run 或 no_task；无任务尝试下一中心。不能向多个中心同时广播长期 ready。任务一旦领到，就绑定 center_id，后续交互、文件和结果只回原中心。

### 2.2 sessionService：原生执行上下文

Client session 直接对应原生 harness 的会话：Codex 的 thread、Claude Code 的 session。**一个 session 包含若干个从 server 领取的 run。** 首次创建原生会话，后续任务可以继续使用；run 完成不删除 session。

默认按 center、conversation、agent、harness 找当前可用会话，并检查包版本及恢复条件。同 session 同时只执行一个 run，多个中心或不同 agent 不共用原生上下文。

### 2.3 runService：一次领来的任务

runService 不在本机另造业务任务。run_id/attempt_id 来自 server，本地用 `(center_id, run_id, attempt_id)` 区分执行，将它绑定到本机 sessions.id 后执行。

runService 取得 run_prompts、共享历史和文件，调用 sessionService 与 harnessService 完成执行，记录进度、问题、插话确认、用量和终态。结果返回原中心，本机状态不代替 server 提交成功。

### 2.4 harnessService：本地原生接入

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

一个 conversation 的多个 agent 共享 server 历史，但在 client 各自有原生 session。一个 server run 的不同 attempt 可以有多条本地执行记录，每条绑定实际使用过的 session。

首次执行准备共享历史；恢复 session 时以历史增量和当前 run_prompts 组织输入，不重复把完整 conversation 灌进已有原生上下文。细节见 sessionService 设计。

## 四、Service 之间怎样协作

| 业务 | 主要调用关系 |
|---|---|
| 注册、启停和连接中心 | CLI → manageService → 本机配置存储 / 中心连接循环 |
| 本地探测与能力上报 | manageService → harnessService；探测结果由各中心连接上报 |
| 领取并执行任务 | manageService → runService → sessionService / harnessService |
| 创建/恢复原生会话 | sessionService → harnessService；保存原生会话 ID 和当前选择 |
| 任务进度/结果 | harnessService → 标准事件回调 → runService → 所属中心连接 |
| 用户答案/补充输入/取消 | 所属中心连接 → runService → 当前 session 内的原生执行 |
| 执行收尾 | runService 结束本次占用，sessionService 保留原生历史，manageService 释放槽位 |
| 本机只读查询 | controller → store.Reader；不调用执行 service |

回调传递标准业务事件，不要求 harnessService 反向管理中心配置或直接访问 server 数据库。Session 的跨 run 生命周期与当前 run 的进程/回调清理分开管理。

## 五、从启动到一个 Run 结束

1. CLI 打开本机 SQLite，装配四个 service 及可选的只读 HTTP 页面。
2. manageService 读取中心配置；新增中心分别注册，已登记中心独立握手和对账。
3. harnessService 本地探测程序与包版本；manageService 向各中心上报可用性和能力。
4. manageService 预留槽位并领取任务，交 runService 校验中心、attempt、版本和本机容量。
5. runService 取得输入与文件，sessionService 选择或创建原生 session，并建立本地运行关联。
6. harnessService 在该 session 中执行；后续 run 恢复同一会话或在既有会话继续，不默认新建。
7. 标准事件更新本机 runs，再回报原中心。文件产物经 fileService 上传，相对路径进入消息 attachments。
8. run 完成后清理本次资源、释放会话锁和全局槽位；session 保留，供后续领来的 run 使用。

## 六、本地数据与中心隔离

| 数据 | 保存方式 |
|---|---|
| 中心配置/凭据、本机容量 | SQLite centers / manage_settings，manageService 管理 |
| 原生 session | SQLite sessions，sessionService 管理本机 UUID 与原生 ID 的映射 |
| 执行记录 | SQLite runs，runService 按 center/run/attempt 记录，并关联本地 session_id |
| Harness 接入包和原生历史 | 本地文件及原生程序自己的持久化，harnessService 管理接入 |
| 业务工作目录 | 按 center_id + conversation_id 隔离，同对话共享业务文件 |
| Hook/配置/临时控制资源 | 按 center_id + run/attempt 隔离，不覆盖其他执行 |

本地 session UUID、原生 session ID、server conversation UUID、server run/attempt 是不同标识。两个中心返回相同 ID 也不能串目录、凭据或执行状态。

默认 90 天保留期只清理过期执行账本，不自动删除当前 session 或原生历史。账本默认仅保存正文前 200 字摘要，用量/费用缺失留空；复用 session 后按本次执行增量计量，不重复累计历史。

## 七、断线、重启和只读页面

网络断开时，manageService 只重连对应中心；sessionService/runService 保持仍受控的本地状态。失去有效租约则结束旧执行；client 重启先对账，不因数据库中有旧 session 或 run 就自行继续工作。

run 取消或进程退出不必删除 session；由本地适配确认是否可恢复。原生历史丢失则标记旧 session 不可用，后续有效任务可建立新会话，旧记录保留。

本机 controller 只使用账本/session 的只读 Reader，不访问中心凭据，不调用四个执行 service。页面默认监听 `127.0.0.1:7801`，仅允许 GET，使用本机令牌与 Host 校验，不开放 CORS。

CLI 负责配置管理和提供页面链接。页面展示执行、所属 session 和用量统计，不提供中心写入或执行控制。

## 相关设计

[manageService](client-manage.md) · [sessionService](client-session.md) · [runService](client-run.md) · [harnessService](client-harness.md) · [Server 总览](server-main.md)
