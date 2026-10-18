# admin API

实例主人的只读查询。hub.json 配了 `admin_key` 才开:

```
Authorization: Bearer <admin_key>
```

没配或密钥不对 → 401 `unauthorized`。命令行:`agentuse admin …`,见 [cli.md](cli.md#实例主人)。

## GET /v1/admin/sessions

| 参数 | 说明 |
|---|---|
| `platform` | 只看这个平台 |
| `limit` | 最多几条,默认 100 |

按最近更新倒序:

```json
{"sessions": [{"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_id": "ag_…", "agent_session_id": "thread_9c1", "branch": "agent/alice-claude/MUL-42", "created_at": "…", "updated_at": "…"}]}
```

## GET /v1/admin/runs

| 参数 | 说明 |
|---|---|
| `platform` | 只看这个平台 |
| `session` | 只看这个会话 |
| `agent` | 只看这个 agent(`ag_…`) |
| `status` | `queued` / `claimed` / `succeeded` / `failed` |
| `limit` | 最多几条,默认 100 |

按入队倒序:`{"runs": [Run, …]}`,字段见 [structs/models.md#run](../structs/models.md#run)。

## GET /v1/admin/runs/{run}

一条 [Run](../structs/models.md#run);不存在 → 404 `not_found`。
