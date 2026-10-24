# harnesses

全局登记的 CLI harness。表结构和自定义 harness 的约定见 [tables/harnesses](../tables/harnesses.md)。

## harness 对象

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

`kind` 是 `builtin`(内置：`claude`、`codex`,服务启动时写入)或 `custom`(用户注册);`owner_id` 是自定义 harness 的注册者，内置为空串。

## GET /api/harnesses

列出全部 harness,内置的在前，再按名字排序。所有登录用户都能看到全部。

## POST /api/harnesses

注册自定义 harness。

```json
{ "name": "my-harness", "display_name": "My Harness", "description": "说明，可省略" }
```

- `name` 只能用小写字母、数字和 `-`,以字母或数字开头，最长 63 个字符;已存在返回 400。
- `display_name` 省略时用 `name`。

返回 201 和 harness 对象。注册后还要在 daemon 的 `~/.agentuse/daemon.json` 里配置本机启动命令，见 [daemon](daemon.md#本机配置)。

## DELETE /api/harnesses/{name}

删除自定义 harness,返回 204。

- 内置 harness 不能删;普通用户只能删自己注册的(否则 404)。
- 还有会话引用它时返回 400。
- 各 daemon 上对应的开关一起删掉。
