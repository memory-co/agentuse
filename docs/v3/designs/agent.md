# agentService：任务角色与执行选择

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

agent 是用户在会话中选择的任务角色。agentService 管名称、说明、归属和执行选择：哪个 device、哪个 harness。Agent 不拥有独立会话历史。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 创建/修改 agent | 检查用户权限、名称，以及 device/harness 的登记关系 |
| 查询执行选择 | 向 runService 提供当前 device、harness 和角色信息 |
| 删除 agent | 删除未来可选择的角色，已有 run 保留原 agent_id；读取时关联不到则显示已删除状态 |

userService 提供当前身份；deviceService 提供设备归属和能力；harnessService 提供执行配置。创建配置和当前能够执行是两个检查：runService 建立/领取任务时仍需检查运行时是否可用。

run 创建后保存本次 agent、device、harness 的选择。修改 agent 不改写已有 run；删除设备时由 deviceService 协调 agentService 清空 agent.device_id，之后该字段可以为空，必须重新配置才能继续使用。

## 表结构

### agents

`id` 是全局稳定标识，`name` 在同一用户下唯一。`device_id` 指定执行位置，`harness` 指定执行方式，`description` 描述角色。这里没有 session_id 或单独上下文。

```sql
CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    device_id TEXT,
    harness TEXT NOT NULL,
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX agents_user_name
    ON agents(user_id, name);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [harnessService](harness.md) · [sessionService](session.md) · [runService](run.md) · [fileService](file.md) · [workService](work.md)
