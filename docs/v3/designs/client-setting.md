# Client settingService：本机配置与临时目录策略

> v3 目标设计，尚未实现。Client 不使用 SQLite；本服务读取 `~/.agentuse/setting.yaml`，该文件是本机设置的唯一持久化来源。

## 一、职责

settingService 加载、校验并提供本机设置：本地页面监听地址/端口、执行容量、sessionService 的统一临时工作根目录，以及容量限制和自动清理策略。

它不保存 endpoint 注册信息，不领取任务，也不直接删除 conversation 工作目录。endpointService 管 `endpoints.yaml`；runService 执行领取调度；sessionService 按设置选择和清理工作目录。

## 二、setting.yaml

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801

execution:
  max_running_runs: 1

session:
  work_root: ~/.agentuse/tmp
  quota:
    enabled: true
    max_bytes: 21474836480 # 20 GiB，所有受管 conversation 工作目录合计
  cleanup:
    interval: 5m
    target_ratio: 0.8
```

| 设置 | 含义 |
|---|---|
| local_http | 本机只读页面的开关、监听地址和端口；仅允许回环地址 |
| execution.max_running_runs | 所有 endpoint 共用的执行上限，不是各 server 分别计算 |
| session.work_root | sessionService 为新 conversation 分配工作目录的默认根目录，不按 harness 划分 |
| session.quota.enabled | 是否启用所有已登记受管工作目录的统一容量限制与自动清理 |
| session.quota.max_bytes | 所有受管 conversation 工作目录的总容量上限，正整数字节数 |
| session.cleanup.interval | 周期检查间隔；启动、执行前后也可触发检查 |
| session.cleanup.target_ratio | 超限清理目标占比，必须大于 0 且小于 1，避免刚清完又立刻触发 |

默认本机端口 7801、总并发 1；默认工作根目录为 `~/.agentuse/tmp`。未配置 quota 时默认关闭容量限制，不触发容量回收；示例显式开启。所有 harness 共用这一目录分配规则。

缺少配置文件时首次生成默认文件再使用，不另存隐藏配置表。端口须合法、执行容量须正数，路径扩展 `~` 后规范化；非法配置在启动时明确报错。运行时修改无效则拒绝应用，保留当前有效内存快照并报告错误，不能静默写回另一份“权威配置”。

## 三、工作目录怎样组织

```text
<session.work_root>/
  <endpoint_id>/
    <conversation_id>/
      .agentuse-workspace.json
      ...同一 conversation 的所有 agent / harness 共享的业务文件...
      .agentuse/runs/<run_id>/<attempt_id>/
        ...本次执行独立的 hook、临时配置和执行资源...
```

sessionService 按 `(endpoint_id, conversation_id)` 分配并持久化唯一工作目录。同一 conversation 的所有原生 session（包括 Codex 和 Claude Code）使用同一个 work_root，可以同时运行；不同 endpoint/conversation 分开。原生上下文仍各自独立，工作文件则直接共享。

setting.yaml 的 session.work_root 是分配新 conversation 目录的默认父目录；实际目录记录在 sessionService 的 workspace.yaml 中，并写入各 session.yaml 的 work_root。已有 conversation 新建或更换 harness/session 时沿用已绑定目录，不重新分配。具体文件见 [sessionService](client-session.md)。

`.agentuse-workspace.json` 标识 client 创建的受管目录，记录 endpoint/conversation/workspace_id；会话状态文件通过 workspace_id 关联该目录。没有标识的用户目录不作为清理对象。不得把 home、磁盘根目录、原生工具全局用户目录或设置/状态/适配器目录作为可回收工作根目录。

容量只统计这些受管工作目录，不统计 endpoint 密钥、session/run 状态文件、接入包，也不擅自扫描删除 `~/.codex`、`~/.claude` 等原生全局历史。

## 四、超过容量后怎样自动清理

1. runService 在启动、领取执行前后及周期检查时，按 settingService 的策略触发 sessionService 检查。
2. sessionService 统计所有受管 conversation 目录，每个共享目录只计算一次；未开启限制或未超限则不因容量删除文件。
3. 超限后取 conversation 内所有 session 最近使用时间的最大值，按该时间从旧到新回收**整个空闲 conversation 工作目录**，直到低于 session.quota.max_bytes × session.cleanup.target_ratio。
4. 任一关联 session 正在执行、等待用户答案、持有执行锁，或已分配给即将启动任务时，整个目录都不能删除。目录使用登记允许多个 session 并发，清理必须取得 conversation 工作目录的独占权限，不能边执行边清理。
5. 回收后将 workspace.yaml 标记为 missing，并更新所有关联 session.yaml 的 workspace_state 为 missing，使其不可直接恢复。后续有效任务建立新原生 session，从 server 历史和文件准备输入，不假装旧工作区仍在。

文件应先通过 fileService 上传再作为产物交付；清理只处理本机临时工作副本，不删除 server 文件。未上传的空闲工作目录内容会随回收删除，这就是启用临时目录回收的语义。

如果超限但只有在用目录，暂不再启动需要该工作空间容量的新任务，等待资源释放并重试清理。正在运行的任务可继续，不能为达容量目标杀掉任务或删除其目录。因此这是清理/准入策略，不是文件系统硬配额，执行中的瞬时用量可能超过上限。

## 五、配置变更与归属

| 变化 | 应用方式 |
|---|---|
| 端口/监听设置变化 | 明确重启本机 HTTP 组件，不中断现有 run |
| 执行上限降低 | runService 等已有执行收尾，不为满足新限制强制取消 |
| 工作根目录变化 | 只对尚未绑定工作目录的新 conversation 生效；已有 conversation 的所有 session 继续使用原绑定目录，不搬迁、不拆分。sessionService 依据已登记映射继续管理旧根目录中的受管目录，不扫描任意旧路径 |
| 容量策略变化 | 后续检查按新策略执行，仍保护在用目录 |

文件变更通知只用于提醒重读；CLI 或用户编辑后的 setting.yaml 才是持久化事实。sessionService 执行目录选择/回收，harnessService 负责在选定目录内运行，settingService 仅管理配置。

## 相关设计

[Client 总览](client-main.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [runService](client-run.md) · [harnessService](client-harness.md)
