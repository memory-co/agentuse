# 瀏覽器通知與 Markdown 顯示

> 本稿的通知傳輸機制已實現。下面按新的父子 conversation 目標模型調整資源字段；目前代碼仍有獨立 Work 通知，需隨 [父子 conversation 設計](server-conversation.md#父子對話) 一併替換。

Server 網頁透過 `GET /api/browser/ws` 建立一條 WebSocket，僅接收資料變更通知。發消息、回覆問題、上傳文件及讀取具體內容仍走既有 HTTP API；此入口與設備領任務的 WebSocket 分開。

## 身份與權限

瀏覽器使用既有 HttpOnly 登入 cookie；不把 token 放進 URL。管理員可用 `?as=<user_id>` 指定 Acting as，沿用 HTTP 的身份切換規則。切換身份或登出時，前端關閉舊連線；通知的權限不會沿用到新身份。Server 拒絕跨來源連線，此入口只接受瀏覽器用戶身份。

一般用戶只收到自己有權讀取的資源通知；管理員未切換身份時可接收全部，Acting as 後只接收該用戶範圍。會話依 `user_id`／`created_by` 判斷，與原 HTTP 權限一致。通知仍不授予讀取權限，實際 GET 會再次檢查。Server 在發送通知及定時 ping 前重新驗證登入令牌，失效時關閉連線。

## 訊息

連線訂閱完成後送出：

```json
{"type":"ready"}
```

前端此時重新讀取目前啟用的查詢，包含首次連線、重連及切換身份。先訂閱再送 ready，避免初始快照與訂閱之間漏掉變更。

資料提交成功後送出：

```json
{"type":"changed","changes":[{"resource":"conversations","id":"child_conversation_uuid","parent_conversation_id":"parent_conversation_uuid"}]}
```

`resource` 包含 `conversations`、`attachments`、`agents`、`skills`、`devices`、`users`。`id` 指對應資源，附件通知的 id 是 conversation ID；有父對話時附 parent_conversation_id。Work 視圖使用 conversation 資源，不再有獨立 works 資源通知。通知不包含消息正文、附件內容、日誌、執行結果或憑證。

前端依資源精確使相關查詢失效，只重新讀取目前啟用的查詢。對話變更更新該對話詳情及其所在的頂層或子對話列表；附件變更只更新該對話文件列表。遞歸刪除提交後通知被刪除節點及相關列表，刪除通知使用刪除前的父對話 ID。Agent 回覆和 run detail 的輸出使用 Markdown 顯示，支援 GFM 表格、清單、連結及程式碼區塊；使用者消息仍顯示原文。停用原始 HTML，保留安全 URL 過濾，不使用 dangerouslySetInnerHTML。

## 合併、斷線與邊界

Service 在交易提交成功後發布通知；回滾不發布。設備的 accepted、message、request、result、log 事件及在線狀態同樣觸發通知，不依賴使用者發出 HTTP 請求。子對話的回覆只改變該對話及列表展示；不因通知而向父對話生成消息或啟動 Agent。

每條連線將 250 毫秒內同一資源／ID 的變更合併為一項。每條連線的待送項目有數量上限；溢出時合併為 `{"resource":"reset"}`，要求前端重新讀取啟用的查詢。慢連線的寫入會超時關閉，不阻塞資料提交，也不無限緩存日誌。

前端移除固定 HTTP 輪詢。WebSocket 斷線後以退避方式重連，收到 ready 後補讀；失效登入透過 HTTP 確認後返回登入頁。ping/pong 僅維持連線，不定時查詢所有資源。不新增 ACK、通知資料表、事件重放或另一套資料同步協議，資料庫仍是事實來源。

目前通知後仍讀取既有 HTTP 快照；此變更消除無變化時的輪詢，不宣稱已將會話詳情改成增量 API。Server 重啟不保留通知佇列，瀏覽器重連補讀即可恢復。
