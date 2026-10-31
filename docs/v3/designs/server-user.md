# userService：用户与访问身份

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

userService 管理用户身份、角色和访问令牌。它回答“当前请求是谁、能以什么身份访问”，资源是否归这个用户访问，由对应业务 service 判断。

- 创建用户，维护 admin/user 角色。
- 签发、重置和校验用户令牌，只存哈希，明文只在签发时返回。
- 支持管理员明确代用户操作；设备凭据与工作令牌分别交给 deviceService、workService 校验。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 创建用户 | 校验 ID、名称和角色，插入用户并返回初始令牌 |
| 登录/鉴权 | 计算令牌哈希，查询有效用户，返回身份 |
| 重置令牌 | 替换 token_hash，旧令牌立即失效 |
| 管理员代办 | 验证 admin 权限后解析目标用户；后续操作以该身份进行 |

一个用户拥有多个 device、agent、session 和 work。userService 不直接清理这些关联业务；v3 第一版不提供删除用户操作，以免把资源删除策略混入身份管理。

## 表结构

### users

用户主表。`id` 是稳定身份，`name` 是唯一显示/登录名称，`role` 由 service 限定为 admin/user；`token_hash` 唯一。重置令牌不改变用户 ID，也不改变已有资源归属。

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'user',
    token_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX users_token
    ON users(token_hash);
```

## 相关设计

[Server 总览](server-main.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [Client harnessService](client-harness.md) · [sessionService](server-session.md) · [runService](server-run.md) · [fileService](server-file.md) · [workService](server-work.md)
