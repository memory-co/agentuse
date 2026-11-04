# runService：输入分配与执行事实

完整 WebSocket 报文、字段和恢复规则见 [protocol.md](protocol.md)。

Server 分配输入，client 执行并报告事实。**run_id 只由适配器的 accepted 事件产生**。Server 不生成执行 UUID，不创建 attempt、租约或原生执行状态，也不推断一次输入应该 start、steer 还是 queue。

## 消息、输入与 Run

一条消息 @ 两个 agent，先写两条 run_prompts，此时 run_id 为空，HTTP 返回 deliveries，runs 可以为空。领取成功表示输入已交付，不等于适配器已接受。

```text
M1 @coder @reviewer → P1、P2（pending）
client 领取 P1 → P1.sent
accepted(run_id=R1,input_id=I1) → 建 R1，P1 绑定 R1/I1
M2 @coder → P3 → client 再次 send
accepted(run_id=R1,input_id=I2) → P3 也绑定 R1：真实插话
或 accepted(run_id=R3,input_id=I2) → 建 R3：真实排队
```

一个 message 对多个 run，一个 run 可以消费多条 message。关系以 accepted 为准，不根据“上一个 run 尚未结束”提前合并。first_message_id 来自该 UUID 第一条 accepted 对应的消息。输入分配 ID 只用于传输关联，不是另一个执行身份。

## Agent 版本化扩展（目标，尚未实现）

当前实际 SQL 仍见后文；新增版本字段不能视为已经部署。版本定义见 [agentService](server-agent.md)。

| 数据 | 目标新增关联 | 固定时机 |
|---|---|---|
| run_prompts | agent_version_id | 保存消息并为目标建立输入时，与消息在同一事务固定当前版本 |
| runs | agent_version_id | 第一条 accepted 从对应 prompt 复制；同 run 后续输入只能来自相同版本 |

两列均为非空版本 UUID，不存可变化的 latest，不冗余名称或版本配置。Agent 版本指向固定 device/harness/model/指令；agentService 拥有关联，skillService 提供其固定 skill_versions 的元数据和下载授权。显示版本号时关联 agent_versions 查询。

Claim 使用固定版本生成 dispatch，不在领取时再查当前 head。重连仍重发原 dispatch。下行增加 agent_version_id、model、agent_instructions 和 skill 包版本清单，详情见 [protocol.md 的目标扩展](protocol.md#agent-versioning)。Server 只准备版本化业务资源，不生成 harness 命令。

Client 活动流必须按 conversation/agent/agent_version 匹配；容量不足时，新版本不能因为旧 agent 仍在执行就被当成可插话输入。新版使用新 session/events 并 init，旧版本可以继续完成。同一个 native run UUID 不能接收跨 agent_version 的输入；server 校验业务版本归属，不替适配器决定实际 run UUID。

最终 agent 回复同时记录 author_agent_id 与 author_agent_version_id，仍通过 reply_to_message_id 回复首条 message，messages 不增加 run_id。具体目标消息结构见 [conversationService](server-conversation.md)。

## 与其他 Service 的关系

conversationService 保存共享历史并建立输入分配；agentService 提供执行设备和 harness 标签；skillService 管独立的技能包版本；deviceService 管连接和上报能力。runService 在领取时准备共享历史、附件引用和业务指令。workService 提供工作说明、协调规则和授权。所有这些都是 server 业务，client 不推断角色。

Client 的 harnessService 管适配器 YAML、进程、原生协议、hook、skill、自动授权及插话策略。Server 不导入这些实现，不保存全局 harness 配置。设备上报的名称、版本、revision 用作选择和历史展示。

## WebSocket 协议

版本为 harnessctl-events-v1，旧协议不能接入。hello 报告可用 harness；claim 带空闲 session 数和仍可接收输入的 conversation/agent，server 返回 prompt 或 no_task。

下行 prompt 包含分配 ID、conversation、message、agent、harness 标签、准备好的文本、共享上下文及附件引用；不含原生命令或 attempt。补充输入仍是 prompt。reply 携带 request_id 与不透明 JSON answer；cancel 针对 client session，并携带目标 run_id 防止延迟取消作用于后续交互。

上行 event 包含 conversation、agent、client session、实际 harness/revision、源 JSONL 行结束字节偏移，以及完整公共事件。accepted 额外关联 prompt_id。Server 原样保存公共事件，不把原生工具帧转换成业务类型。

| 公共类型 | Server 行为 |
|---|---|
| accepted | 验证输入分配归属，按适配器 UUID 建 run，绑定 input_id |
| message | 保存正文事件，供执行详情读取 |
| request | 保存完整请求快照及 pending/replied/expired 状态；不解释原生权限工具 |
| result | 保存 completed/failed/cancelled、input_ids、输出、错误、用量和费用原文；投影 run 与输入终态 |
| log | 保存可观测事件；允许在 accepted 之前出现，不据日志猜测状态 |

请求答案由 client/适配器解释。控制命令送达确认不等于 request 已 replied；必须等待 client 的 request 事件。取消 HTTP 成功只表示已排入控制队列，run 保持原状，直到 client 报告 result。

Server 校验身份、路由、事件格式、UUID、输入集合及重复记录一致性；不接受跨设备、跨 agent 或重新绑定输入。结果的 input_ids 必须覆盖该 run 已接受的输入。执行输出及终态以 client 为准。

## 断线、恢复与回复

Client 以 events.jsonl 为持久化 outbox，一次发送完整行，只有 server 提交后收到 event_ack 才推进源字节游标。Server 以 device/client_session/event_seq 唯一键处理重放；同偏移不同内容拒绝。结果重放不会重复生成 assistant 消息或 Work 汇报。

断线只改变设备在线状态，不补造 failed/cancelled，不重新分配正在执行的输入。首次领取保存准备好的 dispatch 快照，重连不重新组织上下文或签发令牌。重连重送相同分配 ID 与未确认控制命令；client 使用输入收据去重，绝不因重连重新 send。未被适配器接受的失败以 prompt_error 报告，只失败该输入，不伪造 run。失败通知同样等待 server 提交确认；确认前保留输入收据和所属目录。

原生 result 与 assistant 回复在同一事务内保存。回复通过 reply_to_message_id 指向 first_message_id，messages 不以 run_id 建关系。run.result_message_id 只是内部去重关联。cancelled 不新增 assistant 回复。

## 表结构

以下为当前执行层实际 SQL。物理 session_id 指 server conversation，daemon_id 指 device；公共 API 的 /sessions、/daemons 及旧元数据命名尚未整体更名。client_session_id 才是本地原生会话。message_id 目前仍为 INTEGER；消息 UUID 和文件存储改造属于独立的元数据设计，不能把本文当作已经完成它们。新执行表没有数据库外键。

```sql
-- A prompt is a server-side message delivery, not an execution.
CREATE TABLE IF NOT EXISTS run_prompts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL, message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL, harness TEXT NOT NULL,
 content TEXT NOT NULL, dispatch TEXT, status TEXT NOT NULL DEFAULT 'pending',
 run_id TEXT NOT NULL DEFAULT '', input_id TEXT NOT NULL DEFAULT '',
 client_session_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(message_id,agent_id)
);
CREATE INDEX IF NOT EXISTS prompts_dispatch ON run_prompts(daemon_id,status,id);
CREATE INDEX IF NOT EXISTS prompts_run ON run_prompts(run_id,input_id);
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, first_message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id INTEGER,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_conversation ON runs(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
 created_at INTEGER NOT NULL, UNIQUE(daemon_id,client_session_id,event_seq)
);
CREATE TABLE IF NOT EXISTS run_requests (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, run_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 payload TEXT NOT NULL, state TEXT NOT NULL, answer TEXT,
 answered_by TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_controls (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 run_id TEXT NOT NULL, daemon_id TEXT NOT NULL, kind TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
 answer TEXT, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS work_run_tokens (
 token_hash TEXT PRIMARY KEY, work_id TEXT NOT NULL, prompt_id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_notifications (message_id INTEGER PRIMARY KEY);
```

run_prompts 状态为 pending/sent/accepted/completed/failed/cancelled；runs 只有真实执行的 running/waiting/completed/failed/cancelled。run_events.payload 和 runs.result 保存公共事件原文。run_requests 保存最新公共请求快照；run_controls 是待传输控制命令，不是执行事件。

work_run_tokens 绑定输入分配，而不是尚不存在的 run；有效性由输入状态检查。run_notifications 保证同一线程消息只汇报一次。

## 升级

数据库 schema 版本为 2。旧执行表和旧 WebSocket 协议不兼容；现有数据库会被拒绝启动，需按部署流程备份后使用新数据库，并同步更新 client。代码不会在启动时自动清空历史或重放旧输入。
