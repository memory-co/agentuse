# Sessions API

Session 保存共享消息與附件，屬於創建者，不綁定 agent、daemon 或 harness。所有會話使用 `multi_agent` 模型。

## POST /api/sessions

```json
{"title":"評審","prompt":"檢查改動","to_agent_ids":["coder","reviewer"]}
```

title、prompt/input、to_agent_ids、to_user_ids 都可省略，`{}` 創建空會話。mode 可省略；若提供，只接受 `multi_agent`。不接受 agent_id 綁定。工作線程只能使用對應工作令牌創建。

to_agent_ids、to_user_ids 都是 ID 字符串數組，省略時為 []。兩組可同時指定；只有 to_agent_ids 創建執行關係，只有用戶收件人時只保存消息，兩組為空時保存備註。用戶收件人必須已具有會話讀取權限；收件人不會改變權限。重複 ID 去重，任一收件人無效則整個請求失敗。響應 message 始終包含兩組數組；只接受新字段，不提供舊字段兼容。成功返回 201，以下是無收件人的備註示例：

```json
{"session":{"id":"c41455e2-8d30-4b21-9665-c30b41d97ec4","mode":"multi_agent","status":"idle"},"message":{"id":"33b960b2-7c4a-428c-8564-3862f4bbed08","seq":1,"to_agent_ids":[],"to_user_ids":[],"content":{"prompt":"備註"}},"runs":[],"deliveries":[]}
```

空會話只返回 session；帶首條消息時同時返回實際的 message、runs 和 deliveries。

## POST /api/sessions/{id}/messages

```json
{"prompt":"補充要求","to_agent_ids":["coder","reviewer"]}
```

兩組收件人的規則與創建會話時相同。消息與全部 Agent 的 message_agents 在一個事務內提交，server 不預建 run。Client 通過 accepted 返回實際 run UUID，並決定補充消息屬於原 run 或後續 run。沒有 Agent 收件人時不執行；不從正文或上次消息推斷收件人。

成功返回 201 `{message,runs,deliveries}`。每次成功發送都創建新的消息 UUID，不按請求 key 或內容去重。收件人不存在或用戶無權讀取會話時返回 404；空 ID、字段類型錯誤、未知字段或運行時不可用返回 400。

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
