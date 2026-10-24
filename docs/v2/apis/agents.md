# agents

用户起了名字的 agent,背后映射一个 harness。表结构和换 harness 的规则见 [tables/agents](../tables/agents.md)。

## agent 对象

```json
{
  "id": "ag_3f9a1c0d2b7e4a51",
  "user_id": "u_10086",
  "name": "写代码助手",
  "harness": "claude",
  "description": "日常改代码",
  "created_at": 1792800000,
  "updated_at": 1792800000
}
```

## GET /api/agents

列出 agent,按名字排序。普通用户只看自己的,admin 看全部。

## POST /api/agents

```json
{ "name": "写代码助手", "harness": "claude", "description": "说明，可省略" }
```

- `name` 必填，最长 64 个字，同一用户下重名返回 400。
- `harness` 必填，必须是登记过的 harness,否则 400。

返回 201 和 agent 对象。

## GET /api/agents/{id}

返回 agent 对象。

## PATCH /api/agents/{id}

改名字、换 harness、改说明，只给要改的字段:

```json
{ "harness": "codex" }
```

规则同新建。返回 agent 对象。换 harness 只影响之后新建的会话，已有会话还用原来的 harness。

## DELETE /api/agents/{id}

删除 agent,返回 204。用过它的会话保留,`agent_id` 变空。
