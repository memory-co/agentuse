# agents

用户起了名字的 agent,绑定一个运行时(daemon + harness)。表结构和切换运行时的规则见 [tables/agents](../tables/agents.md)。

## agent 对象

```json
{
  "id": "code-helper",
  "user_id": "u_10086",
  "name": "写代码助手",
  "daemon_id": "dm_5c1c8602536e55cb",
  "harness": "claude",
  "description": "日常改代码",
  "created_at": 1792800000,
  "updated_at": 1792800000
}
```

`daemon_id` 为空时不出现(daemon 被删了，要先切换运行时)。

## GET /api/agents

列出 agent,按名字排序。普通用户只看自己的,admin 看全部。

## POST /api/agents

```json
{ "id": "code-helper", "name": "写代码助手", "daemon_id": "dm_5c1c8602536e55cb", "harness": "claude", "description": "说明，可省略" }
```

- `id` 全局唯一。只能用小写字母、数字、`-` 和 `_`,以字母或数字开头，最长 64 个字符;已被占用返回 400。省略时生成 `ag_xxx`。建好后不能改。
- `name` 必填，最长 64 个字，同一用户下重名返回 400。
- `daemon_id`、`harness` 必填，一起指定运行时:daemon 必须是自己的,harness 必须是这台 daemon 上报过的(在 [daemon 对象](daemons.md) 的 `harnesses` 列表里),否则 400。

返回 201 和 agent 对象。

## GET /api/agents/{id}

返回 agent 对象。普通用户只能查自己的。

## PATCH /api/agents/{id}

改名字、切换运行时、改说明，只给要改的字段(`id` 不能改):

```json
{ "daemon_id": "dm_9a0b…", "harness": "codex" }
```

- 切换运行时要 `daemon_id` 和 `harness` 一起给，只给一个返回 400;规则同新建。
- 只有主人(或 admin)能改，否则 404。

返回 agent 对象。切换运行时只影响之后新建的会话，已有会话还在原来的 daemon 和 harness 上。

## DELETE /api/agents/{id}

删除 agent,返回 204。只有主人(或 admin)能删。用过它的会话保留,`agent_id` 变空。
