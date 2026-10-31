# deviceService：执行设备

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。

## 职责

device 是一台用户的执行设备，client 是设备上运行的程序。deviceService 管登记、凭据、连接状态和执行能力，不管理对话或 run 的业务终态。

- 登记设备、生成一次性 setup token，注册时换取长期 device key。
- 保存 client 上报的可用 harness、版本和协议能力。
- 维护 online/offline 和最近活动时间，管理某个 harness 是否允许领取任务。
- 删除/重置设备时协调终止相关 run，再撤销凭据、断开旧连接。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 登记/注册 | 在事务内消费 setup_token，生成长期密钥哈希，禁止重复注册 |
| 上线 | 验证设备密钥；替换 capabilities，更新 harness 可用情况；不把 available 自动等同于 enabled |
| 心跳/离线 | 更新最近活动时间和连接状态；在线连接对象由连接管理组件维护 |
| 开关 harness | 修改 enabled；重新开启后通知派活 |
| 删除/重置 | 调用 runService 终止相关任务并协调撤销 work 凭据，然后提交凭据变更，再关闭连接 |

agentService 读取设备归属和运行能力，runService 根据设备选择可领取任务。harnessService 提供已登记的 harness 定义；未知名字不直接当成可执行配置使用。

状态为 pending（未注册）、offline、online。在线状态表示连接状态，不代表设备当前空闲；是否可以领取由 client 的 ready 和 runService 的领取规则共同决定。

## 表结构

### devices

设备主表。`user_id` 是归属用户，`setup_token` 只供注册使用，`key_hash` 是长期凭据哈希。注册后清除 setup_token；重置时废除旧 key_hash。

```sql
CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
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

设备与 harness 的组合。`available` 表示本机报告可用，`enabled` 表示用户允许使用，二者都满足才可派活。`user_id` 是设备所有者的冗余字段，由 service 保证一致。

```sql
CREATE TABLE device_harnesses (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    harness TEXT NOT NULL REFERENCES harnesses(name) ON DELETE CASCADE,
    version TEXT,
    available INTEGER NOT NULL DEFAULT 1,
    enabled INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, harness)
);
CREATE INDEX device_harnesses_user_harness
    ON device_harnesses(user_id, harness);
```

### device_capabilities

设备支持的协议能力集合，例如多 agent run、附件。每次上线使用本次上报替换旧集合，不把 harness 名称混入 capabilities。

```sql
CREATE TABLE device_capabilities (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    capability TEXT NOT NULL,
    PRIMARY KEY(device_id,capability)
);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [agentService](agent.md) · [harnessService](harness.md) · [sessionService](session.md) · [runService](run.md) · [attachmentService](attachment.md) · [workService](work.md)
