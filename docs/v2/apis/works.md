# works

工作：一组会话加一份共用说明。表结构见 [tables/works](../tables/works.md),规则见 [designs/work](../designs/work.md)。

普通用户只看得到自己的工作,admin 看全部。看不到返回 404。

## work 对象

```json
{
  "id": "wk_3a1f9c0e2b7d4c55",
  "user_id": "u_10086",
  "name": "重构 daemon",
  "instructions": "代码在 memory-co/agentuse,改完跑 go test ./...",
  "session_count": 3,
  "created_at": 1792800000,
  "updated_at": 1792800105
}
```

`session_count` 是里面的会话数(调用者看得到的)。

## GET /api/works

列出工作，按 `updated_at` 新的在前。

## POST /api/works

```json
{ "name": "重构 daemon", "instructions": "可省略" }
```

`name` 必填，空或超过 64 个字返回 400。返回 201 和 work 对象，属于调用者。

## GET /api/works/{id}

返回 work 对象。里面的会话用 `GET /api/sessions?work_id=wk_…` 取([sessions](sessions.md))。

## PATCH /api/works/{id}

```json
{ "name": "新名字", "instructions": "新说明" }
```

只改传了的字段。返回 work 对象。改了说明，里面会话的下一条消息就用新的。

## DELETE /api/works/{id}

删除工作，返回 204。里面的会话保留,`work_id` 变空。
