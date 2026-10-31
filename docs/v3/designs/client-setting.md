# Client settingService：本机配置

> v3 目标设计，尚未实现。Client 不使用 SQLite；本服务读取 `~/.agentuse/setting.yaml`，该文件是本机设置的唯一持久化来源。

## 一、职责

settingService 加载、校验并提供本机设置：本地页面监听地址/端口、sessionService 的统一临时工作根目录，以及容量限制和自动清理策略。

它不保存 endpoint 注册信息，不领取任务，也不直接删除 conversation 工作目录。endpointService 管 `endpoints.yaml`；runService 执行领取调度；sessionService 按设置选择和清理工作目录。

## 二、setting.yaml

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801

session:
  work_root: ~/.agentuse/tmp
  quota:
    max_bytes: 21474836480 # 20 GiB，所有受管 conversation 工作目录合计
```

| 设置 | 含义 |
|---|---|
| local_http | 本机只读页面的开关、监听地址和端口；仅允许回环地址 |
| session.work_root | sessionService 为新 conversation 分配工作目录的默认根目录，不按 harness 划分 |
| session.quota.max_bytes | 所有受管 conversation 工作目录的总容量上限，正整数字节数 |

默认本机端口 7801；默认工作根目录为 `~/.agentuse/tmp`。声明 session.quota 就启用容量限制与自动清理，不再配置 enabled 或 cleanup。省略 quota 表示不限容量、不因容量回收；声明时 max_bytes 必须为正整数。所有 harness 共用这一目录分配规则。

缺少配置文件时首次生成默认文件再使用，不另存隐藏配置表。端口须合法，路径扩展 `~` 后规范化；非法配置在启动时明确报错。运行时修改无效则拒绝应用，保留当前有效内存快照并报告错误，不能静默写回另一份“权威配置”。

目录布局、共享规则及容量回收流程由 sessionService 管理，见 [工作目录怎样组织](client-session.md#七工作目录怎样组织) 和 [超过容量后怎样自动清理](client-session.md#九超过容量后怎样自动清理)。

## 三、配置变更与归属

| 变化 | 应用方式 |
|---|---|
| 端口/监听设置变化 | 明确重启本机 HTTP 组件，不中断现有 run |
| 工作根目录变化 | 只对尚未绑定工作目录的新 conversation 生效；已有 conversation 的所有 session 继续使用原绑定目录，不搬迁、不拆分。sessionService 依据已登记映射继续管理旧根目录中的受管目录，不扫描任意旧路径 |
| 容量策略变化 | 后续检查按新策略执行，仍保护在用目录 |

文件变更通知只用于提醒重读；CLI 或用户编辑后的 setting.yaml 才是持久化事实。sessionService 执行目录选择/回收，harnessService 负责在选定目录内运行，settingService 仅管理配置。

## 相关设计

[Client 总览](client-main.md) · [endpointService](client-endpoint.md) · [sessionService](client-session.md) · [runService](client-run.md) · [harnessService](client-harness.md)
