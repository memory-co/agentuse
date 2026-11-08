# works

工作：協調者 + 線程 + 共用說明。表結構見 [tables/works](../tables/works.md),規則見 [designs/work](../designs/work.md)。

普通用戶只看得到自己的工作,admin 看全部。看不到返回 404。

## work 對象

```json
{
  "id": "wk_3a1f9c0e2b7d4c55",
  "user_id": "u_10086",
  "name": "重構 daemon",
  "instructions": "代碼在 memory-co/agentuse,改完跑 go test ./...",
  "coordinator_id": "ss_9d0c…",
  "thread_count": 3,
  "created_at": 1792800000,
  "updated_at": 1792800105
}
```

## GET /api/works

列出工作，按 `updated_at` 新的在前。

## POST /api/works

```json
{ "name": "重構 daemon", "agent_id": "code-helper", "instructions": "可省略" }
```

- `name` 必填，空或超過 64 個字返回 400。
- `agent_id` 是協調者用的 agent,必須是調用者自己的(和線程一樣，別人的返回 400);其餘規則同開會話([sessions](sessions.md#post-apisessions)):不存在、沒有運行時、harness 不可用都返回 400。
- 同時建出協調者會話(`idle`,沒有消息),返回 201 和 `{"work": …, "coordinator": …}`。之後給協調者發消息用 `POST /api/sessions/{coordinator_id}/messages`。

## GET /api/works/{id}

返回 work 對象。線程用 `GET /api/sessions?work_id=wk_…` 取([sessions](sessions.md)),協調者用 `GET /api/sessions/{coordinator_id}`。

## PATCH /api/works/{id}

```json
{ "name": "新名字", "instructions": "新說明" }
```

只改傳了的字段，返回 work 對象。改了說明，下一條消息就用新的。

## DELETE /api/works/{id}

連協調者和所有線程一起刪，返回 204。有會話在 `running` / `waiting` 時返回 409。

## 工作令牌(`wt_`)

協調者跑的時候,daemon 收到的派活消息裏帶 `work_token`([apis/daemon](daemon.md)),作爲環境變量 `AGENTUSE_WORK` 給 harness 進程，本機的 `agentuse work` 命令拿它調接口。

- 身份是工作的主人，只在這條消息還沒結束時有效。
- 只能調這些，別的一律 403:`GET /api/agents`、`GET /api/sessions?work_id=<本工作>`、`POST /api/sessions`(`work_id` 必須是本工作，開的就是線程;`agent_id` 必須是工作主人自己的 agent,哪臺 daemon 上的都行，別人的 403)、`GET /api/sessions/{線程}`、`POST /api/sessions/{線程}/messages`、`POST /api/sessions/{線程}/resolve`。
- 用工作令牌時 `GET /api/sessions`、`POST /api/sessions` 不帶 `work_id` 就是本工作;令牌過期返回 401。

## 附件權限擴展

`wt_` 可對本工作的協調者和線程調用 [attachments](attachments.md) 的列表、上傳、元數據、下載和刪除接口，越出本工作返回 403。有效期仍是協調者當前消息的執行期。創建線程可用 `defer_start` 先建空會話、上傳文件再發送第一條消息；工作刪除時附件一起回收。詳見 [attachment 設計](../designs/attachment.md)。

## multi_agent 工作

創建時可傳 `mode: "multi_agent"`，agent_id 作爲工作上的 coordinator_agent_id，協調 session 本身不綁定 agent。工作頁消息顯式選中協調 agent，也可 @ 其他自己的 agent。PATCH 支持 coordinator_agent_id；只允許自己的、支持新協議的有效 agent，協調 session 忙時返回 409。

工作令牌只在協調 run/attempt 的有效租約內可用。新線程也使用共享 session，多目標消息在所有目標得到最終結果後向協調者彙總一次；插話未送達改掛後續 run 時繼續等待，不提前通知。自動通知顯式路由到工作的協調 agent。工作裏有未結束的新模式 run 時，不能刪除整組工作。
