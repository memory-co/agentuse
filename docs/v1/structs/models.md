# 对象

字段和 [`internal/models/models.go`](../../../internal/models/models.go) 一一对应,JSON 名即字段名。

## Platform

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 平台 id,出现在 notify 路径里,如 `shop` |
| `secret` | string | 平台与 hub 约定的密钥;验 notify 用 |
| `callback_secret` | string | 可选:hub 签回调用的密钥;空 = 沿用 `secret` |
| `callback_url` | string | 平台挂回调的地址,如 `https://shop.example.com/agentuse/callback` |
| `policy` | [Policy](#policy) | 策略 |

## Policy

| 字段 | 类型 | 说明 |
|---|---|---|
| `agent_name` | string | 被 @ 的名字,如 `claude` |
| `agent_user_id` | string | agent 在平台里的应用用户 id;这个作者的评论不唤醒 |
| `wake_roles` | Role[] | 谁能唤醒 |
| `trusted_roles` | Role[] | 谁的评论作为带作者标注的材料;其余是不可信材料 |
| `actions` | Action[] | 运行令牌允许的动作 |
| `workspace_mode` | `readonly` \| `readwrite` | 现场权限 |

**Role**:`owner`(实例主人 / 站长)、`member`(工作区成员 / 维护者)、`guest`(陌生访客)。只来自平台签名。

**Action**:`reply`(写评论)、`status`(改线程对象的状态)、`label`(打标签,预留)。`activity` 不算动作,有效令牌都能发。

## Comment

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 平台里的评论 id |
| `thread` | string | 线程键 |
| `author` | Author | `{id, name, role}`,由平台签名担保 |
| `text` | string | 正文 |
| `created_at` | RFC 3339 | 可选 |

notify 的请求体是 Comment 加一个可选的 `context: Comment[]`(同一线程里的其它评论)。

## Material

| 字段 | 类型 | 说明 |
|---|---|---|
| `comment` | Comment | 线程里的一条其它评论 |
| `trusted` | bool | 作者是叫醒者本人,或角色在 `trusted_roles` 里 |

## Session

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `s_…` |
| `platform` | string | |
| `thread` | string | 线程键;`(platform, thread)` 唯一 |
| `agent_session_id` | string | agent 自己的会话 id,第一次运行结束后由 daemon 回填,之后 `--resume` |
| `branch` | string | 现场分支 |
| `created_at` / `updated_at` | RFC 3339 | |

## Run

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `r_…` |
| `session_id` | string | |
| `platform` / `thread` | string | 冗余自会话,写回时用 |
| `trigger` | Comment | 叫醒它的那条评论 |
| `context` | Material[] | 线程材料 |
| `actions` | Action[] | 建运行时从策略拷过来,之后改策略不影响已排队的运行 |
| `workspace_mode` | string | 同上 |
| `status` | `queued` \| `claimed` \| `succeeded` \| `failed` | |
| `attempts` | int | 被领取过几次;每次领取 +1,租约过期回收后再领取继续加 |
| `daemon_id` | string | 当前持有它的 daemon(回收后清空) |
| `lease_until` | RFC 3339 | claimed 时的租约到期时间;心跳续期,过期即回收 |
| `error` | string | 失败原因;租约用完是 `lease expired` |
| `created_at` / `claimed_at` / `finished_at` | RFC 3339 | |

## RunToken

`aut_` + base64url(claims JSON) + `.` + base64url(HMAC-SHA256(token_key, claims 段))。claims:

| 字段 | 说明 |
|---|---|
| `run` | 运行 id |
| `attempt` | 第几次领取;和运行当前的 `attempts` 不一致就拒绝写回 |
| `platform` / `thread` | 范围 |
| `actions` | 允许的动作 |
| `exp` | 过期时间(unix 秒),最长签发后 24 小时 |

令牌自包含,不落盘;运行 complete 后 hub 拒绝它的写回(看运行状态)。

## Event

hub 回调平台的正文。

| 字段 | 类型 | 说明 |
|---|---|---|
| `type` | `reply` \| `status` \| `activity` | |
| `platform` / `thread` | string | 来自运行,不来自 agent |
| `run_id` | string | |
| `in_reply_to` | string | 叫醒评论的 id,平台可据此把回复挂成楼中楼 |
| `text` | string | `reply` 的正文 |
| `status` | string | `status` 的目标状态,如 `in_review`;取值由平台定 |
| `activity` | `{type, body}` | `activity` 的内容;type 为 `thought` / `action` / `elicitation` / `response` / `error`(沿用 Linear AgentSession) |
| `created_at` | RFC 3339 | |
