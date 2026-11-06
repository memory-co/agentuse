# Sessions API

Session 保存共享消息與附件，屬於創建者，不綁定 agent、daemon 或 harness。所有會話使用 `multi_agent` 模型。

## POST /api/sessions

```json
{"title":"評審","prompt":"檢查改動","target_agent_ids":["coder","reviewer"]}
```

title、prompt/input、target_agent_ids 都可省略，`{}` 創建空會話。mode 可省略；若提供，只接受 `multi_agent`。不接受 agent_id 綁定。工作線程只能使用對應工作令牌創建。成功返回 201：

```json
{"session":{"id":"ss_xxx","mode":"multi_agent","status":"pending"},"message":{"id":1},"runs":[],"deliveries":[]}
```

空會話只返回 session；帶首條消息時同時返回實際的 message、runs 和 deliveries。

## POST /api/sessions/{id}/messages

```json
{"prompt":"補充要求","target_agent_ids":["coder","reviewer"]}
```

每條消息最多 8 個目標。對空閒 agent 創建 run，對已有 pending/running/waiting run 的 agent 創建 steer 投遞。沒有目標只保存筆記，不隱式選取 agent。JSON input 作爲獨立任務處理。消息與全部目標路由原子提交。

成功返回 201 `{message,runs,deliveries}`。`Idempotency-Key` 相同且內容相同返回已有結果；相同 key 不同內容返回 409。目標不存在或運行時不可用返回 400。

## GET /api/sessions

列普通會話；帶 `work_id` 列指定工作的線程。工作令牌只能列本工作。

## GET /api/sessions/{id}

返回 `{session,messages,runs,deliveries,events,questions,run_counts}`。消息按 seq 排序，assistant 消息帶 agent_key、agent_name、run_id。daemon 讀取須攜帶當前 `X-Agentuse-Run` 和 `X-Agentuse-Attempt`。

## 執行

- `GET /api/sessions/{id}/runs/{run_id}`：返回 `{run,events}`。
- `POST /api/sessions/{id}/runs/{run_id}/cancel`：取消執行和關聯投遞。
- `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer`：提交 `{"answers":{"問題":"答案"}}`，返回 202。

插話統一發送消息，沒有獨立的 steers 寫接口。

## 會話管理

- `DELETE /api/sessions/{id}`：刪除會話、消息、執行與附件；有活動 run 返回 409，成功 204。
- `POST /api/sessions/{id}/resolve`：將 idle 標記爲 resolved。
- `POST /api/sessions/{id}/reopen`：恢復爲 idle。

協調者不能單獨刪除或標記完成。詳見 [設計](../designs/session-mentions.md) 與 [附件 API](attachments.md)。
