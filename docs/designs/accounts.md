# 用户、agent、设备

> **状态:已实施**(roadmap 第一阶段)。代码:[`internal/models/accounts.go`](../../internal/models/accounts.go)、[`internal/hub/accounts.go`](../../internal/hub/accounts.go);字段见 [structs/models.md](../structs/models.md#agent),接口见 [apis/platform.md](../apis/platform.md#管理-agent)。

hub 是平台侧的实例,一个 hub 服务这个平台上的所有用户。所以它必须知道两件事:**被 @ 的 agent 是谁的**,**这个人的电脑是哪台**。没有这一层时,同一平台的运行谁的 daemon 都能领,多用户下会把 bob 叫的活派到 alice 的电脑上。这篇讲这一层。

## 1. 三个对象

```
User(平台里的一个人)
 ├── Agent(这个人在平台上的一个 agent,如 @alice-claude)── 绑定 ──▶ Device + 目录
 └── Device(这个人接进来的一台电脑,上面跑着 daemon)
```

| 对象 | 是什么 | 谁建 | 键 |
|---|---|---|---|
| **User** | 平台里的一个人。hub 不管注册登录,身份由平台签名担保 | hub 第一次见到时自动建(建 agent 或 notify 时) | `(platform, user_id)`,`user_id` 是平台自己的用户 id |
| **Agent** | 一个人在平台上的一个 agent:名字(`alice-claude`)、主人、跑在哪台设备的哪个目录、用什么驱动、权限 | 平台替用户建(设置页「接入我的 agent」) | `ag_…`;`handle` 在平台内唯一 |
| **Device** | 一个人接进来的一台电脑,上面的 daemon 用设备密钥连 hub | 用户在电脑上跑 `agentuse connect` 兑换接入码时自动建([connect.md](connect.md)) | `dv_…`;属于一个平台的一个主人 |

hub **不存用户的密码、邮箱或权限**,只存平台给的 `user_id` 和显示名。平台里谁是谁、谁有什么角色,永远以平台每次签名送来的为准。

## 2. Agent

| 字段 | 说明 |
|---|---|
| `id` | `ag_…` |
| `platform` | 属于哪个平台 |
| `handle` | 评论里 @ 的名字,平台内唯一,如 `alice-claude`;小写 2–40 个 `[a-z0-9_-]`,首尾是字母或数字,建好不能改。平台在自己的用户表里也要有一个同名的应用用户 |
| `agent_user_id` | 平台里那个应用用户的 id;agent 的回复以它的名义写,它发的评论不唤醒任何 agent |
| `owner` | 主人的 `user_id` |
| `device_id` | 绑定的设备 `dv_…`;没接入时为空 |
| `workdir` | 设备上的工作目录,如 `/Users/alice/work/shop-api`;接入时取用户跑命令的那个目录所在 git 仓库的根 |
| `base_ref` | worktree 从哪个分支切;接入时取,默认是仓库当前分支 |
| `driver` | `claude`(之后加 `codex` 等) |
| `wake` | 谁能叫醒它:`owner`(只有主人)/ `members`(平台策略 `wake_roles` 里的人)。默认 `owner` |
| `actions` / `workspace_mode` | agent 能做什么、现场权限;**不能超过平台策略**。`actions` 取与策略的交集(空 = 策略的全部);`workspace_mode` 只有策略是 `readwrite` 且 agent 没设 `readonly` 时才是读写,策略没配按只读算 |

**一个 agent 绑一台设备上的一个目录。** 现场是本机仓库里的 worktree,换机器就是换现场,所以不做「一个 agent 在多台电脑间漂」。想在两台电脑上各有一个,就建两个 agent(`alice-claude`、`alice-claude-mini`)。在新电脑上用同一个 agent 的新接入码再接一次,就是把它搬过去。

一个人可以有多个 agent(不同仓库、不同驱动),平台决定每人最多几个。

## 3. Device

| 字段 | 说明 |
|---|---|
| `id` | `dv_…` |
| `platform` / `owner` | 属于哪个平台的哪个人。同一台电脑接了两个人或两个平台,在 hub 看来是两台设备 |
| `name` | 主机名,如 `alice-mbp`,只用于展示 |
| `key_hash` | 设备密钥的 sha256;密钥只在接入时交给 daemon 一次,hub 不存明文 |
| `last_seen` | 最近一次用设备身份请求(最多一分钟落库一次) |
| `revoked_at` | 吊销时间;吊销后这台设备的连接、领取、心跳全部 401 |

设备密钥取代了原来 hub.json 里手写的 `daemon_keys`。

**在线**不靠 `last_seen` 判断:设备有一条活着的推送连接,或 45 秒内用设备身份请求过(轮询模式),就算在线。只兑换接入码不算在线,要 `agentuse daemon start` 连上来才算。

## 4. 运行只派给主人的设备

运行带 `agent_id`,会话的键是 `(platform, thread, agent_id)`:同一个工单里 @alice-claude 和 @bob-claude 是两段独立的会话,各自的分支(`agent/alice-claude/MUL-42`、`agent/bob-claude/MUL-42`)。

领取时多一个条件:

```
可领 = status 是 queued
     且 所属会话没有进行中的运行        ← 同一线程同一 agent 串行
     且 run.agent.device_id = 来领的设备
```

于是 bob 的电脑永远领不到 alice-claude 的运行。设备不在线(或 agent 还没接入)时运行就在队列里等;等多久由平台策略 `queue_ttl_seconds` 决定(默认 86400,即 24 小时),过期判失败(`error: queue_ttl: device offline`),并回调一条 `activity`(type `error`,「@alice-claude的电脑一直不在线,这条没有处理。等它上线后再 @ 一次。」),平台据此提示。hub 每 10 秒检查一次,和租约回收一起做。

## 5. 一条评论 @ 了好几个 agent

「@alice-claude @bob-claude 你们俩各看一下」会给每个能被叫醒的 agent 各排一条运行,互不影响。评论里 @ 到、但这个平台上没有的名字(平台里的普通用户)直接忽略。notify 的响应逐个交代:

```json
{
  "woken": true,
  "reason": "woken",
  "agents": [
    {"handle": "alice-claude", "agent_id": "ag_…", "woken": true,  "reason": "woken",    "online": true,  "session_id": "s_…", "run_id": "r_…"},
    {"handle": "bob-claude",   "agent_id": "ag_…", "woken": false, "reason": "not_owner", "online": false}
  ]
}
```

顶层的 `woken` 是「至少叫醒了一个」;`reason` 叫醒了就是 `woken`,一个 agent 都没 @ 到是 `no_mention`,否则取 `agents` 里第一个的原因。作者是本平台任何一个 agent 的应用用户时,整条评论直接 `self_comment`,`agents` 为空。

## 6. 平台怎么管 agent

平台用平台密钥签名调 hub,全在服务端调,用户浏览器不直接碰 hub。签名和 notify 同一套,但签的是「方法 + 路径 + 请求体」,截获的一条请求不能换个路径重放:

| 调用 | 用途 |
|---|---|
| `POST /v1/platforms/{p}/agents` | 替某个用户建 agent:`{owner, owner_name, handle, agent_user_id, wake?, actions?, workspace_mode?}`,交回 agent 和第一条接入命令 |
| `GET /v1/platforms/{p}/agents?owner=` | 列某人的 agent,带在线状态和设备,给设置页展示 |
| `GET /v1/platforms/{p}/agents/{id}` | 查一个 |
| `POST /v1/platforms/{p}/agents/{id}/connect-code` | 重新生成接入码(换电脑、命令过期了),旧码作废 |
| `PATCH /v1/platforms/{p}/agents/{id}` | 改 `wake` / `actions` / `workspace_mode` / `agent_user_id` |
| `DELETE /v1/platforms/{p}/agents/{id}` | 删 agent:排队中和进行中的运行判失败(`agent deleted`),接入码删掉,会话和运行留作记录 |
| `DELETE /v1/platforms/{p}/devices/{id}` | 吊销一台设备(电脑丢了):密钥作废,绑在上面的 agent 变成未接入 |

正式契约见 [apis/platform.md](../apis/platform.md#管理-agent),Go SDK 有对应方法。

## 7. 不做什么

- **不做账号体系**:hub 没有注册、登录、密码。用户是谁、在不在团队里,问平台。
- **不跨平台认人**:同一个人在两个平台上是两个 User。一台电脑可以同时接两个平台的 hub,daemon 各连各的。
- **不让 agent 漂移**:一个 agent 一台设备一个目录,原因见 §2。
