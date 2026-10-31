# Client manageService：多个任务中心的管理

> v3 目标设计，尚未实现。manageService 使用 client 本机 SQLite 保存配置；不访问 server 数据库，不使用外键。中心指一套独立的 agentuse server；一个 client 可以同时登记并连接多个中心。整体结构见 [client-main.md](client-main.md)。

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
| device_key | 该中心签发的设备凭据，存本机 SQLite，不与其他中心共享 |

配置的权威来源是本机 SQLite，不再保存一份中心 JSON 配置或单独凭据引用文件。CLI 通过 manageService 读写，主循环读取已提交配置启动对应连接。

每个中心分别注册。相同电脑在不同中心可以有不同 device_id、用户归属和权限。改展示名称不改变 center_id；连接另一套 server 时新增中心记录，不复用旧执行的命名空间。

### centers：中心连接配置

`id` 为本机生成的 UUID，对外及其他本地记录称为 center_id。新增尚未注册时，device_id、device_key 都为空；注册后在一个本机事务内一起保存；未注册完成的中心不建立业务连接、不参与领取。name 用于显示，server_url 保存中心业务地址。

```sql
CREATE TABLE centers (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    server_url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    device_id TEXT,
    device_key TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (device_id IS NULL AND device_key IS NULL)
        OR (device_id IS NOT NULL AND device_key IS NOT NULL)
    )
);
CREATE INDEX centers_enabled ON centers(enabled,id);
```

Connection 状态、重连计时和领取预留属于运行时内存，不写进 centers；进程重启后重新建连接和对账，不用旧 online 标记判断设备仍在线。

### manage_settings：本机共享领取设置

单行配置，id 固定为 1；max_running_runs 是所有中心合计的执行上限，不是每中心各有一份。首次打开本机库时建立默认记录。

```sql
CREATE TABLE manage_settings (
    id INTEGER NOT NULL PRIMARY KEY CHECK(id=1),
    max_running_runs INTEGER NOT NULL DEFAULT 1 CHECK(max_running_runs>0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

降低容量时，已有执行继续收尾；运行数降到限制以内前不再领新任务。内存中的槽位预留由 manageService 串行/原子管理，不通过新增“槽位表”恢复进程。

### 与执行账本的关联

本机账本保留 center_id，按 `(center_id, run_id, attempt_id)` 唯一区分执行，并保留任务接收时的服务地址。它与 centers 不声明外键，移除中心记录不删除历史账本；重新添加中心生成新的 UUID，不接管旧记录。

这两张配置表与执行账本共用 client 本机 SQLite。manageService 使用专用的配置存储接口，负责读取设备凭据和修改配置；本机页面的 Reader 只暴露执行记录/统计，不提供 centers.device_key 或原始配置查询。

本机数据库及其备份按设备凭据文件管理，限制为当前系统用户访问。CLI 状态输出不打印 device_key；本机只读页面也不增加中心写入接口。

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

- **新增中心**：插入 centers 记录，单独注册并事务保存该中心身份/凭据，再建连接，上报能力后参与领取。
- **停领中心**：将 centers.enabled 设为 0，立即停止新领取，保留已有执行的连接、续租和结果回报，完成后退出该中心连接。
- **移除中心**：先停领，已有任务结束后再删除 centers 配置及凭据记录，保留历史账本；需要立即终止时显式取消，不能直接删掉凭据留下失管进程。
- **中心掉线**：只重连该中心；其执行仍占本机槽位，直到恢复、终结或确认租约失效后清理。
- **client 退出/重启**：各中心分别收尾/对账，不能把一个中心的 valid_runs 用于其他中心。

配置写入提交后通知运行中的 manageService 重载；启动及定期核对也以 SQLite 为准，不仅依赖一次内存通知。数据库事务不跨越注册请求、WebSocket 建连或进程执行。

本机配置管理与本机页面的只读边界保持不变。manageService 的配置写入入口是本地 CLI，不开放给任意远端中心。

## 相关设计

[Client 总览](client-main.md) · [本地 harnessService](client-harness.md) · [Server 总览](server-main.md) · [deviceService](server-device.md) · [runService](server-run.md)
