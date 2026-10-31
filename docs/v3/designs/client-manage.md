# Client manageService：多个任务中心的管理

> v3 目标设计，尚未实现。中心指一套独立的 agentuse server；一个 client 可以同时登记并连接多个中心。整体结构见 [client-main.md](client-main.md)。

## 一、职责

manageService 管理“这台 client 从哪些中心领任务，以及这些中心怎样共用本机执行资源”。

- 管理中心配置的新增、修改、启用、停领和移除。
- 为每个中心维护独立的地址、设备身份、凭据和连接实例。
- 按本机统一容量安排各中心的领取机会，避免同时超量领活。
- 给每次执行绑定所属中心，保证后续交互、文件和结果不串中心。

manageService 不执行 harness，不决定 server 内的任务顺序，也不在中心之间转移任务。主循环维护连接，Run 管理控制执行，harnessService 负责本地原生协议。

## 二、中心配置

每个配置项有一个本地生成且稳定的 `center_id`（UUID）。它区分中心资源命名空间，不等于 server 中的用户或 device ID。

| 字段 | 含义 |
|---|---|
| center_id | 本地稳定身份，用于关联执行、凭据、目录和账本 |
| name | 供用户识别的中心名称 |
| server_url | 该中心的访问地址，HTTP 与 WebSocket 使用同一业务中心配置 |
| enabled | 是否允许连接并领取新任务；关闭后已有执行先完成收尾 |
| device_id | 这台设备在该中心登记后的身份 |
| credential_ref | 指向本机该中心的设备凭据，不共享其他中心密钥 |

配置示意：

```json
{
  "max_running_runs": 1,
  "centers": [
    {
      "center_id": "66c645f1-f128-4c83-b29a-93bd1f181017",
      "name": "公司中心",
      "server_url": "https://company.example.com",
      "enabled": true,
      "device_id": "device-company",
      "credential_ref": "centers/company.key"
    },
    {
      "center_id": "cc9b6a6f-3d19-4cdd-b5e8-217eb15e7784",
      "name": "个人中心",
      "server_url": "https://personal.example.com",
      "enabled": true,
      "device_id": "device-personal",
      "credential_ref": "centers/personal.key"
    }
  ]
}
```

每个中心分别注册。相同电脑可以在不同中心有不同 device_id、用户归属和权限，不要求中心互相知道彼此。修改展示名称不改变 center_id；连接到另一套 server 时新增中心身份，不复用旧执行的命名空间。

这些配置和凭据存本机文件，不新增 server 表，也不为中心配置新增本机 SQL 表。CLI 负责修改配置；本机只读页面仍不提供配置写入或执行控制。

## 三、与主循环和执行层的关系

```text
CLI → manageService
          ├─ 中心 A → 主循环 A → 注册/握手/心跳/对账
          ├─ 中心 B → 主循环 B → 注册/握手/心跳/对账
          └─ 本机共享容量与领取调度
                         ↓
              Run 管理 → harnessService
                         ↓
                事件返回执行所属中心
```

| 组件 | 向 manageService 提供/取得什么 |
|---|---|
| CLI | 新增/修改/启停中心配置，查询各中心状态 |
| 每中心主循环 | 汇报连接状态；按调度授权发起一次领取，维护独立心跳与对账 |
| Run 管理 | 接收带中心上下文的任务；汇报执行开始/结束、释放本机槽位 |
| harnessService | 共用本地接入包，不管理中心密钥；使用本次执行明确传入的业务上下文 |
| 本机账本 | 保存 center_id、服务地址和执行身份，支持按中心查询 |

同一中心的连接失败只触发该连接的退避重连，不阻塞其他中心。能力探测在本地复用，但每个中心的 harness 启用状态和权限独立。

## 四、怎样从多个中心领任务

第一版所有中心共享本机 `max_running_runs`，默认 1。正在等用户答案的 run 也占槽位；不为了让另一个中心领活而抢占它。

1. 有空闲容量时，manageService 先在本机预留一个领取槽位。
2. 对已连接、允许领取的中心轮询，授权某一个主循环发起**一次有界领取**。
3. 中心明确返回 run 或 no_task；没有任务立即释放预留并尝试下一个中心。超时/断线进入该中心退避，不让其他中心无限等待。
4. 收到 run 后，将预留转为执行槽位，交 Run 管理处理；实际运行总数不得超过本机容量。
5. 执行结束释放槽位，从下一个可用中心继续轮询。多个空闲槽位也必须逐个原子预留。

**不能向所有中心同时广播一个长期有效的 ready。** 否则每个 server 都认为设备空闲，可能同时派来多个 run。多中心模式要求领取协议明确返回任务或无任务，不采用没有结束条件的 ready 授权。

领取超时后的晚到任务不能直接启动：先核对当前领取授权；已失效则向原中心说明拒绝/取消或交其租约恢复。即使网络结果不确定，本机也不能跳过槽位检查而超量执行。

轮询只分配领取机会，不保证不同中心的任务完成时间相同；长任务不因轮询被切断。Server 继续负责自己中心内的任务排队和租约。

## 五、中心隔离

| 数据/动作 | 隔离规则 |
|---|---|
| 执行身份 | 本地用 `(center_id, run_id, attempt_id)` 定位；不能只用远端 run_id |
| 共享对话 | 本地目录包含 center_id 和 conversation UUID，例如 `workspaces/<center_id>/<conversation_id>/` |
| 控制资源 | 路径包含 center_id、run_id、attempt_id，隔离 hook、配置和临时文件 |
| 账本 | 保存 center_id，按 `(center_id, run_id, attempt_id)` 区分记录 |
| 心跳/事件/结果 | 发回执行绑定的中心，不发给“当前选中的中心” |
| 文件和工作 API | 使用该次执行的中心地址、设备/工作凭据，路径仍相对该中心的 conversation 空间 |
| 插话/答案/取消 | 从连接所属中心派生 center_id，再查对应执行；不信任远端消息自行声明另一个中心 |

两个中心即使返回相同的 conversation/run/attempt ID，也不能共享目录、状态或结果。凭据按执行显式传入，不能通过修改整个 client 进程的全局环境来切换中心；否则并行执行可能错用另一中心的地址与密钥。

## 六、修改配置与退出

- **新增中心**：保存本地身份，单独注册并建连接，上报能力后参与领取。
- **停领中心**：立即停止新领取，保留已有执行的连接、续租和结果回报，完成后退出该中心连接。
- **移除中心**：先停领，已有任务结束后再移除配置/凭据；需要立即终止时显式取消，不能直接删掉凭据留下失管进程。
- **中心掉线**：只重连该中心；其执行仍占本机槽位，直到恢复、终结或确认租约失效后清理。
- **client 退出/重启**：各中心分别收尾/对账，不能把一个中心的 valid_runs 用于其他中心。

本机配置管理与本机页面的只读边界保持不变。manageService 的配置写入入口是本地 CLI，不开放给任意远端中心。

## 相关设计

[Client 总览](client-main.md) · [本地 harnessService](client-harness.md) · [Server 总览](server-main.md) · [deviceService](server-device.md) · [runService](server-run.md)
