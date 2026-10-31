# harnessService：统一执行描述

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

harnessService 用 YAML config 描述不同 harness，把命令、参数、输入输出及交互差异收敛成 client 能理解的执行约定。Server 不运行用户设备上的命令。

| 配置内容 | client 的用途 |
|---|---|
| command、版本探测 | 找到程序，检查本机可用性 |
| 参数、环境、工作目录 | 准备执行环境并启动程序 |
| input / output | 将任务转换为输入，将输出转换为统一事件、结果、用量 |
| transport / steer / ask | 选择内置适配器，处理协议、插话和提问 |

YAML 描述配置，复杂协议由 client 适配器实现；不承诺任意程序只写 YAML 就能支持全部能力。

## 操作与协作

1. 导入内置 YAML 或接受自定义配置，检查格式及适配器标识，计算配置版本摘要。
2. 保存定义；deviceService 根据名字登记本机可用版本，agentService 选择 harness。
3. runService 建立任务时固定 `harness + harness_revision`。
4. client 领取任务时取得固定版本的配置正文（内联随任务提供），再按本机路径覆盖规则启动命令；不拿“最新同名配置”替换已经建立的任务配置。

为避免配置更新影响排队任务，run 保存执行配置快照。Client 不支持所需 transport 时拒绝执行并回报错误，不默默使用其他协议。

## 与现有结构的区别

v3 在 harnesses 中增加 `config_yaml / config_revision`，保存可分发的完整定义；revision 为配置内容摘要。内置文件是导入来源，数据库是 server 向 client 分发定义的依据。run 的配置快照见 runService 设计。

## 表结构

### harnesses

`name` 是唯一配置标识；`kind` 为 builtin/custom；`owner_id` 表示自定义配置归属，内置配置可为空。`config_yaml` 是完整正文，`config_revision` 是对应内容摘要；改定义时同时更新二者。name 在全局唯一，不是按用户分区。

```sql
CREATE TABLE harnesses (
    name TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT,
    owner_id TEXT,
    config_yaml TEXT NOT NULL,
    config_revision TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [sessionService](session.md) · [runService](run.md) · [attachmentService](attachment.md) · [workService](work.md)
