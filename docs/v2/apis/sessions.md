# sessions

会话和其中的消息。表结构见 [tables/sessions](../tables/sessions.md)、[tables/messages](../tables/messages.md)、[tables/session_events](../tables/session_events.md)。

## session 对象

```json
{
  "id": "ss_b83a324345de1f46",
  "user_id": "u_10086",
  "created_by": "u_10010",
  "agent_id": "code-helper",
  "work_id": "wk_3a1f9c0e2b7d4c55",
  "work_role": "thread",
  "harness": "claude",
  "daemon_id": "dm_5c1c8602536e55cb",
  "harness_session_id": "9f1c…",
  "title": "修一下 README 的错别字",
  "status": "idle",
  "created_at": 1792800040,
  "updated_at": 1792800105
}
```

- `status`:`idle` / `pending` / `running` / `waiting`(在等用户回答[提问](../designs/question.md)) / `resolved`(已完成)。
- `user_id` 是 agent 的主人，会话跑在他的 daemon 上;`created_by` 是发起人，用自己的 agent 时两者相同。
- `daemon_id`、`harness` 是建会话时 agent 的运行时，之后 agent 切换运行时它们也不变。
- `work_id` 是所属[工作](works.md),建的时候定，之后不能改;`work_role` 是 `coordinator` / `thread`,两个都是不在工作里时不出现。agent 主人看得到这两个字段，但打不开那个工作。
- `agent_id`、`work_id`、`daemon_id`、`harness_session_id`、`lease_until` 没有值时不出现(agent 删除后 `agent_id` 就没了)。

## message 对象

```json
{ "id": 3, "session_id": "ss_…", "role": "user", "content": { "prompt": "再把 CHANGELOG 也改一下" }, "created_at": 1792800090 }
{ "id": 4, "session_id": "ss_…", "role": "assistant", "content": { "ok": true, "output": "改好了", "session_id": "9f1c…", "cost_usd": 0.031 }, "created_at": 1792800105 }
```

## GET /api/sessions

最近 100 个会话，按 `updated_at` 新的在前。普通用户看自己 agent 上的和自己发起的,admin 看全部。

不带参数时只列不在工作里的会话。带 `?work_id=wk_…` 列这个工作的线程(不含协调者;工作看不到返回 404)。下面的接口也一样：主人和发起人都能看、回复、删除。

## POST /api/sessions

用一个 [agent](agents.md) 新建会话，同时发出第一条消息。两种写法:

```json
{ "agent_id": "code-helper", "prompt": "修一下 README 的错别字" }
```

```json
{ "agent_id": "my-bot", "input": { "任意": "JSON" } }
```

- `agent_id` 必填，可以是任何人的 agent,不存在返回 400;agent 没有运行时(它的 daemon 被删了)也返回 400。会话属于 agent 的主人,`created_by` 是调用者;`daemon_id` 和 `harness` 取 agent 此刻的运行时。
- `work_id` 只有[工作令牌](works.md)能用：协调者在自己的工作里开线程(不带就是本工作，别的工作 403)。用户令牌带 `work_id` 返回 403,线程只能由协调者开。建好后不能再改。协调者随工作一起建([works](works.md)),不走这里。
- 不能指定工作目录,daemon 固定用 `~/.agentuse/work/<session_id>/`。
- 给 `prompt` 时消息内容是 `{"prompt": …}`;给 `input` 时原样交给 harness,忽略 `prompt`。
- agent 运行时上的 harness 没开启、或那台 daemon 最近一次连上时没检测到它，返回 400 并说明原因，不会排队。
- 返回 201 和 `{"session": …, "message": …}`,会话为 `pending`。只派给 agent 运行时的那台 daemon;daemon 离线就排队等它上线。
- 排队中的会话，如果它的 harness 之后被关掉、或 daemon 重连时没再上报它，会话直接失败：回到 `idle`,追加一条 `{"ok": false, "error": "原因"}` 的 assistant 消息。

## POST /api/sessions/{id}/messages

回复：在会话里发下一条消息。

```json
{ "prompt": "再把 CHANGELOG 也改一下" }
```

或 `{"input": {…}}`,规则同上。

- 会话的 daemon 已被删除返回 400(harness 会话在原来那台电脑上，续不上)。
- `idle` / `resolved` 时，会话的 harness 没开启或不可用，返回 400 并说明原因。
- `idle` / `resolved` 时返回 201 和 message 对象;`resolved` 的会话就此重新打开。只派给这个会话的 daemon,它离线就等它上线。
- `pending` / `running` / `waiting` 时是[插话](../designs/steer.md)：返回 202 和 steer 对象。插话只收 `prompt`,带 `input` 返回 409。

```json
{ "id": 7, "session_id": "ss_…", "message_id": 3, "created_by": "u_10010", "prompt": "顺便把 CHANGELOG 也改了", "status": "pending", "created_at": 1792800050 }
```

`status`:`pending` 还没送达;`delivered` 已经塞给 harness;`queued` 没来得及送，已经转成下一条消息。

## GET /api/sessions/{id}

```json
{
  "session": { "id": "ss_…", "status": "idle", "…": "…" },
  "messages": [ { "id": 1, "role": "user", "…": "…" }, { "id": 2, "role": "assistant", "…": "…" } ],
  "steers": [
    { "id": 7, "session_id": "ss_…", "message_id": 1, "created_by": "u_10010", "prompt": "…", "status": "delivered", "created_at": 1792800044 }
  ],
  "events": [
    { "id": 1, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "progress", "payload": { "text": "…" }, "created_at": 1792800043 },
    { "id": 2, "session_id": "ss_…", "message_id": 1, "daemon_id": "dm_…", "kind": "done", "payload": { "ok": true, "output": "…" }, "created_at": 1792800045 }
  ],
  "daemon_offline": true
}
```

- `daemon_offline`:会话 `pending`、它的 daemon 不在线时为 `true`(在等 daemon 上线),否则不出现。

还有 `questions`,对象见下。`messages`、`steers`、`questions`、`events` 都按时间顺序;它们的 `message_id` 是挂着的那条 `user` 消息。

## POST /api/sessions/{id}/questions/{qid}/answer

回答 harness 的[提问](../designs/question.md)。

```json
{ "answers": { "要用 Python 还是 Go？": "Go" } }
```

- 每个问题都要有答案，可以是选项的 `label`,也可以是自己写的话;多选用 `, ` 拼起来。缺了返回 400。
- 问题不是 `pending` 返回 409。
- 返回 202 和 question 对象。daemon 把答案送到后才变成 `answered`。

```json
{ "id": 5, "session_id": "ss_…", "message_id": 3, "tool_use_id": "toolu_…",
  "questions": [{ "question": "要用 Python 还是 Go？", "header": "语言", "multi_select": false,
                  "options": [{ "label": "Python", "description": "…" }, { "label": "Go", "description": "…" }] }],
  "answers": { "要用 Python 还是 Go？": "Go" }, "answered_by": "u_10010", "status": "pending", "created_at": 1792800050 }
```

`status`:`pending` 等回答(或答案还没送到);`answered` 已送到;`expired` 超时或进程没了。

## POST /api/sessions/{id}/resolve

标记为已完成：`idle` → `resolved`,返回 session 对象。不是 `idle`(`resolved` 除外，重复标记直接返回)返回 409;协调者返回 400。

## POST /api/sessions/{id}/reopen

取消已完成：`resolved` → `idle`,返回 session 对象。不是 `resolved` 返回 409。

## DELETE /api/sessions/{id}

删除会话和它的消息、回报，返回 204。`running` 时返回 409。协调者不能单独删(返回 400),要删整个工作。只删 agentuse 这边的记录，不动 daemon 本机上 harness 的会话文件。

## 附件扩展

会话下增加独立的 [attachments](attachments.md) 资源：`/api/sessions/{id}/attachments`，包含上传、列表、元数据、内容下载、删除。附件属于 session，不挂 message；session 详情暂不内嵌附件数组，单独分页获取。

为支持先上传输入文件，`POST /api/sessions` 增加可选 `defer_start: true`：不传 prompt/input，返回 `{"session": …}`（201），初始状态 `idle`，没有 message，也不派活。随后上传附件，再用 messages 接口发第一条消息。未指定该选项时行为保持不变。细则见 [attachment 设计](../designs/attachment.md#先建空会话)。

删除会话或整个工作时，附件元数据级联删除，二进制异步回收。下载到 daemon 的副本不做远程擦除。

## 多 agent 会话方案

新方案通过 `mode: "multi_agent"` 创建不绑定单个 agent 的会话，每条消息用 `target_agent_ids` 指定一个或多个目标。同一消息对空闲 agent 创建 run、对忙碌 agent 作为插话，统一通过 messages 接口提交。共享 session 内容、run、提问路由及旧接口兼容见 [Session 中 @ 多个 agent](../designs/session-mentions.md)。本文件上面的单 agent 接口是 legacy 行为，新模式见下方及设计文档。

### multi_agent 请求与响应

创建空会话：`POST /api/sessions` 发送 `{"mode":"multi_agent","title":"评审"}`，201 返回 session；也可同时带 prompt/input、target_agent_ids 原子创建首条消息。新模式不使用 agent_id 绑定。未传 mode 的旧请求按 legacy 处理。

```json
{"prompt":"分别看看实现与风险","target_agent_ids":["coder","reviewer"]}
```

将上面请求发到 `POST /api/sessions/{id}/messages`，201 返回 `{message, runs, deliveries}`。每个空闲目标新建 run；pending/running/waiting 目标关联为插话。runs 只包含对应的新建执行，deliveries 表示各目标当前的 run、kind（new_run/steer）、status 和 error；后续未送达插话可能改挂后续 run，应以 GET 详情里的 deliveries 跟踪。目标省略/空数组只保存笔记，不隐式调用上一个 agent。发送可带 Idempotency-Key；相同用户、session、key 和相同请求内容不重复派活，内容不同返回 409。

`GET /api/sessions/{id}` 增加 runs、deliveries、run_counts；消息增加 seq、agent_key、agent_name、run_id、target_agent_ids。seq 复用 message id，按递增顺序排列。session 不返回有效的单一 agent/runtime/resume 绑定。

- `GET /api/sessions/{id}/runs/{run_id}`：返回 `{run, events}`。
- `POST /api/sessions/{id}/runs/{run_id}/cancel`：取消当前执行和未送达补充，返回 run；重复取消幂等，已成功/失败返回 409。
- `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer`：按 run 回答提问，响应为 question，202。
- `POST /api/sessions/{id}/upgrade`：空闲 legacy 会话显式升级，工作内会话整组升级；未结束任务、插话或问题返回 409。旧消息、文件、插话/问答、访问权限保留，原生 resume 不再使用。

没有单独的 steers 写接口。更多状态、回退、重派规则见 [设计](../designs/session-mentions.md)。
