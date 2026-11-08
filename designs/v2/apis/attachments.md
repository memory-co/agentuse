# attachments

> 會話附件設計見 [designs/attachment](../designs/attachment.md)，表結構見 [tables/attachments](../tables/attachments.md)。

附件屬於 session，創建後內容不可變，不關聯某條 message。全部接口先校驗會話權限，再校驗附件屬於這個會話。用戶、admin、daemon 密鑰、工作令牌的範圍見設計文檔；下面的下載路徑也需要認證。

## attachment 對象

```json
{
  "id": "at_51e76a8b1a1e45c0",
  "session_id": "ss_b83a324345de1f46",
  "filename": "report.pdf",
  "content_type": "application/pdf",
  "size_bytes": 204800,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "created_by": "u_10086",
  "source": "daemon",
  "daemon_id": "dm_5c1c8602536e55cb",
  "created_at": 1792800105,
  "download_path": "/api/sessions/ss_b83a324345de1f46/attachments/at_51e76a8b1a1e45c0/content"
}
```

`sha256` 是文件原始字節的 64 位小寫十六進制摘要，示例值僅表示格式。`source` 爲 `user` / `daemon` / `work`；`created_by` 是執行請求的用戶身份，daemon 上傳時爲 daemon 主人，工作令牌上傳時爲工作主人。`daemon_id` 僅 daemon 來源且 daemon 仍存在時出現。`download_path` 是相對服務根路徑，客戶端拼接自己的服務地址，響應不包含磁盤路徑或認證令牌。

## POST /api/sessions/{id}/attachments

`Content-Type: multipart/form-data; boundary=…`，恰好一個 `file` 字段，文件名取該 part 的 filename。可選文本字段 `filename` 覆蓋展示文件名。用 `dk_` 時必須帶當前執行的 `X-Agentuse-Run` 與 `X-Agentuse-Attempt` 請求頭。重複字段、多個文件、未知字段、缺文件返回 400；空文件允許。

```bash
curl -H 'Authorization: Bearer ut_…' \
  -F 'file=@./report.pdf' \
  https://agentuse.example.com/api/sessions/ss_xxx/attachments
```

成功返回 201 和 attachment 對象，`Location` 指向元數據接口。上傳不創建消息、不觸發派活，session 的任何狀態都允許用戶或工作令牌上傳；daemon 讀取與上傳受當前 run/attempt 和租約約束。大小和會話配額見設計，失敗不產生可見附件。同一文件重傳會創建新 id。

## GET /api/sessions/{id}/attachments

返回 200：

```json
{ "attachments": [ { "id": "at_…", "session_id": "ss_…", "…": "…" } ], "next_cursor": null }
```

按 `(created_at, id)` 升序。`limit` 默認 100，範圍 1–100；`cursor` 爲上一頁返回的不透明遊標，非法或跨會話遊標返回 400。沒有下一頁時 `next_cursor` 爲 null；空列表爲 `[]`。分頁不是快照，期間新增、刪除可能改變列表，daemon 按本次取到的清單執行，下一輪重取。

## GET /api/sessions/{id}/attachments/{attachment_id}

返回 200 和 attachment 對象。

## GET /api/sessions/{id}/attachments/{attachment_id}/content

返回 200 和二進制原文，帶 `Content-Length`、檢測出的 `Content-Type`、`Content-Disposition: attachment`、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`。第一版不要求 Range / 斷點續傳，客戶端校驗完整大小和摘要。瀏覽器使用登錄 cookie 下載；腳本帶 Bearer token，不把令牌放在 URL。

## DELETE /api/sessions/{id}/attachments/{attachment_id}

用戶或本工作令牌可以刪除，成功返回 204；不存在或已經刪除返回 404。daemon 密鑰調用返回 403。允許在會話執行中刪除；已經下載到 daemon 的副本不會被遠程刪除。刪除 session 或 work 會級聯刪除附件元數據並異步回收文件。

## 錯誤

沿用 `{"error": "說明"}`；鑑權和參數驗證失敗時也不返回附件內容。

| 狀態碼 | 場景 |
|---|---|
| 400 | 文件名或 multipart 字段錯誤、遊標錯誤、daemon 上傳缺少 message_id |
| 401 | 未認證、令牌/密鑰失效、工作令牌過期 |
| 403 | 身份不允許該操作，或工作令牌越出本工作 |
| 404 | 會話不可見、附件不存在或不屬於 URL 中的會話、daemon 不匹配 |
| 409 | 會話的 daemon 未聲明附件能力；daemon 的消息不在執行、租約失效；上傳提交前會話被刪除 |
| 413 | 文件、請求體或會話總配額超限 |
| 415 | 上傳不是 multipart/form-data |
| 500 | 文件寫入失敗、數據庫提交失敗、元數據存在但文件缺失 |

## 多 agent session

multi_agent 空會話可以先上傳文件，不要求先綁定 daemon。選執行目標時校驗 daemon 附件能力。每次執行讀取同一 session 當前附件列表。

元數據增加可選 created_by_run_id、created_by_agent_key，用於標識產物來自哪個執行和 agent。普通用戶上傳不填它們；daemon 和新工作令牌的作者由服務推導。

daemon 請求帶 Bearer dk_ 及 X-Agentuse-Run、X-Agentuse-Attempt；新模式不再僅憑 message_id 授權。執行結束、取消或租約失效後，讀取和上傳都拒絕；其他會話/其他 daemon 的執行返回 404。
