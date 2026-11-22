# 瀏覽器資料層、通知與 Markdown 顯示

> 本稿對應當前通知機制。變更資源使用 parent_conversation_id 表示父對話，見 [父子 conversation 設計](server-conversation.md#父子對話) 一併替換。

Server 網頁透過 `GET /api/browser/ws` 建立一條 WebSocket，僅接收資料變更通知。發消息、回覆問題、上傳文件及讀取具體內容仍走既有 HTTP API；此入口與設備領任務的 WebSocket 分開。

## 前端資料層

`server/frontend/src/services/` 按業務集中 HTTP 路徑、TanStack Query 查詢 key 和快取失效規則。`conversations.ts` 提供頂層與子會話列表、會話摘要、輪次及寫入操作。`runs.ts` 提供執行明細、事件分頁、完整事件下載、取消及回覆。共用的 `lib/api.ts` 處理請求、身份標頭和 HTTP 錯誤。

`hooks/use-browser-notifications.ts` 管理 WebSocket 連線、重連和身份切換。`services/browser-notifications.ts` 將通知路由到各業務定義的查詢範圍，並合併同一查詢的待刷新通知。會話寫入復用相同的會話查詢範圍。頂層或子會話列表有獨立的查詢 key，會話摘要及指定輪次則按會話 ID 查找。

畫面保存展開狀態、頁面游標和表單草稿，透過參數決定查詢是否啟用。伺服器資料的生命週期由 TanStack Query 管理，語言和 Acting as 等少量跨頁偏好使用現有的 React 訂閱及本機儲存。

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

`resource` 包含 `conversations`、`attachments`、`agents`、`skills`、`devices`、`users`。`id` 指對應資源，附件通知的 id 是 conversation ID；有父對話時附 parent_conversation_id。嵌套對話視圖使用 conversation 資源，不再有獨立 works 資源通知。通知不包含消息正文、附件內容、日誌、執行結果或憑證。

前端只重新讀取受通知影響且目前啟用的查詢。對話變更更新該對話摘要、已打開的執行詳情及其所在的頂層或子對話列表。執行中的 `log`、`message` 事件只保存執行歷史，通知帶有 `detail_only: true`。收到這類通知時，只重新讀取該對話中目前展開的執行事件頁。執行詳情在打開時載入，事件按 ID 游標每頁讀取 20 條。附件變更只更新該對話文件列表。遞歸刪除提交後通知被刪除節點及相關列表，刪除通知使用刪除前的父對話 ID。Agent 回覆和 run detail 的輸出使用 Markdown 顯示，支援 GFM 表格、清單、連結及程式碼區塊；使用者消息仍顯示原文。停用原始 HTML，保留安全 URL 過濾，不使用 dangerouslySetInnerHTML。

## 合併、斷線與邊界

Service 在交易提交成功後發布通知；回滾不發布。設備的 accepted、message、request、result、log 事件及在線狀態同樣觸發通知，不依賴使用者發出 HTTP 請求。子對話結果及輪次結束、父對話的 child_conversation 消息分別在提交後通知各自對話及相關列表。父通知提交後也使來源輪次的送達狀態查詢失效。父消息以子會話圖標展示來源與輪次，連結打開固定輪次的只讀消息及執行範圍；文件仍從子對話文件管理器查看，不顯示為父對話新上傳的附件。按輪次通知由 conversationService 完成；瀏覽器通知本身不生成消息或啟動 Agent。

每條連線將 250 毫秒內同一資源／ID 的變更合併為一項。完整的對話變更涵蓋只涉及詳情的變更。已保存的執行事件重送時照常確認，但不再發佈變更。查詢還在進行時收到的通知，合併到該次查詢完成後的下一次讀取。每條連線的待送項目有數量上限；溢出時合併為 `{"resource":"reset"}`，要求前端重新讀取啟用的查詢。慢連線的寫入會超時關閉，不阻塞資料提交，也不無限緩存日誌。

前端移除固定 HTTP 輪詢。WebSocket 斷線後以退避方式重連，收到 ready 後補讀；失效登入透過 HTTP 確認後返回登入頁。ping/pong 僅維持連線，不定時查詢所有資源。不新增 ACK、通知資料表、事件重放或另一套資料同步協議，資料庫仍是事實來源。

目前通知後仍讀取既有 HTTP 快照；此變更消除無變化時的輪詢，不宣稱已將會話詳情改成增量 API。Server 重啟不保留通知佇列，瀏覽器重連補讀即可恢復。

## 源碼職責

server/internal/notification 的 Broker 只分發變更提示，使用明確的訂閱者 ID、管理員標記及發佈者提供的所有者範圍，不依賴 SQL、database 或 service。用戶變更由 user.Service 以僅管理員範圍發佈；其他資源由所屬服務發佈，對話包含所有者、創建者及父對話引用。設備相關對話由 conversation.Service 根據消息的固定目標路由查詢並發佈。HTTP/WebSocket 層消費通知，不參與業務消息或輪次生成。所有變更仍在業務事務成功提交後發佈。

## Run 與輸入展示

對話時間線只渲染每個真實 run 的一個可點擊入口，不按消息目標重複渲染 completed 狀態標籤。Run 詳情集中顯示 run_inputs 的受理輸入列表，可展開原消息、附件與 input_id；補充輸入和子會話通知的受理關係只在此處查看。未受理的錯誤保留在來源消息，子會話通知內的錯誤隨通知折疊。
