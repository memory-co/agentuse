# Client sessionService：固定事件地址与工作目录

一个 client session 对应一个固定的 events.jsonl 地址。原生 Codex thread 或 Claude session 的身份及恢复状态封装在 harnessctl 内，client 不保存也不选择原生 ID。

## 关系

```text
endpoint / conversation
  workspace/                       所有本机 session 共用的 cwd
  sessions/<client_session_uuid>/
    session.yaml
    events.jsonl                   传给 harnessctl 的唯一会话地址
    events.jsonl.d/                适配器拥有
    runs/<adapter_run_uuid>.jsonl  一个实际交互的引用
```

一个 conversation 可以有多个 Codex、Claude Code 或自定义 harness session，并可以同时在同一 workspace 中工作。工作文件共享，原生上下文各自独立，不自动获得并发文件事务。

一个 session 可以包含多个实际 run；run UUID 必须来自 accepted。同一次交互中的追加输入可能沿用该 UUID。Session 与进程不是同一实体：适配器空闲退出后仍可根据同一事件地址恢复上下文。

## 元数据

```yaml
id: 7877d33b-788f-4f6c-9ff0-987c2128064e
endpoint_id: local
conversation_id: 550e8400-e29b-41d4-a716-446655440000
agent_id: local-codex
harness: codex
harness_revision: package-revision
status: idle
created_at: 1793443200000
last_used_at: 1793443205100
updated_at: 1793443205100
```

id 只是目录定位用的 client UUID，不传给原生 CLI 充当其 session ID。路径来自启动配置和 endpoint/conversation，不冗余保存 work_root、目录映射、原生 ID 或 server 历史游标。时间为 Unix 毫秒。

## 选择、占用与控制

当前实现按 endpoint/conversation/agent/harness/revision 选择最近的有效空闲 session；版本化目标会进一步加入 agent_version_id，见后文。显式向指定 session send 时使用该地址；不能因其忙碌就换成另一个 session 或自行决定新 run ID。

同一个受管 session 由一个驱动处理 send/reply/cancel，追加输入交给适配器裁定插话或排队。全部输入有结果并确认前台工作停止后，session 才恢复 idle。活动阶段为 running，问题是否等待回答从公共事件或 status 获取，不维护重复的问题账本。

包版本不同或历史不可恢复时，不静默更换既有 session 的适配器和事件地址。新会话创建新目录；旧引用仍可读取，显式恢复不可用版本时返回错误。原生全局历史由原生工具管理，不擅自删除 ~/.codex 或 ~/.claude。

cancel 取消 session 当前和排队工作，保留原生上下文；reply 使用公开 request_id。Native status 返回 state、当前 run_id、queued 和公开 requests。Client 不读取 .d/ 的 PID、锁或 native.json 来替代这些命令。

## Agent 版本绑定（目标，尚未实现）

Server agent 版本设计见 [server-agent.md](server-agent.md)。session.yaml 增加 `agent_version_id`，它是来源提供的不可变配置身份，不是原生 session ID 或 run UUID。选择和复用的完整键变为 endpoint/conversation/agent/agent_version/harness/适配器 revision。

同一 agent 发布新版本后，不改旧 session.yaml 的版本字段，也不向旧 events.jsonl 重新 init。下一条新版输入创建新的本地 session 目录及事件地址，按该版资源初始化；旧版工作继续结束，两版仍使用同一 `<conversation>/workspace/`。不存在自动搬迁或拼接原生上下文，新的上下文由 server 共享历史随输入提供。

首次初始化的临时输入放在 session 目录内、workspace 之外：

```text
sessions/<client_session_id>/
  session.yaml                     固定 agent_id、agent_version_id
  init-resources/                  准备期间的临时资源
    AGENTS.md
    packages/<skill_version_id>.zip
    skills/<skill_name>/SKILL.md
    skills/<skill_name>/scripts/...
  events.jsonl
  events.jsonl.d/resources/         init 成功后的适配器快照
  runs/<native_run_id>.jsonl
```

Client 下载并校验指定 ZIP，解包到技能根目录，然后把指令文件、模型和技能根目录交给 harnessctl init；空指令/空 skills/默认模型分别省略对应参数。成功后可以删除 init-resources，后续 send 和原生进程恢复依赖适配器快照，不重新读取 server 的 latest。初始化失败不发送正文，不假造 run。

已存在的 session 只能复用同一 agent_version_id。重启后通过公开 status/init 确认绑定；若需要重新准备资源，只从原输入固定的版本恢复，不能换成当前 head。历史包不可读或校验失败时明确失败。

Local 模式也使用相同的不可变配置和 ZIP 准备流程；由本地输入边界形成版本快照与本地 agent_version_id，其余 session 选择、init、send、校验和恢复规则与远端一致，不绕开版本机制。

## 生命周期和回收

工作根目录是 setting.yaml 的 session.work_root，默认 ~/.agentuse/tmp。目录固定为 <root>/<endpoint_id>/<conversation_id>/，实际 cwd 是其 workspace/，session 元数据在同级 sessions/。修改根目录须停止并重启，需要保留历史时完整迁移目录。

启动时清理遗留前台工作并重建 run 引用；不自动重放旧输入。Session 生命周期状态不承担 server 派发或确认状态。中心事件的 ACK 游标和输入/控制收据隔离在 .server/。

声明 quota 后自动检查受管目录总量，按整个 conversation 最近使用时间从旧到新清理空闲目录。运行、等待回答、清理未确认、预留启动和中心待确认事件均保护目录。没有可回收空间时暂停新启动，不能杀任务来满足容量目标。

回收删除该 conversation 的 workspace、session、引用和输入资源，不删除 server 文件或原生工具的全局历史。旧路径重新出现不代表旧上下文可恢复；必须用新的 session 记录。目录标记只校验受管归属，不是另一张 workspace 映射表。

## 实现归属

sessionService 直接管理 conversation.yaml、session.yaml、workspace 和会话目录，提供 conversation/session 查询、Acquire、Release、指定 session 复用、重启元数据恢复以及 quota 回收。所有这些操作位于 service/session，直接通过 fileutil 的通用原子写入和路径检查操作文件，不转发给 store。

runService 依赖 sessionService 完成占用与释放；HTTP controller 只接收其只读查询接口。目录正在执行或被保留时不得回收；中心未确认事件的保护条件由 runService 注入，sessionService 不读取或理解交付协议。运行互斥和目录保留计数仅协调进程内并发，文件仍是唯一持久化事实。
