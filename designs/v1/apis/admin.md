# admin API

實例主人的只讀查詢。hub.json 配了 `admin_key` 纔開:

```
Authorization: Bearer <admin_key>
```

沒配或密鑰不對 → 401 `unauthorized`。命令行:`agentuse admin …`,見 [cli.md](cli.md#實例主人)。

## GET /v1/admin/sessions

| 參數 | 說明 |
|---|---|
| `platform` | 只看這個平臺 |
| `limit` | 最多幾條,默認 100 |

按最近更新倒序:

```json
{"sessions": [{"id": "s_…", "platform": "shop", "thread": "issue:MUL-42", "agent_session_id": "thread_9c1", "branch": "agent/claude/MUL-42", "created_at": "…", "updated_at": "…"}]}
```

## GET /v1/admin/runs

| 參數 | 說明 |
|---|---|
| `platform` | 只看這個平臺 |
| `session` | 只看這個會話 |
| `status` | `queued` / `claimed` / `succeeded` / `failed` |
| `limit` | 最多幾條,默認 100 |

按入隊倒序:`{"runs": [Run, …]}`,字段見 [structs/models.md#run](../structs/models.md#run)。

## GET /v1/admin/runs/{run}

一條 [Run](../structs/models.md#run);不存在 → 404 `not_found`。
