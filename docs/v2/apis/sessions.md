# Sessions API

Session 保存共享消息与附件，属于创建者，不绑定 agent、daemon 或 harness。所有会话使用 `multi_agent` 模型。

## POST /api/sessions

```json
{"title":"评审","prompt":"检查改动","target_agent_ids":["coder","reviewer"]}
```

title、prompt/input、target_agent_ids 都可省略，`{}` 创建空会话。mode 可省略；若提供，只接受 `multi_agent`。不接受 agent_id 绑定。工作线程只能使用对应工作令牌创建。成功返回 201：

```json
{"session":{"id":"ss_xxx","mode":"multi_agent","status":"pending"},"message":{"id":1},"runs":[],"deliveries":[]}
```

空会话只返回 session；带首条消息时同时返回实际的 message、runs 和 deliveries。

## POST /api/sessions/{id}/messages

```json
{"prompt":"补充要求","target_agent_ids":["coder","reviewer"]}
```

每条消息最多 8 个目标。对空闲 agent 创建 run，对已有 pending/running/waiting run 的 agent 创建 steer 投递。没有目标只保存笔记，不隐式选取 agent。JSON input 作为独立任务处理。消息与全部目标路由原子提交。

成功返回 201 `{message,runs,deliveries}`。`Idempotency-Key` 相同且内容相同返回已有结果；相同 key 不同内容返回 409。目标不存在或运行时不可用返回 400。

## GET /api/sessions

列普通会话；带 `work_id` 列指定工作的线程。工作令牌只能列本工作。

## GET /api/sessions/{id}

返回 `{session,messages,runs,deliveries,events,questions,run_counts}`。消息按 seq 排序，assistant 消息带 agent_key、agent_name、run_id。daemon 读取须携带当前 `X-Agentuse-Run` 和 `X-Agentuse-Attempt`。

## 执行

- `GET /api/sessions/{id}/runs/{run_id}`：返回 `{run,events}`。
- `POST /api/sessions/{id}/runs/{run_id}/cancel`：取消执行和关联投递。
- `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer`：提交 `{"answers":{"问题":"答案"}}`，返回 202。

插话统一发送消息，没有独立的 steers 写接口。

## 会话管理

- `DELETE /api/sessions/{id}`：删除会话、消息、执行与附件；有活动 run 返回 409，成功 204。
- `POST /api/sessions/{id}/resolve`：将 idle 标记为 resolved。
- `POST /api/sessions/{id}/reopen`：恢复为 idle。

协调者不能单独删除或标记完成。详见 [设计](../designs/session-mentions.md) 与 [附件 API](attachments.md)。
