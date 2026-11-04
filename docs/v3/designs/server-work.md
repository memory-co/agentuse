# workService：工作、协调与线程汇总

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。表之间只保留关联字段，不声明数据库外键；引用校验和关联清理由 service 在事务内完成。

## 职责

workService 将多个 conversation 组织成一项工作。一个 work 有一个协调 conversation、一个协调 agent 和若干线程 conversation。它复用 conversationService/runService 的对话与任务模型，不引入另一种执行实体。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 创建 work | 通过 agentService 校验协调 agent，通过 conversationService 创建协调 conversation；在同一事务建立关联 |
| 创建线程 | 以工作范围权限调用 conversationService 创建关联 work_id 的 conversation |
| 修改配置 | 维护名称、说明和协调 agent；忙碌时拒绝不安全的执行选择变更 |
| 汇总结果 | 在最终回复已由 conversationService 保存后，检查原消息给所有目标的 run_prompts 是否完成；将汇总作为消息发到协调 conversation |
| 删除 work | 检查相关任务空闲，再经 conversationService 删除所属对话及 work 数据 |

汇总使用 run_notifications 去重：记录“该线程消息已汇报”与向协调 conversation 发汇总消息，必须在同一事务完成。不能只收到 run 结束事件就汇总，因为最终回复可能还没消费入库；需要等回复落库通知，并能通过持久化消息/run_prompts 扫描补偿。

## Agent 版本衔接（目标）

Work 仍选择稳定的 coordinator_agent_id。每条协调输入和线程输入在入队时固定目标 agent_version_id；发布新 agent 或 skill 版本不改已有排队输入，也不自动改写正在工作的协调原生上下文。下一条使用新版的输入绑定新的 client session 并执行 init，workspace 和 server 共享历史仍属于原 conversation。

Work 说明和协调者业务规则仍由 server 随输入准备，不混入可变的 agent 初始化快照。Skill 依赖来自该输入固定的 agent_version；WorkToken 仅授权对应输入及其引用的资源，不授予任意技能库访问权。

## 工作令牌

领取输入时签发限定 work/prompt 的令牌；此时实际 run 可能还不存在。每次使用检查输入状态为 sent 或 accepted，以及 work 范围。输入完成后授权失效，不把令牌当作长期用户身份。

协调者职责、派活规则及工作说明由 server 在领取输入时组织到 Instructions，再随任务下发。只有协调 conversation 当前指定的协调 agent 获得协调者指令；线程和其他 agent 只收到共享工作说明。client 不根据 WorkToken 推断角色或生成协调策略，只透传凭据和已准备的指令。

`coordinator_id` 是协调 conversation 的 UUID；`conversations.work_id` 是对话归属。两者仅以标识关联，由 workService 在创建/删除事务里保证一致，不声明外键。

## 表结构

### works

工作名称、说明、所有者、协调 conversation 和协调 agent。协调 agent 被删除时，由 agentService 协调 workService 清空 coordinator_agent_id；重新配置之前不能继续派协调任务。

```sql
CREATE TABLE works (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    coordinator_id TEXT NOT NULL UNIQUE,
    coordinator_agent_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX works_user
    ON works(user_id, updated_at);
```

### work_run_tokens

令牌只存哈希，绑定 work 和输入分配 prompt_id；实际有效性由 run_prompts.status 检查。

```sql
CREATE TABLE work_run_tokens (
    token_hash TEXT PRIMARY KEY,
    work_id TEXT NOT NULL,
    prompt_id INTEGER NOT NULL
);
```

### run_notifications

已汇总的线程 user message_id。按消息而不是 run 去重，因为一条消息可以有多个目标，也可能经历插话回退后的后续 run。

```sql
CREATE TABLE run_notifications (
    message_id TEXT NOT NULL PRIMARY KEY
);
```

## 相关设计

[Server 总览](server-main.md) · [userService](server-user.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [conversationService](server-conversation.md) · [runService](server-run.md) · [fileService](server-file.md)
