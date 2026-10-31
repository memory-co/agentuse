# deviceService：执行设备

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

device 是一台用户的执行设备，client 是设备上运行的程序。deviceService 管登记、凭据、连接状态和执行能力，不管理对话或 run 的业务终态。

- 登记设备、生成一次性 setup token，注册时换取长期 device key。
- 保存 client 本地探测后上报的 harness、版本与业务能力，不下发探测命令。
- 维护 online/offline 和最近活动时间，管理某个 harness 是否允许领取任务。
- 删除/重置设备时协调终止相关 run，再撤销凭据、断开旧连接。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 登记/注册 | 在事务内消费 setup_token，生成长期密钥哈希，禁止重复注册 |
| 上线 | 验证设备密钥，更新 client 上报的 run 协议能力与本地 harness 探测结果，不把 available 自动等同于 enabled |
| 心跳/离线 | 更新最近活动时间和连接状态；在线连接对象由连接管理组件维护 |
| 开关 harness | 修改 enabled；重新开启后通知派活 |
| 删除/重置 | 调用 runService 终止相关任务并协调撤销 work 凭据，然后提交凭据变更，再关闭连接 |

agentService 读取设备归属和运行能力，runService 根据设备选择可领取任务。探测由 client 本地 harnessService 读取 YAML 完成；deviceService 保存上报的 harness 标识、接入包版本、程序版本及业务能力。Server 不保存全局 harnesses 定义或 YAML，也不向设备发送探测命令。

状态为 pending（未注册）、offline、online。在线状态表示连接状态，不代表设备当前空闲；是否可以领取由 client 的 ready 和 runService 的领取规则共同决定。

## 表结构

### devices

设备主表。`user_id` 是归属用户，`setup_token` 只供注册使用，`key_hash` 是长期凭据哈希。注册后清除 setup_token；重置时废除旧 key_hash。

```sql
CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    key_hash TEXT,
    setup_token TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    last_seen_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX devices_key
    ON devices(key_hash);
CREATE UNIQUE INDEX devices_setup_token
    ON devices(setup_token);
```

### device_harnesses

设备与 harness 的组合。`available` 表示 client 对本地接入包与程序的探测判定可用，`enabled` 表示用户允许使用，二者都满足才可派活。`user_id` 是设备所有者的冗余字段，由 service 保证一致。

```sql
CREATE TABLE device_harnesses (
    device_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    capabilities TEXT NOT NULL DEFAULT '[]',
    version TEXT,
    available INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, harness)
);
CREATE INDEX device_harnesses_user_harness
    ON device_harnesses(user_id, harness);
```

### device_capabilities

设备支持的 run 业务协议版本与能力，例如任务领取、标准事件、文件操作。每次上线以上报内容替换旧集合，不把进程流、JSON-RPC 或原生 hook 等 client 内部实现当成 server 指令集。

device_harnesses.harness_revision 是本地接入包版本，capabilities 是该 harness 的业务能力 JSON 数组，例如 steer/question/plan/usage。Client 升级接入包后重新探测并上报，不用 server 编译配置。

```sql
CREATE TABLE device_capabilities (
    device_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    PRIMARY KEY(device_id,capability)
);
```

## 相关设计

[Server 总览](server-main.md) · [userService](server-user.md) · [agentService](server-agent.md) · [Client harnessService](client-harness.md) · [sessionService](server-session.md) · [runService](server-run.md) · [fileService](server-file.md) · [workService](server-work.md)
