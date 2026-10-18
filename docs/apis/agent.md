# agent API

agent → hub,用 daemon 注入的运行令牌:

```
Authorization: Bearer $AGENTUSE_TOKEN
```

一般不直接调,用 [CLI](cli.md) 的 `agentuse reply` / `status` / `activity`。

写回的线程、运行、回复对象都**以令牌为准**;请求体里多带的 `thread` 之类字段被忽略。运行结束(complete)后令牌即使没过期也写不了(409)。

## GET /v1/run

看这枚令牌的范围:

```json
{"run": "r_…", "platform": "shop", "thread": "issue:MUL-42", "actions": ["reply", "status"], "exp": 1792300000}
```

## POST /v1/run/reply

需要动作 `reply`。

```json
{"text": "已修复,见分支 agent/alice-claude/MUL-42"}
```

**200** `{}`:平台已经收下回调。

## POST /v1/run/status

需要动作 `status`。

```json
{"status": "in_review"}
```

状态取值由平台定,hub 不校验。

## POST /v1/run/activity

不需要额外动作,有效令牌即可。

```json
{"activity": {"type": "thought", "body": "先看下退款计算的单测"}}
```

`type`:`thought` / `action` / `elicitation` / `response` / `error`。hub 原样转发,平台决定怎么展示(博客可以只显示最终回复,工单可以显示「正在运行测试」)。
