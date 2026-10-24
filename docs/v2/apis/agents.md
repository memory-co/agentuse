# agents

用户起了名字的 agent,背后映射一个 harness。表结构和换 harness 的规则见 [tables/agents](../tables/agents.md)。

## agent 对象

```json
{
  "id": "code-helper",
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
{ "id": "code-helper", "name": "写代码助手", "harness": "claude", "description": "说明，可省略" }
```

- `id` 全局唯一，别人用它 @ 这个 agent。只能用小写字母、数字、`-` 和 `_`,以字母或数字开头，最长 64 个字符;已被占用返回 400。省略时生成 `ag_xxx`。建好后不能改。

- `name` 必填，最长 64 个字，同一用户下重名返回 400。
- `harness` 必填，必须是登记过的 harness,否则 400。

返回 201 和 agent 对象。

## GET /api/agents/{id}

返回 agent 对象。任何登录用户都能查任何人的 agent,用来解析 @id。

## PATCH /api/agents/{id}

改名字、换 harness、改说明，只给要改的字段(`id` 不能改):

```json
{ "harness": "codex" }
```

只有主人(或 admin)能改，否则 404。规则同新建。返回 agent 对象。换 harness 只影响之后新建的会话，已有会话还用原来的 harness。

## DELETE /api/agents/{id}

删除 agent,返回 204。只有主人(或 admin)能删。用过它的会话保留,`agent_id` 变空。
