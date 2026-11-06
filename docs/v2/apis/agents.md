# agents

用戶起了名字的 agent,綁定一個運行時(daemon + harness)。表結構和切換運行時的規則見 [tables/agents](../tables/agents.md)。

## agent 對象

```json
{
  "id": "code-helper",
  "user_id": "u_10086",
  "name": "寫代碼助手",
  "daemon_id": "dm_5c1c8602536e55cb",
  "harness": "claude",
  "description": "日常改代碼",
  "created_at": 1792800000,
  "updated_at": 1792800000
}
```

`daemon_id` 爲空時不出現(daemon 被刪了，要先切換運行時)。

## GET /api/agents

列出 agent,按名字排序。普通用戶只看自己的,admin 看全部。

## POST /api/agents

```json
{ "id": "code-helper", "name": "寫代碼助手", "daemon_id": "dm_5c1c8602536e55cb", "harness": "claude", "description": "說明，可省略" }
```

- `id` 全局唯一。只能用小寫字母、數字、`-` 和 `_`,以字母或數字開頭，最長 64 個字符;已被佔用返回 400。省略時生成 `ag_xxx`。建好後不能改。
- `name` 必填，最長 64 個字，同一用戶下重名返回 400。
- `daemon_id`、`harness` 必填，一起指定運行時:daemon 必須是自己的,harness 必須是這臺 daemon 上報過、現在可用並且已開啓的(在 [daemon 對象](daemons.md) 的 `harnesses` 列表裏，`available && enabled`),否則 400 並說明原因。

返回 201 和 agent 對象。

## GET /api/agents/{id}

返回 agent 對象。普通用戶只能查自己的。

## PATCH /api/agents/{id}

改名字、切換運行時、改說明，只給要改的字段(`id` 不能改):

```json
{ "daemon_id": "dm_9a0b…", "harness": "codex" }
```

- 切換運行時要 `daemon_id` 和 `harness` 一起給，只給一個返回 400;規則同新建。
- 只有主人(或 admin)能改，否則 404。

返回 agent 對象。切換運行時隻影響之後新建的會話，已有會話還在原來的 daemon 和 harness 上。

## DELETE /api/agents/{id}

刪除 agent,返回 204。只有主人(或 admin)能刪。用過它的會話保留,`agent_id` 變空。
