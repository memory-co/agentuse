# Client settingService：本机配置与临时目录策略

> v3 目标设计，尚未实现。Client 不使用 SQLite；本服务读取 `~/.agentuse/setting.yaml`，该文件是本机设置的唯一持久化来源。

## 一、职责

settingService 加载、校验并提供本机设置：本地页面监听地址/端口、执行容量、各 harness 的临时工作根目录，以及容量限制和自动清理策略。

它不保存 endpoint 注册信息，不领取任务，也不直接删除 session 目录。endpointService 管 `endpoints.yaml`；runService 执行领取调度；sessionService 按设置选择和清理工作目录。

## 二、setting.yaml

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801

execution:
  max_running_runs: 1

cleanup:
  interval: 5m
  target_ratio: 0.8

harnesses:
  codex:
    work_root: ~/.agentuse/tmp/codex
    quota:
      enabled: true
      max_bytes: 21474836480 # 20 GiB
  claude:
    work_root: ~/.agentuse/tmp/claude
    quota:
      enabled: true
      max_bytes: 21474836480 # 20 GiB
```

| 设置 | 含义 |
|---|---|
| local_http | 本机只读页面的开关、监听地址和端口；仅允许回环地址 |
| execution.max_running_runs | 所有 endpoint 共用的执行上限，不是各 server 分别计算 |
| harnesses.<name>.work_root | 该 harness 启动和恢复原生 session 时使用的临时工作根目录 |
| quota.enabled | 是否启用该根目录的容量限制与自动清理 |
| quota.max_bytes | 当前 harness 受管工作目录的总容量上限，正整数字节数 |
| cleanup.interval | 周期检查间隔；启动、执行前后也可触发检查 |
| cleanup.target_ratio | 超限清理目标占比，必须大于 0 且小于 1，避免刚清完又立刻触发 |

默认本机端口 7801、总并发 1；两个 harness 的默认根目录如上。未配置 quota 时默认关闭容量限制，不触发容量回收；示例显式开启。自定义 harness 也使用相同结构。

缺少配置文件时首次生成默认文件再使用，不另存隐藏配置表。端口须合法、执行容量须正数，路径扩展 `~` 后规范化；非法配置在启动时明确报错。运行时修改无效则拒绝应用，保留当前有效内存快照并报告错误，不能静默写回另一份“权威配置”。

## 三、工作目录怎样组织

```text
<work_root>/
  <endpoint_id>/
    <conversation_id>/
      <local_session_id>/
        .agentuse-workspace.json
        ...本次原生 session 的业务文件...
        .agentuse/runs/<run_id>/<attempt_id>/
          ...hook、临时配置和执行资源...
```

Codex 和 Claude Code 可以位于不同磁盘/目录。一个原生 session 的后续 run 默认复用同一工作目录；控制资源按 run/attempt 隔离。不同目录下不承诺本地文件自动互相同步，共享文件以 server conversation 的 fileService 空间为准，经上传/下载交换。

`.agentuse-workspace.json` 标识这是 client 创建的受管目录，至少记录 endpoint/conversation/local session/harness。没有标识的用户目录不作为清理对象。不得把 home、磁盘根目录、原生工具全局用户目录或设置/状态目录作为可回收工作根目录；不同 harness 的根目录不能重叠。

容量只统计这些受管工作目录，不统计 endpoint 密钥、session/run 状态文件、接入包，也不擅自扫描删除 `~/.codex`、`~/.claude` 等原生全局历史。

## 四、超过容量后怎样自动清理

1. runService 在启动、领取执行前后及周期检查时，按 settingService 的策略触发 sessionService 检查。
2. sessionService 统计该 harness 的受管目录；未开启限制或未超限则不因容量删除文件。
3. 超限后按最近使用时间从旧到新回收**整个空闲 session 工作目录**，直到低于 max_bytes × target_ratio。
4. 正在执行、等待用户答案、持有会话锁，或已分配给即将启动任务的目录不能删除；清理与会话选用共用排他保护，不能边恢复边清理。
5. 回收后更新对应 session.yaml 的 workspace_state 为 missing，使其不可直接恢复。后续有效任务建立新原生 session，从 server 历史和文件准备输入，不假装旧工作区仍在。

文件应先通过 fileService 上传再作为产物交付；清理只处理本机临时工作副本，不删除 server 文件。未上传的空闲工作目录内容会随回收删除，这就是启用临时目录回收的语义。

如果超限但只有在用目录，暂不再启动该 harness 的新任务，等待资源释放并重试清理。正在运行的任务可继续，不能为达容量目标杀掉任务或删除其目录。因此这是清理/准入策略，不是文件系统硬配额，执行中的瞬时用量可能超过上限。

## 五、配置变更与归属

| 变化 | 应用方式 |
|---|---|
| 端口/监听设置变化 | 明确重启本机 HTTP 组件，不中断现有 run |
| 执行上限降低 | runService 等已有执行收尾，不为满足新限制强制取消 |
| 工作根目录变化 | 新 session 使用新根目录，已有运行不搬迁；旧目录不在新根目录策略下被越界扫描 |
| 容量策略变化 | 后续检查按新策略执行，仍保护在用目录 |

文件变更通知只用于提醒重读；CLI 或用户编辑后的 setting.yaml 才是持久化事实。sessionService 执行目录选择/回收，harnessService 负责在选定目录内运行，settingService 仅管理配置。

## 相关设计

[Client 总览](client-main.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [runService](client-run.md) · [harnessService](client-harness.md)
