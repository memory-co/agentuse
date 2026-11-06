# harnesses

全局登記的 CLI harness。表結構和自定義 harness 的約定見 [tables/harnesses](../tables/harnesses.md)。

## harness 對象

```json
{
  "name": "claude",
  "kind": "builtin",
  "display_name": "Claude Code",
  "description": "Anthropic Claude Code CLI(claude -p)",
  "owner_id": "",
  "created_at": 1792799993
}
```

`kind` 是 `builtin`(內置：`claude`、`codex`,服務啓動時寫入)或 `custom`(用戶註冊);`owner_id` 是自定義 harness 的註冊者，內置爲空串。

## GET /api/harnesses

列出全部 harness,內置的在前，再按名字排序。所有登錄用戶都能看到全部。

## POST /api/harnesses

註冊自定義 harness。

```json
{ "name": "my-harness", "display_name": "My Harness", "description": "說明，可省略" }
```

- `name` 只能用小寫字母、數字和 `-`,以字母或數字開頭，最長 63 個字符;已存在返回 400。
- `display_name` 省略時用 `name`。

返回 201 和 harness 對象。註冊後還要在 daemon 的 `~/.agentuse/daemon.json` 裏配置本機啓動命令，見 [daemon](daemon.md#本機配置)。

## DELETE /api/harnesses/{name}

刪除自定義 harness,返回 204。

- 內置 harness 不能刪;普通用戶只能刪自己註冊的(否則 404)。
- 還有 agent 或會話引用它時返回 400。
- 各 daemon 上對應的開關一起刪掉。
