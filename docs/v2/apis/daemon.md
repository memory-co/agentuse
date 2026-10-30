# daemon

daemon 和服务之间的接口：先用安装令牌注册，再用长期密钥开一条 WebSocket。流程和派活规则见 [designs/connect](../designs/connect.md),代码里的定义在 `internal/protocol`。

## POST /api/daemon/register

用一次性安装令牌(`st_`)换长期密钥(`dk_`)。不需要登录。

```json
{ "token": "st_…", "hostname": "my-laptop" }
```

返回 200:

```json
{ "daemon_id": "dm_…", "name": "my-laptop", "key": "dk_…" }
```

- 页面上添加 daemon 时没填名字，就用 `hostname` 当名字。
- 令牌用过一次就作废;无效或已用过返回 401。
- `key` 只在这里出现一次，daemon 把它写进 `~/.agentuse/daemon.json`。

## GET /api/daemon/me

用密钥查自己，验证密钥是否还有效，带 `Authorization: Bearer dk_…`。不建连接，不影响正在跑的 daemon,`agentuse status` 用它。

```json
{ "daemon_id": "dm_…", "name": "my-laptop", "user_id": "u_10086", "status": "online" }
```

密钥无效返回 401。

## GET /api/daemon/ws

WebSocket 长连接，握手时带 `Authorization: Bearer dk_…`。密钥无效时不升级，直接返回 401(daemon 收到 401 就不再重连，提示重新跑安装命令)。

同一台 daemon 再连一次，旧连接会被踢掉。连接断开后 daemon 退避重连(1 秒起，翻倍，最多 1 分钟)。

每条消息是一个 JSON 对象，用 `type` 区分，只带该类型用到的字段。

### daemon → 服务

| type | 字段 | 说明 |
|---|---|---|
| `hello` | `harnesses`、`running`、`version` | 连上后第一条 |
| `ready` | | 空闲，可以接活 |
| `heartbeat` | `message_id`(可空) | 每 15 秒一次 |
| `event` | `message_id`、`kind`、`payload` | 回报进度或结果 |

**hello**:上报本机可用的 harness 和手上还在跑的消息(`message_id`)。服务据此更新 daemon_harnesses 的 `available`,并把挂在这台 daemon 名下、但不在 `running` 里的会话收回重排。

```json
{
  "type": "hello",
  "version": "v0.2.0",
  "harnesses": [{ "name": "claude", "version": "2.1.310 (Claude Code)" }, { "name": "my-harness" }],
  "running": [3]
}
```

**ready**:`{"type": "ready"}`。服务收到后给它领一个会话;没有就记住它空闲，等有活时再推。

**heartbeat**:`{"type": "heartbeat", "message_id": 3}`。带 `message_id` 时给它所在的会话续租(租约 60 秒);空闲时不带，只更新 `last_seen_at`。

**event**:

```json
{ "type": "event", "message_id": 3, "kind": "progress", "payload": { "text": "🔧 Bash {\"command\":\"ls\"}" } }
{ "type": "event", "message_id": 3, "kind": "plan", "payload": { "steps": [{ "text": "改 harness.yaml", "status": "in_progress" }] } }
{ "type": "event", "message_id": 3, "kind": "steered", "payload": { "steer_id": 7 } }
{ "type": "event", "message_id": 3, "kind": "question", "payload": { "tool_use_id": "toolu_…", "questions": [ … ] } }
{ "type": "event", "message_id": 3, "kind": "answered", "payload": { "tool_use_id": "toolu_…" } }
{ "type": "event", "message_id": 3, "kind": "question_expired", "payload": { "tool_use_id": "toolu_…" } }
{ "type": "event", "message_id": 3, "kind": "done", "payload": { "ok": true, "output": "…", "session_id": "9f1c…" } }
{ "type": "event", "message_id": 3, "kind": "failed", "payload": { "ok": false, "error": "…" } }
```

每条都写进 session_events 并续租;`plan` 每次带整份计划([设计](../designs/plan.md));`steered` 表示插话已经塞给 harness([设计](../designs/steer.md));`question` 让会话变成 `waiting`,`answered` / `question_expired` 让它回到 `running`([设计](../designs/question.md));`done` / `failed` 时 payload 记成一条 `assistant` 消息，会话回到 `idle`。`payload.session_id` 是 harness 自己的会话 id,服务存进 sessions.harness_session_id,下次派活时带回来。daemon 发完结果再发一次 `ready`。

### 服务 → daemon

| type | 字段 | 说明 |
|---|---|---|
| `welcome` | `daemon_id`、`name`、`ignored` | 回应 hello |
| `message` | `message` | 派一条用户消息 |
| `steer` | `steer` | 推一条插话:`{"id", "message_id", "prompt"}`;daemon 不在跑这条消息或 harness 不能插话时忽略 |
| `answer` | `answer` | 推一个提问的答案:`{"message_id", "tool_use_id", "answers"}`;daemon 已经不在等它时忽略 |
| `error` | `message_id`(可空)、`error` | 出错 |

**welcome**:`ignored` 是 hello 里报了、但服务上没登记的 harness 名字(被忽略)。

```json
{ "type": "welcome", "daemon_id": "dm_…", "name": "my-laptop", "ignored": ["ghost"] }
```

**message**:

```json
{
  "type": "message",
  "message": {
    "id": 3,
    "session_id": "ss_…",
    "harness": "claude",
    "resume": "9f1c…",
    "instructions": "代码在 memory-co/agentuse…",
    "work_token": "wt_…",
    "input": { "prompt": "…" }
  }
}
```

`input` 是这条消息的 content。`resume` 是会话的 harness_session_id,第一条或还没有时不出现。`instructions` 是会话所在[工作](works.md)的说明，不在工作里或说明为空时不出现;daemon 起进程前把它写进会话目录(claude 写 `CLAUDE.md`,codex 写 `AGENTS.md`,自定义 harness 放进 stdin),不出现就删掉那个文件([设计](../designs/work.md#说明怎么交给-harness))。`work_token` 只在会话是工作的协调者时出现,daemon 把它和服务地址作为 `AGENTUSE_WORK`、`AGENTUSE_SERVER` 环境变量给 harness 进程(PATH 最前面加上 agentuse 自己的目录),并在说明文件最前面放上协调者的规矩和 `agentuse work` 的用法([设计](../designs/work.md#协调者怎么开线程怎么收结果))。工作目录不下发,daemon 固定用 `~/.agentuse/work/<session_id>/`。

**error**:带 `message_id` 表示这条消息已经不归你了(租约过期被收回、或已经结束),daemon 应该停掉它、不再回报。不带 `message_id` 是普通错误，比如未知消息类型。

```json
{ "type": "error", "message_id": 3, "error": "store: message is not running on this daemon" }
```

## 本机配置

`~/.agentuse/daemon.json`,注册时自动写入 `server`、`daemon_id`、`key`,其余手动加:

| 字段 | 说明 |
|---|---|
| `server` | 服务地址 |
| `daemon_id` / `key` | 注册得到的 id 和密钥 |
| `harnesses.<name>.command` | 启动命令;内置的默认 `claude` / `codex`,自定义的必填 |
| `harnesses.<name>.args` | 追加参数 |
| `harnesses.<name>.disabled` | `true` 时不上报 |
| `ask_timeout` | 等用户回答提问的最长时间，默认 `30m`,超时让 harness 先结束这一轮 |

各 harness 怎么被调用见 [designs/connect](../designs/connect.md#本机怎么跑-harness)。改完重启 daemon 生效。

## 附件扩展

不通过 WebSocket 传文件，daemon 用现有 `dk_` 调 [attachments HTTP API](attachments.md)。可读取分配给自己的 session 的附件；上传须提供当前有效租约下正在执行的 `message_id`，不能删除附件。daemon 密钥对其他管理接口的权限保持不变。

每次启动 harness 前，取附件清单、下载并校验到会话工作目录的 `.agentuse/attachments/<id>/<filename>`。给 harness 增加附件上下文，以及 `AGENTUSE_SERVER`、`AGENTUSE_SESSION`、`AGENTUSE_MESSAGE_ID`、`AGENTUSE_CONFIG` 环境变量和 agentuse 的 PATH，供它调用 `agentuse attachment upload`。自定义 harness 的 stdin 运行上下文增加 `attachments` 数组，每项含 `id`、`filename`、`path`（本地相对路径）、`content_type`、`size_bytes`、`sha256`；用户原始 input 保持不变。完整流程、并发与失败规则见 [attachment 设计](../designs/attachment.md#daemon-和-harness)。

附件能力通过 hello 的可选 `capabilities: ["attachments-v1"]` 声明，缺省视为不支持；服务在 daemon 对象中返回最近一次的能力列表。不支持时附件上传返回 409，含附件的会话不派给旧 daemon，具体降级规则见 [升级兼容](../designs/attachment.md#升级兼容)。

## 多 agent 共享 session

hello 的 capabilities 增加 `session-runs-v1`。新派活 type 为 run，run 内的 id 为触发消息 id，另带 run_id、attempt_id、session_id、agent_id、task_message_ids、harness、context_seq、input；没有 resume。daemon 开始执行时经 HTTP 读取共享 session 和附件，写 `.agentuse/session-history.json`，每次启动新原生会话。同一 daemon 仍一次执行一个任务。

hello 的 active_runs / welcome 的 valid_runs 都是 `{run_id, attempt_id}` 数组，legacy 的 running 消息 id 数组保留。无效执行必须停止后才能 ready。heartbeat、event、内部 steer/answer 带 run_id/attempt_id；event 另带 event_seq 去重。type cancel 停止对应执行。

使用 dk_ 调 `GET /api/sessions/{id}` 和附件接口时，新模式还须带 `X-Agentuse-Run`、`X-Agentuse-Attempt`，服务验证本机当前执行及有效租约。终态、取消、过期代次不能继续读取或上传。工作协调令牌改为绑定当前 run/attempt，只有协调 agent 的执行拿得到。

详见 [session-mentions](../designs/session-mentions.md)。旧 daemon/legacy 接口保持兼容。
