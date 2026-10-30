# works

工作：协调者 + 线程 + 共用说明。表结构见 [tables/works](../tables/works.md),规则见 [designs/work](../designs/work.md)。

普通用户只看得到自己的工作,admin 看全部。看不到返回 404。

## work 对象

```json
{
  "id": "wk_3a1f9c0e2b7d4c55",
  "user_id": "u_10086",
  "name": "重构 daemon",
  "instructions": "代码在 memory-co/agentuse,改完跑 go test ./...",
  "coordinator_id": "ss_9d0c…",
  "thread_count": 3,
  "created_at": 1792800000,
  "updated_at": 1792800105
}
```

## GET /api/works

列出工作，按 `updated_at` 新的在前。

## POST /api/works

```json
{ "name": "重构 daemon", "agent_id": "code-helper", "instructions": "可省略" }
```

- `name` 必填，空或超过 64 个字返回 400。
- `agent_id` 是协调者用的 agent,必须是调用者自己的(和线程一样，别人的返回 400);其余规则同开会话([sessions](sessions.md#post-apisessions)):不存在、没有运行时、harness 不可用都返回 400。
- 同时建出协调者会话(`idle`,没有消息),返回 201 和 `{"work": …, "coordinator": …}`。之后给协调者发消息用 `POST /api/sessions/{coordinator_id}/messages`。

## GET /api/works/{id}

返回 work 对象。线程用 `GET /api/sessions?work_id=wk_…` 取([sessions](sessions.md)),协调者用 `GET /api/sessions/{coordinator_id}`。

## PATCH /api/works/{id}

```json
{ "name": "新名字", "instructions": "新说明" }
```

只改传了的字段，返回 work 对象。改了说明，下一条消息就用新的。

## DELETE /api/works/{id}

连协调者和所有线程一起删，返回 204。有会话在 `running` / `waiting` 时返回 409。

## 工作令牌(`wt_`)

协调者跑的时候,daemon 收到的派活消息里带 `work_token`([apis/daemon](daemon.md)),作为环境变量 `AGENTUSE_WORK` 给 harness 进程，本机的 `agentuse work` 命令拿它调接口。

- 身份是工作的主人，只在这条消息还没结束时有效。
- 只能调这些，别的一律 403:`GET /api/agents`、`GET /api/sessions?work_id=<本工作>`、`POST /api/sessions`(`work_id` 必须是本工作，开的就是线程;`agent_id` 必须是工作主人自己的 agent,哪台 daemon 上的都行，别人的 403)、`GET /api/sessions/{线程}`、`POST /api/sessions/{线程}/messages`、`POST /api/sessions/{线程}/resolve`。
- 用工作令牌时 `GET /api/sessions`、`POST /api/sessions` 不带 `work_id` 就是本工作;令牌过期返回 401。

## 附件权限扩展

`wt_` 可对本工作的协调者和线程调用 [attachments](attachments.md) 的列表、上传、元数据、下载和删除接口，越出本工作返回 403。有效期仍是协调者当前消息的执行期。创建线程可用 `defer_start` 先建空会话、上传文件再发送第一条消息；工作删除时附件一起回收。详见 [attachment 设计](../designs/attachment.md)。

## multi_agent 工作

创建时可传 `mode: "multi_agent"`，agent_id 作为工作上的 coordinator_agent_id，协调 session 本身不绑定 agent。工作页消息显式选中协调 agent，也可 @ 其他自己的 agent。PATCH 支持 coordinator_agent_id；只允许自己的、支持新协议的有效 agent，协调 session 忙时返回 409。

工作令牌只在协调 run/attempt 的有效租约内可用。新线程也使用共享 session，多目标消息在所有目标得到最终结果后向协调者汇总一次；插话未送达改挂后续 run 时继续等待，不提前通知。自动通知显式路由到工作的协调 agent。工作里有未结束的新模式 run 时，不能删除整组工作。
