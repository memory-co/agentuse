# Client endpointService：已注册的 Server

> v3 目标设计。endpointService 属于 client，只管理本机 SQLite 的 endpoints 表，记录 client 向哪些 server 注册了。不负责连接、领任务、执行调度或本机容量。

## 一、职责

- 保存每个已注册 server 的地址、名称、device 身份和设备凭据。
- 提供 endpoint 的新增、查询、修改、启用/停用和移除操作。
- 向 client runService 提供已提交的注册信息。

注册 HTTP 由 CLI 的注册流程完成，成功取得 device_id、device_key 后交给 endpointService 保存。每行表示一份已注册身份，不把未完成注册的临时状态写成可用 endpoint。

一个 client 可以向多个 server 注册，相同电脑在各 server 中的 device_id 和权限可以不同。endpoint_id 是本机生成的 UUID，不是 server 的设备 ID。修改显示名称不改变 ID；换成另一套 server 时新增 endpoint，不能接管原执行的命名空间。

## 二、表结构

只拥有 endpoints 一张表。时间字段使用 Unix 毫秒，不声明数据库外键；设备密钥与地址保存在本机 SQLite，不再维护中心配置 JSON。

```sql
CREATE TABLE endpoints (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    server_url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    device_id TEXT NOT NULL,
    device_key TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX endpoints_enabled ON endpoints(enabled,id);
```

| 字段 | 含义 |
|---|---|
| id | 本机 endpoint UUID，其他本地表用 endpoint_id 关联 |
| name | 用户识别这份注册的名称 |
| server_url | 该 server 的业务地址 |
| enabled | 是否允许 runService 使用该注册领取新任务 |
| device_id | 当前 client 在该 server 中的设备身份 |
| device_key | 该 server 签发的设备凭据，不与其他 endpoint 共用 |
| created_at / updated_at | 本机创建与修改时间 |

表不保存在线状态、心跳、重连、领取授权、执行槽位或 run 状态。这些都属于 runService 的运行时管理；共享容量配置属于 runService 的 run_settings 表。

## 三、与 runService 的关系

```text
CLI 注册 / 配置 → endpointService → endpoints
                                      ↑
                              runService 读取
                                      ↓
                         连接各 Server，领取并执行 Run
```

runService 读取启用的 endpoints，自己建立每个 server 的连接、上报能力、续租、重连、对账和领取。endpointService 不向 runService 派活，也不为每个 endpoint 创建主循环。

配置提交后可以发送变更通知，让 runService 重读列表；启动及定期核对也以 SQLite 为准，不依赖一次通知。endpointService 发出的只是“配置已变化”，不是连接或执行命令。

## 四、停用、删除与历史

停用只修改 enabled，runService 据此停止新领取，保留已有执行所需的连接、续租和结果回报直到收尾。endpointService 不直接取消 run 或关闭原生 session。

正常移除流程由 CLI 先请求 runService 停领并收尾，再调用 endpointService 删除记录。若直接移除配置，runService 仍用已绑定执行上下文完成收尾，不把任务改投其他 endpoint；重启后没有对应凭据则由 server 的租约机制恢复，不能用另一个 server 的身份代替。

本机 sessions/runs 保留 endpoint_id，不与 endpoints 声明外键。删除注册不删除历史账本或原生会话；重新注册建立新的本机 endpoint UUID，不自动复用旧记录。

配置存储接口仅提供给受信任的 CLI 与执行服务。本机只读 Reader 不暴露 device_key；CLI 状态输出不打印设备密钥，数据库及备份限制为当前系统用户访问。

## 相关设计

[Client 总览](client-main.md) · [client runService](client-run.md) · [client sessionService](client-session.md) · [Server deviceService](server-device.md)
