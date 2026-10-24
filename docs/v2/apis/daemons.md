# daemons

用户的 daemon(一台电脑一个)。表结构见 [tables/daemons](../tables/daemons.md) 和 [tables/daemon_agents](../tables/daemon_agents.md),接入流程见 [designs/connect](../designs/connect.md)。

## daemon 对象

```json
{
  "id": "dm_5c1c8602536e55cb",
  "user_id": "3f2b8c1e-…",
  "name": "my-laptop",
  "status": "online",
  "last_seen_at": 1792800027,
  "created_at": 1792800012,
  "agents": [
    {
      "daemon_id": "dm_5c1c8602536e55cb",
      "user_id": "3f2b8c1e-…",
      "agent": "claude",
      "version": "2.1.310 (Claude Code)",
      "available": true,
      "enabled": false,
      "updated_at": 1792800027
    }
  ],
  "setup_token": "st_…",
  "setup_command": "curl -fsSL \"https://agentuse.example.com/setup.sh\" -o setup.sh && TOKEN=\"st_…\" bash setup.sh"
}
```

- `status`:`pending`(已添加、还没跑安装命令)/ `online` / `offline`。
- `setup_token`、`setup_command` 只在 `pending` 时出现。命令里的服务地址取服务的 `-public-url`,没配就按请求的 Host(和 `X-Forwarded-Proto` / `X-Forwarded-Host`)推断。
- `agents`:这台电脑上报过的 agent。`available` 是本机最近一次上报里有没有它，`enabled` 是用户有没有打开。任务只派给 `available && enabled` 的。
- `last_seen_at` 没连过时为 `null`。

## GET /api/daemons

列出我的 daemon(admin 看所有人的),按创建时间排序。

## POST /api/daemons

添加一台 daemon,生成安装命令。

```json
{ "name": "my-laptop" }
```

请求体可以省略;`name` 为空时，注册时用电脑的主机名。返回 201 和 daemon 对象(`status` 为 `pending`)。

## POST /api/daemons/{id}/reset

重新生成安装命令：生成新的 `setup_token`,清掉原来的密钥，状态回到 `pending`,原来那台电脑的连接立即断开。返回 200 和 daemon 对象。

## PUT /api/daemons/{id}/agents/{agent}

开关这台 daemon 上的某个 agent。

```json
{ "enabled": true }
```

只能开关 `agents` 列表里已有的(本机上报过的),否则 404。打开后，这个用户排队中的任务马上会派过来。返回 200 和 daemon 对象。

## DELETE /api/daemons/{id}

删除 daemon,返回 204。它的连接立即断开、密钥失效，正在跑的任务回到 `pending`。
