# 身份三段式

> 代码:[`pkg/signature/`](../../pkg/signature/)、[`internal/services/token/`](../../internal/services/token/)、[`internal/services/callback/`](../../internal/services/callback/)

平台不接触 agent 令牌,agent 不直接访问平台。身份分三段,每段只验自己该验的:

```
平台 ──(平台签名)──▶ hub ──(运行令牌)──▶ agent ──▶ hub ──(hub 签名)──▶ 平台
       进来:验签不认人      中间:线程级、动作级           出去:平台只验 hub
```

## 1. 进来:平台签名

平台用与 hub 约定的密钥,对「线程、作者(id / 名字 / 角色)、内容」整个请求体签名。hub **只验签,不认识用户本人**——这是 Disqus SSO(`remote_auth_s3`)那一套:角色由平台担保,hub 不维护用户表。

- 算法:`HMAC-SHA256(secret, timestamp + "." + body)`,hex 编码,前缀 `v1=`。
- 头:`X-AgentUse-Timestamp`(unix 秒)、`X-AgentUse-Signature`。
- 时间戳偏差超过 5 分钟拒绝,防重放。
- 细节见 [apis/platform.md](../apis/platform.md#签名)。

## 2. 中间:运行令牌

daemon 领取运行时,hub 为这次运行签发一枚令牌:

- 形态 `aut_<claims>.<sig>`,hub 自己的 `token_key` 签,claims 见 [structs/models.md](../structs/models.md#runtoken)。
- **范围**:一个平台的一个线程、一次运行。
- **动作**:只限策略里的 `actions`(如博客只有 `reply`,工单有 `reply` + `status`)。
- **时效**:最长 24 小时;运行 complete 后立刻失效(hub 检查运行状态)。
- daemon 把它注入 `AGENTUSE_TOKEN`,agent 用 `agentuse reply` / `agentuse status` 调 hub。

写回时线程、运行 id、回复对象都**以令牌和运行为准**,agent 在请求体里自带的 `thread` 一律忽略,所以一枚令牌写不到别的线程。

## 3. 出去:hub 签名

hub 校验令牌后,用同一套签名(该平台的 `secret`)回调平台的 `callback_url`。平台只需要验 hub 的签名,然后把 `reply` 写成 agent 应用用户的评论、把 `status` 写回对象。

> 现在进出两个方向共用一个平台密钥。拆成「平台 → hub」与「hub → 平台」两把,或者 hub 用非对称签名,见 [roadmap.md](roadmap.md)。

## 4. agent 在平台里是谁

平台在用户表里加一个代表 agent 的**应用用户**(参考 Linear 的 `actor=app`),它:

- 是 `@claude` 能提到的名字(策略里的 `agent_name`);
- 是 agent 回复的作者;它的 id 写进策略的 `agent_user_id`,hub 据此忽略 agent 自己的评论。
