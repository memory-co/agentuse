# daemon

daemon 和服务之间的接口：先用安装令牌注册，再用长期密钥开一条 WebSocket。流程和派任务规则见 [designs/connect](../designs/connect.md),代码里的定义在 `internal/protocol`。

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

## GET /api/daemon/ws

WebSocket 长连接，握手时带 `Authorization: Bearer dk_…`。密钥无效时不升级，直接返回 401(daemon 收到 401 就不再重连，提示重新跑安装命令)。

同一台 daemon 再连一次，旧连接会被踢掉。连接断开后 daemon 退避重连(1 秒起，翻倍，最多 1 分钟)。

每条消息是一个 JSON 对象，用 `type` 区分，只带该类型用到的字段。

### daemon → 服务

| type | 字段 | 说明 |
|---|---|---|
| `hello` | `agents`、`running`、`version` | 连上后第一条 |
| `ready` | | 空闲，可以接活 |
| `heartbeat` | `task_id`(可空) | 每 15 秒一次 |
| `event` | `task_id`、`kind`、`payload` | 回报进度或结果 |

**hello**:上报本机可用的 agent 和手上还在跑的任务。服务据此更新 daemon_agents 的 `available`,并把挂在这台 daemon 名下、但不在 `running` 里的任务收回重排。

```json
{
  "type": "hello",
  "version": "v0.2.0",
  "agents": [{ "name": "claude", "version": "2.1.310 (Claude Code)" }, { "name": "my-harness" }],
  "running": ["tk_…"]
}
```

**ready**:`{"type": "ready"}`。服务收到后给它领一个任务;没有就记住它空闲，等有任务时再推。

**heartbeat**:`{"type": "heartbeat", "task_id": "tk_…"}`。带 `task_id` 时给任务续租(租约 60 秒);空闲时不带，只更新 `last_seen_at`。

**event**:

```json
{ "type": "event", "task_id": "tk_…", "kind": "progress", "payload": { "text": "🔧 Bash {\"command\":\"ls\"}" } }
{ "type": "event", "task_id": "tk_…", "kind": "done", "payload": { "ok": true, "output": "…" } }
{ "type": "event", "task_id": "tk_…", "kind": "failed", "payload": { "ok": false, "error": "…" } }
```

每条都写进 task_events 并续租;`done` / `failed` 同时结束任务。daemon 发完结果再发一次 `ready`。

### 服务 → daemon

| type | 字段 | 说明 |
|---|---|---|
| `welcome` | `daemon_id`、`name`、`ignored` | 回应 hello |
| `task` | `task` | 派一个任务 |
| `error` | `task_id`(可空)、`error` | 出错 |

**welcome**:`ignored` 是 hello 里报了、但服务上没登记的 agent 名字(被忽略)。

```json
{ "type": "welcome", "daemon_id": "dm_…", "name": "my-laptop", "ignored": ["ghost"] }
```

**task**:

```json
{ "type": "task", "task": { "id": "tk_…", "agent": "claude", "input": { "prompt": "…", "cwd": "~/code/x" } } }
```

**error**:带 `task_id` 表示这个任务已经不归你了(租约过期被收回、或已经结束),daemon 应该停掉它、不再回报。不带 `task_id` 是普通错误，比如未知消息类型。

```json
{ "type": "error", "task_id": "tk_…", "error": "store: task is not running on this daemon" }
```

## 本机配置

`~/.agentuse/daemon.json`,注册时自动写入 `server`、`daemon_id`、`key`,其余手动加:

| 字段 | 说明 |
|---|---|
| `server` | 服务地址 |
| `daemon_id` / `key` | 注册得到的 id 和密钥 |
| `workdir` | 没给 `cwd` 的任务在哪跑，默认 `~/.agentuse/work`(每个任务一个子目录) |
| `agents.<name>.command` | 启动命令;内置的默认 `claude` / `codex`,自定义的必填 |
| `agents.<name>.args` | 追加参数 |
| `agents.<name>.disabled` | `true` 时不上报 |

各 agent 怎么被调用见 [designs/connect](../designs/connect.md#本机怎么跑-agent)。改完重启 daemon 生效。
