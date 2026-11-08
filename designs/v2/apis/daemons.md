# daemons

用戶的 daemon(一臺電腦一個)。表結構見 [tables/daemons](../tables/daemons.md) 和 [tables/daemon_harnesses](../tables/daemon_harnesses.md),接入流程見 [designs/connect](../designs/connect.md)。

## daemon 對象

```json
{
  "id": "dm_5c1c8602536e55cb",
  "user_id": "u_10086",
  "name": "my-laptop",
  "status": "online",
  "last_seen_at": 1792800027,
  "created_at": 1792800012,
  "harnesses": [
    {
      "daemon_id": "dm_5c1c8602536e55cb",
      "user_id": "u_10086",
      "harness": "claude",
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

- `status`:`pending`(已添加、還沒跑安裝命令)/ `online` / `offline`。
- `setup_token`、`setup_command` 只在 `pending` 時出現。命令裏的服務地址取服務的 `-public-url`,沒配就按請求的 Host(和 `X-Forwarded-Proto` / `X-Forwarded-Host`)推斷。
- `harnesses`:這臺電腦上報過的 harness。`available` 是本機最近一次上報裏有沒有它，`enabled` 是用戶有沒有打開。只派給 `available && enabled` 的。
- `last_seen_at` 沒連過時爲 `null`。

## GET /api/daemons

列出我的 daemon(admin 看所有人的),按創建時間排序。

## POST /api/daemons

添加一臺 daemon,生成安裝命令。

```json
{ "name": "my-laptop" }
```

請求體可以省略;`name` 爲空時，註冊時用電腦的主機名。返回 201 和 daemon 對象(`status` 爲 `pending`)。

## POST /api/daemons/{id}/reset

重新生成安裝命令：生成新的 `setup_token`,清掉原來的密鑰，狀態回到 `pending`,原來那臺電腦的連接立即斷開。返回 200 和 daemon 對象。

## PUT /api/daemons/{id}/harnesses/{harness}

開關這臺 daemon 上的某個 harness。

```json
{ "enabled": true }
```

只能開關 `harnesses` 列表裏已有的(本機上報過的),否則 404。打開後，這個用戶排隊中的會話馬上會派過來;關掉後，這個 harness 上排隊中的會話直接失敗(見 [sessions](sessions.md)),以它爲運行時的 agent 也不能再開新會話，要先切換運行時。返回 200 和 daemon 對象。

## DELETE /api/daemons/{id}

刪除 daemon,返回 204。它的連接立即斷開、密鑰失效，綁定在它上面的會話不能再發消息，沒回完的消息記爲失敗。
