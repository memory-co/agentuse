# 對話文件與消息附件

每個 conversation 都有獨立的文件樹。點擊對話中的“文件”按鈕，會打開居中的文件管理器彈層；平時不佔用對話空間。桌面端頂部是可點擊的路徑欄，左邊顯示多級文件樹和搜索，右邊預覽所選圖片或文本。小屏幕使用同一彈層，以“瀏覽文件／文件預覽”切換列表與內容。

對話頁的右側留給協作模式的子會話列表，顯示標題與狀態，點擊即可進入；小屏幕通過“子會話”按鈕打開列表。單體對話不顯示子會話側欄。

```text
/
├── user_input/
│   ├── 截圖.png
│   └── specs/需求.txt
└── reports/
    └── daily/result.txt
```

用戶上傳的文件進入 `user_input/`。AI 產物使用自己的相對路徑，例如 `reports/daily/result.txt`。相同路徑的重複上傳會生成帶編號的新路徑，保留原文件。不同 conversation 的路徑相互獨立。

## 隨消息發送文件

1. 在消息輸入框點擊回形針按鈕，選擇本條消息的文件。發送前可以預覽和移除，也可以只發文件。
2. 點擊發送後，頁面先上傳尚未保存的文件，再提交本條消息選定的附件。
3. 消息下方顯示文件卡片及相對路徑，點擊即可打開文件管理器，自動定位該文件並預覽。下一條消息默認不攜帶之前的附件。
4. 複用已上傳文件時，在文件管理器中選中文件，點擊“添加到消息”。

新會話和後續消息使用相同流程。上傳或發送失敗時保留草稿和已成功上傳的文件，重試會複用這些文件。已經被消息引用的附件會保留到對話刪除時一起清理；未關聯消息的文件可以從文件管理器刪除。點擊“添加到消息”會關閉彈層並返回輸入框；關閉或按 Escape 則回到原先打開文件的按鈕，已有草稿不會丟失。

消息仍記錄本次選定的附件，Client 不提前下載。`context attachment list` 直接查詢整個 conversation 的 Server 附件，`context attachment download PATH` 按 conversation 相對路徑選擇文件，保存到 workspace/<relative_path>；先前或其他 run 的文件同樣可下載，不另設本地路徑映射。

## AI 輸出

AI 使用 `context attachment upload` 上傳產物。在 workspace 內上傳 `reports/result.txt` 時，會保留這個相對路徑；只接受 workspace 內的文件。Server 因重名而分配新路徑時，Client 也保存到相同路徑。`user_input/` 專用於用戶輸入，AI 無法寫入該目錄。

上傳後文件出現在對應 conversation 的文件樹中；run 的最終回覆會附上本次產物的文件卡片。上下文工具說明見 [Client context](../client/context.md)。

## API

上傳仍使用 `POST /api/conversations/{id}/attachments`，multipart 可提供 `path` 指定相對路徑；用戶路徑始終位於 `user_input/` 下。Attachment 返回 `relative_path`。

消息通過 `POST /api/conversations/{id}/messages` 的 `attachment_ids` 選擇文件：

```json
{
  "prompt": "請分析這張圖片",
  "to_agent_ids": ["image-helper"],
  "attachment_ids": ["at_示例"]
}
```

省略或空數組表示本條消息沒有附件。附件必須屬於當前 conversation，最多 64 個；重複 ID 去重。沒有文本時可以只提供附件。Message 的 `attachments` 保存所選文件的元數據快照，最終 AI 回覆同樣記錄該 run 的輸出文件。

當前使用數據庫 schema 8。不提供舊數據遷移或兼容讀取；升級時按項目約定重建數據庫，兩端一起更新。

目錄創建、移動、重命名等後續操作列在 [文件樹設計的 TODO](../../designs/v3/conversation-files.md#todo)。

附件字节统一使用 fileService 的 conversations/<conversation_id>/<relative_path>，与通用对话文件使用相同的本地/S3 后端。消息引用的元数据属于 conversation 业务；不再配置独立 attachments-dir。通用 PUT 无法覆盖已登记的附件，删除对话仅清理对应 conversations 前缀。
