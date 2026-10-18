# 身份三段式

> 代码:[`pkg/signature/`](../../pkg/signature/)、[`internal/services/token/`](../../internal/services/token/)、[`internal/services/callback/`](../../internal/services/callback/)

平台不接触 agent 令牌,agent 不直接访问平台。身份分三段,每段只验自己该验的(用户电脑怎么拿到自己的设备密钥是另一条线,见 [connect.md](connect.md#4-安全)):

```
平台 ──(平台签名)──▶ hub ──(运行令牌)──▶ agent ──▶ hub ──(hub 签名)──▶ 平台
       进来:验签不认人      中间:线程级、动作级           出去:平台只验 hub
```

## 1. 进来:平台签名

平台用与 hub 约定的密钥,对「线程、作者(id / 名字 / 角色)、内容」整个请求体签名。hub **只验签,不认识用户本人**——这是 Disqus SSO(`remote_auth_s3`)那一套:角色由平台担保。hub 的用户表只记平台给的 user_id 和显示名(用来认 agent 的主人),不存密码、角色或权限。

- 算法:`HMAC-SHA256(secret, timestamp + "." + body)`,hex 编码,前缀 `v1=`。
- 头:`X-AgentUse-Timestamp`(unix 秒)、`X-AgentUse-Signature`。
- 时间戳偏差超过 5 分钟拒绝,防重放。
- 平台管理 agent 的接口(建 / 改 / 删 agent、吊销设备)用同一把密钥,但签的是 `方法 + " " + 路径(含查询串)+ "\n" + 请求体`(`signature.Request`),一条截获的签名请求换个路径或方法就作废。
- 细节见 [apis/platform.md](../apis/platform.md#签名)。

## 2. 中间:运行令牌

daemon 领取运行时,hub 为这次运行签发一枚令牌:

- 形态 `aut_<claims>.<sig>`,hub 自己的 `token_key` 签,claims 见 [structs/models.md](../structs/models.md#runtoken)。
- **范围**:一个平台的一个线程、一次运行。
- **动作**:只限这个 agent 生效的 `actions`(agent 设置与平台策略的交集,如博客只有 `reply`,工单有 `reply` + `status`)。
- **时效**:最长 24 小时;运行 complete 后立刻失效(hub 检查运行状态)。
- **领取次数**:claims 里带 `attempt`。运行租约过期被回收、重新领取后,上一次领取的令牌作废。
- daemon 把它注入 `AGENTUSE_TOKEN`,agent 用 `agentuse reply` / `agentuse status` 调 hub。

写回时线程、运行 id、回复对象都**以令牌和运行为准**,agent 在请求体里自带的 `thread` 一律忽略,所以一枚令牌写不到别的线程。

## 3. 出去:hub 签名

hub 校验令牌后,用同一套签名(该平台的 `secret`)回调平台的 `callback_url`。平台只需要验 hub 的签名,然后把 `reply` 写成 agent 应用用户的评论、把 `status` 写回对象。

平台配置里可以给回调单独一把 `callback_secret`:notify 用 `secret` 验,回调用 `callback_secret` 签;不配则两个方向共用 `secret`。拆开之后,平台泄露其中一把,攻击者也只能冒充一个方向。hub 用非对称签名(平台只存公钥)见 [roadmap.md](roadmap.md)。

## 4. agent 在平台里是谁

每个 agent 在平台的用户表里有一个同名的**应用用户**(参考 Linear 的 `actor=app`),它:

- 是评论里 `@alice-claude` 提到的名字(agent 的 `handle`);
- 是 agent 回复的作者;它的 id 是 agent 的 `agent_user_id`,建 agent 时由平台交给 hub。作者是本平台任何一个 agent 的应用用户时,hub 不唤醒任何 agent。

## 5. 用户电脑:设备密钥

daemon 连 hub 用的是设备身份:`X-AgentUse-Device: <dv_…>` + `Authorization: Bearer <dk_…>`。设备密钥由用户跑 `agentuse connect` 兑换一次性接入码换来,hub 只存 sha256;设备被吊销后立刻 401。见 [connect.md](connect.md) 和 [apis/daemon.md](../apis/daemon.md)。
