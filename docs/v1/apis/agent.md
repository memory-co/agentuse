# agent API

agent → hub,用 daemon 注入的運行令牌:

```
Authorization: Bearer $AGENTUSE_TOKEN
```

一般不直接調,用 [CLI](cli.md) 的 `agentuse reply` / `status` / `activity`。

寫回的線程、運行、回覆對象都**以令牌爲準**;請求體裏多帶的 `thread` 之類字段被忽略。運行結束(complete)後令牌即使沒過期也寫不了(409)。

## GET /v1/run

看這枚令牌的範圍:

```json
{"run": "r_…", "platform": "shop", "thread": "issue:MUL-42", "actions": ["reply", "status"], "exp": 1792300000}
```

## POST /v1/run/reply

需要動作 `reply`。

```json
{"text": "已修復,見分支 agent/claude/MUL-42"}
```

**200** `{}`:平臺已經收下回調。

## POST /v1/run/status

需要動作 `status`。

```json
{"status": "in_review"}
```

狀態取值由平臺定,hub 不校驗。

## POST /v1/run/activity

不需要額外動作,有效令牌即可。

```json
{"activity": {"type": "thought", "body": "先看下退款計算的單測"}}
```

`type`:`thought` / `action` / `elicitation` / `response` / `error`。hub 原樣轉發,平臺決定怎麼展示(博客可以只顯示最終回覆,工單可以顯示「正在運行測試」)。
