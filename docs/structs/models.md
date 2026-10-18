# 对象

字段和 [`internal/models/models.go`](../../internal/models/models.go)、[`internal/models/accounts.go`](../../internal/models/accounts.go) 一一对应,JSON 名即字段名。

## Platform

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 平台 id,出现在 notify 路径里,如 `shop` |
| `secret` | string | 平台与 hub 约定的密钥;验 notify 和平台管理接口用 |
| `callback_secret` | string | 可选:hub 签回调用的密钥;空 = 沿用 `secret` |
| `callback_url` | string | 平台挂回调的地址,如 `https://shop.example.com/agentuse/callback` |
| `policy` | [Policy](#policy) | 策略 |

## Policy

按平台的策略,也是这个平台上**每个 agent 设置的上限**。

| 字段 | 类型 | 说明 |
|---|---|---|
| `wake_roles` | Role[] | `wake=members` 的 agent 还有谁能叫醒(主人总能叫醒) |
| `trusted_roles` | Role[] | 谁的评论作为带作者标注的材料;其余是不可信材料 |
| `actions` | Action[] | agent 最多能做的动作 |
| `workspace_mode` | `readonly` \| `readwrite` | 现场权限上限:`readonly`(或不配)时所有 agent 都只读 |
| `queue_ttl_seconds` | int | 运行排队等设备上线的最长时间,默认 `86400`(24 小时);过期判失败(`queue_ttl: device offline`)并回调一条 `error` 进度 |

agent 实际生效的权限(建运行时算,拷到 Run 上):

- **actions** = agent 的 `actions` ∩ 策略的 `actions`;agent 的为空 = 策略的全部。
- **workspace_mode** = `readwrite` 仅当策略是 `readwrite` 且 agent 没设成 `readonly`;否则 `readonly`。

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
| `trusted` | bool | 作者是叫醒者本人、是这个 agent 的主人,或角色在 `trusted_roles` 里 |

## User

平台里的一个人。hub 不管注册登录,只记平台给的 id 和显示名;建 agent 时记主人,notify 时记评论作者。

| 字段 | 类型 | 说明 |
|---|---|---|
| `platform` | string | |
| `id` | string | 平台自己的用户 id;`(platform, id)` 唯一 |
| `name` | string | 显示名,以最近一次平台送来的为准 |
| `created_at` / `updated_at` | RFC 3339 | |

## Agent

一个人在平台上的一个 agent。机制见 [designs/accounts.md](../designs/accounts.md)。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `ag_…` |
| `platform` | string | |
| `handle` | string | 评论里 @ 的名字,如 `alice-claude`;小写,2–40 个 `[a-z0-9_-]`,首尾是字母或数字;平台内唯一,建好后不能改 |
| `agent_user_id` | string | 平台里同名应用用户的 id;这个作者的评论不唤醒任何 agent |
| `owner` | string | 主人的 user_id |
| `device_id` | string | 绑定的设备 `dv_…`;空 = 还没接入(或被解绑 / 设备被吊销) |
| `workdir` | string | 设备上的工作目录(git 仓库根),接入时取 |
| `base_ref` | string | worktree 从哪个分支切,接入时取,默认 `main` |
| `driver` | string | `claude` |
| `wake` | `owner` \| `members` | 谁能叫醒;默认 `owner`。见 [wake-and-trust.md](../designs/wake-and-trust.md#3-谁能叫醒谁的-agent) |
| `actions` | Action[] | 空 = 策略允许的全部;建 / 改时就按策略截断后再存 |
| `workspace_mode` | `readonly` \| `readwrite` \| 空 | 空 = 跟策略;只能比策略更严 |
| `created_at` / `updated_at` | RFC 3339 | |

平台接口交回的 agent 另带 `online`(bool)和 `device`(`{id, name, os, online, last_seen}`,没接入时没有),见 [apis/platform.md](../apis/platform.md#管理-agent)。

## Device

一个人接进来的一台电脑。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `dv_…` |
| `platform` | string | 设备只属于一个平台的一个主人;同一台电脑接两个人或两个平台就是两台设备 |
| `owner` | string | 主人的 user_id |
| `name` | string | 主机名,只用于展示 |
| `os` | string | 如 `darwin/arm64` |
| `key_hash` | string | 设备密钥(`dk_…`)的 sha256 hex;明文只在兑换时交给 daemon 一次 |
| `last_seen` | RFC 3339 | 最近一次用设备身份请求;最多一分钟落库一次 |
| `revoked_at` | RFC 3339 | 吊销时间;非空后所有请求 401 |
| `created_at` | RFC 3339 | |

**在线**不存库:有活着的推送连接,或 45 秒内请求过,就算在线。

## ConnectCode

一次性接入码,只能把 `agent_id` 这个 agent 接到兑换它的那台电脑上。

| 字段 | 类型 | 说明 |
|---|---|---|
| `hash` | string | 码的 sha256 hex;明文 `ac_` + 128 位随机(base64url)只在发码时交给平台一次 |
| `platform` | string | |
| `agent_id` | string | |
| `expires_at` | RFC 3339 | 发码后 15 分钟 |

兑换时原子地取走并删除;给同一个 agent 发新码时旧码作废;agent 删掉时它的码一并删掉。

## Session

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `s_…` |
| `platform` | string | |
| `thread` | string | 线程键 |
| `agent_id` | string | 哪个 agent 的会话;`(platform, thread, agent_id)` 唯一。同一线程里两个 agent 是两段会话 |
| `agent_session_id` | string | agent 自己的会话 id,第一次运行结束后由 daemon 回填,之后 `--resume` |
| `branch` | string | 现场分支,`agent/<handle>/<线程键安全形式>` |
| `created_at` / `updated_at` | RFC 3339 | |

## Run

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | `r_…` |
| `session_id` | string | |
| `platform` / `agent_id` / `thread` | string | 冗余自会话,领取和写回时用 |
| `trigger` | Comment | 叫醒它的那条评论 |
| `context` | Material[] | 线程材料 |
| `actions` | Action[] | 建运行时按 agent 设置和策略算出生效值拷过来,之后改设置 / 策略不影响已排队的运行 |
| `workspace_mode` | string | 同上 |
| `status` | `queued` \| `claimed` \| `succeeded` \| `failed` | |
| `attempts` | int | 被领取过几次;每次领取 +1,租约过期回收后再领取继续加 |
| `device_id` | string | 当前持有它的设备(回收后清空) |
| `lease_until` | RFC 3339 | claimed 时的租约到期时间;心跳续期,过期即回收 |
| `error` | string | 失败原因:租约用完是 `lease expired`,排队超时是 `queue_ttl: device offline`,agent 被删是 `agent deleted` |
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
| `agent_id` | string | 哪个 agent 的运行 |
| `agent` | string | 它的 handle;平台据此选用哪个应用用户写回复 |
| `in_reply_to` | string | 叫醒评论的 id,平台可据此把回复挂成楼中楼 |
| `text` | string | `reply` 的正文 |
| `status` | string | `status` 的目标状态,如 `in_review`;取值由平台定 |
| `activity` | `{type, body}` | `activity` 的内容;type 为 `thought` / `action` / `elicitation` / `response` / `error`(沿用 Linear AgentSession) |
| `created_at` | RFC 3339 | |
