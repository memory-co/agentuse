# 會話附件

> 已實現。附件需要服務端和 daemon 一起升級；daemon 通過 `attachments-v1` 聲明支持。

## 用途

在 session 這一級增加 attachment。用戶把需求文檔、圖片、數據上傳到會話，harness 讀取這些文件；harness 生成報告、代碼包、圖片後，用本機 `agentuse` 上傳，用戶在會話裏下載。附件屬於會話，不掛在某條消息上，也不放進消息的 JSON 或 WebSocket 裏傳輸。

同一會話中的每個附件有獨立 id，文件內容和元數據創建後不修改。同名文件可以共存，重新上傳得到新的 id。第一版不做跨會話共享、在線編輯、目錄上傳或斷點續傳；目錄由調用者先打包成一個文件。

## 一次完整交互

1. 用戶建會話、上傳輸入文件，再發消息讓 harness 處理。
2. daemon 領到消息後，取得附件清單、下載缺少的文件，放進會話工作目錄的 `.agentuse/attachments/<attachment_id>/<filename>`。
3. daemon 把附件 id、原始文件名和本地相對路徑交給 harness。文件都是普通文件，文本、圖片等能否理解由 harness 自己決定，服務不解析內容。
4. harness 生成文件後執行 `agentuse attachment upload ./report.pdf`。CLI 流式上傳，打印附件 id 和下載路徑。
5. 頁面輪詢附件列表，顯示上傳者、文件名、大小和下載按鈕。最終回覆可以引用下載路徑，文件即使不寫進最終回覆也會出現在附件列表裏。

上傳不自動發消息，不改變 session 的 `status`、`updated_at`，也不自動把 `resolved` 重新打開。文件落盤成功後纔會出現在列表中。

## 先建空會話

`POST /api/sessions` 發送 `{}` 即可創建 idle 空會話，上傳文件後，再用 messages 攜帶 target_agent_ids 開始執行。工作協調者也從空會話開始；線程使用 `agentuse work new --defer-start`，上傳後 `agentuse work send --agents <目標> <session> "任務"`。

## HTTP 和權限

接口統一放在 `/api/sessions/{id}/attachments` 下，完整請求、響應見 [apis/attachments](../apis/attachments.md)。

| 方法和路徑後綴 | 行爲 |
|---|---|
| `POST /attachments` | multipart 上傳一個文件，201 返回 attachment |
| `GET /attachments` | 分頁列出元數據 |
| `GET /attachments/{attachment_id}` | 取元數據 |
| `GET /attachments/{attachment_id}/content` | 下載原始文件 |
| `DELETE /attachments/{attachment_id}` | 刪除元數據並安排清理文件，204 |

會話主人、發起人和 admin 可以讀、上傳、刪除。支持網頁登錄 cookie、`ut_` 和 admin 的 `X-Agentuse-User`，與 session 一致；代爲用戶時按該用戶權限檢查。附件 id 必須屬於 URL 中的 session，看不到會話或附件一律 404。

客戶端上傳產物使用 daemon 已有的 `dk_`，無需把用戶登錄令牌傳給 harness。讀取和上傳都必須攜帶 X-Agentuse-Run、X-Agentuse-Attempt，請求對應正在本 daemon 執行且租約有效的 run。會話屬於別的 daemon 返回 404；本 daemon 的消息已結束或失去租約返回 409。提交元數據前再次檢查租約，上傳過程中失去租約不提交文件。daemon 身份不能刪除附件，不能訪問會話其他管理接口。刪除 daemon 後其密鑰立即失效。

`wt_` 增加附件讀、上傳、刪除權限，範圍僅限本工作的協調者和線程；超出範圍返回 403，過期返回 401。工作令牌不能擴大到 agent 主人的其他會話。工作刪除時所有會話附件一起刪除。

第一版對上傳和下載使用普通 HTTP。WebSocket 繼續只傳派活、心跳和事件，文件傳輸不阻塞心跳。

## 存儲和一致性

元數據存 [attachments](../tables/attachments.md) 表，二進制放服務本地磁盤。新增 `agentuse-server start -attachments-dir` / `AGENTUSE_ATTACHMENTS_DIR`，默認 `~/.agentuse/attachments`，不能放在公開靜態資源目錄下。數據庫與這個目錄必須一起備份、恢復和遷移；第一版不接對象存儲。

磁盤路徑用 `<root>/<session_id>/<attachment_id>/content`，由服務生成並驗證 id，不拼接用戶文件名。上傳使用流式讀寫和 SHA-256 校驗，默認單文件 100 MiB、每會話總計 1 GiB（含輸入與產物）；新增 `-attachment-max-bytes` / `AGENTUSE_ATTACHMENT_MAX_BYTES`、`-attachment-session-max-bytes` / `AGENTUSE_ATTACHMENT_SESSION_MAX_BYTES`，必須是正整數。請求體上限額外預留 1 MiB 給 multipart 邊界和字段。

上傳順序：在同一文件系統寫臨時文件並計數、算摘要 → fsync 並關閉 → 開數據庫寫事務，重新檢查會話、身份、租約和會話配額 → 原子 rename 到最終路徑 → 插入元數據並提交。失敗時回滾、清理臨時或最終文件。併發上傳在事務內串行覈算配額，不能各自檢查後一起超額。超過大小或配額返回 413。

崩潰可能留下沒有元數據的文件，但不會提前暴露未寫完的附件。啓動和定期清理超過 24 小時的臨時文件、未被元數據引用的最終文件；清理任務與本進程上傳共用互斥鎖，避免刪到尚未提交的文件。第一版一個數據目錄只由一個服務進程寫入。下載時記錄存在但文件缺失，返回 500 並記錄告警，不僞裝成不存在。

刪除先在事務中刪除元數據，再清理文件；清理失敗由上述任務補償。刪除會話時外鍵級聯刪元數據，文件異步清理。附件刪除後不再能下載；已經下載到 daemon 的副本不做遠程擦除，備份也遵守各自保留策略。

文件名取上傳文件名的 basename（同時識別 `/` 和 `\`），拒絕空名、`.`、`..`、控制字符及超過 255 UTF-8 字節的名字。保存原始文件名僅用於展示和下載響應頭。服務檢測前 512 字節得到 `content_type`，無法判斷用 `application/octet-stream`，不信任客戶端聲明。所有內容下載使用 `Content-Disposition: attachment`（編碼後的 `filename*`）、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`；鑑權後纔打開文件，不提供免登錄公開鏈接。頁面不直接執行或內嵌 HTML、SVG 等上傳內容。

## daemon 和 harness

daemon 每次執行一條消息前，通過 HTTP 用自己的 `dk_` 讀取該 session 的完整附件列表，按列表中的 id 下載並驗證大小、SHA-256。附件緩存文件先寫臨時路徑，校驗成功後原子 rename；失敗就回報本輪 `failed`，不啓動 harness。重試複用校驗通過的文件，不僅憑文件名判斷命中。不跟隨目標目錄中的符號鏈接，防止附件路徑寫出會話工作目錄。

附件清單以這次執行前拉取的結果爲準。執行中新增文件不自動塞進正在運行的 harness，用戶應下一輪引用它；本輪急需的補充可插話告知，並由 CLI 顯式下載。附件在拉清單後被刪除、下載返回 404 時，本輪失敗並說明附件已刪除，下次執行重新取清單。已經緩存的文件即使被服務端刪除也可能仍在本地，但之後不再寫進附件清單。

daemon 在自己生成的 harness 上下文中加入附件 id、文件名、本地相對路徑和 CLI 用法，不修改用戶的原始 `input` 或工作說明。Claude / Codex 沿用現有說明文件機制，和工作說明合併生成；自定義 harness 在 stdin 的運行上下文增加 `attachments` 數組。附件名是數據，不能作爲說明指令拼接。目錄固定保留給 daemon，harness 不應寫入。

給每個 harness 進程設置 `AGENTUSE_SERVER`、`AGENTUSE_SESSION`、`AGENTUSE_MESSAGE_ID`，PATH 前面加上 agentuse 可執行文件目錄；已有協調者的 `AGENTUSE_WORK` 行爲保持不變。CLI 的 daemon 配置路徑也由 daemon 顯式傳入 `AGENTUSE_CONFIG`，避免 daemon 使用自定義 `-config` 時 CLI 讀錯密鑰。環境中不增加用戶令牌或 daemon 密鑰明文，CLI 從配置讀取密鑰。

## agentuse 客戶端

```bash
# harness 內，默認當前 session；路徑相對 harness 當前工作目錄
agentuse attachment upload ./report.pdf
agentuse attachment upload ./report-v2.pdf --name report.pdf --json
agentuse attachment list --json
agentuse attachment download at_xxx --output ./input.csv

# 人在終端使用用戶令牌；--session 顯式指定會話
agentuse attachment upload ./requirements.pdf --session ss_xxx --server https://agentuse.example.com --token ut_…
```

命令的完整約定見 [cli/agentuse](../cli/agentuse.md#attachment)。單次上傳一個普通文件；拒絕目錄，不自動壓縮。上傳和下載都流式處理，不把整個文件讀進內存。成功後純文本輸出 id、文件名、大小及服務端返回的相對下載路徑；`--json` 直接輸出 API 對象，錯誤寫 stderr，非零退出。客戶端不自動無限重試 POST，超時可能已經上傳成功，提示先 list 檢查；第一版不保證上傳冪等。

## 頁面與 SDK

頁面在 session 詳情提供附件列表、上傳、下載、刪除，上傳過程顯示進度，失敗顯示服務端錯誤。新會話選擇文件時先建空會話，全部上傳成功才發送第一條消息；失敗保留空會話和已經上傳的附件供重試，不偷偷派活。已有會話上傳不自動發送消息；運行中上傳提示“下一輪可用”。第一版不要求在消息 Markdown 中解析特殊附件語法。

Python、Node、Go SDK 都增加 `Attachment` 類型和 list/upload/get/download/delete 方法，仍支持按網站用戶身份調用。upload 接受文件流、文件名，生成 multipart，不走只支持 JSON 的現有 helper；download 返回可關閉的響應流，調用方決定存到哪裏。保留現有錯誤結構，新增 413 / 415 / 409 等狀態的透傳。CreateSession 的可選參數增加 `defer_start`，對應返回的 `message` 可缺省。

## 執行能力

用戶可直接向空 session 上傳，不要求綁定機器。創建 run 時驗證目標 daemon 的 session-runs-v1 能力；含附件時另外要求 attachments-v1。所有 daemon 文件操作都校驗當前 run/attempt 及租約。

## 實現與驗收

1. 表和文件存儲層：目錄配置、流式寫入、限額、摘要、刪除補償，使用當前全新 schema 初始化。
2. HTTP：上傳、列表、元數據、下載、刪除；擴展用戶、daemon、工作令牌的權限；增加空會話創建。
3. CLI 與 daemon：multipart 上傳、下載、執行前同步、上下文和環境變量；CLI 能使用現有自定義配置路徑。
4. 前端和三個 SDK；更新內置 harness 的交付說明，要求文件產物上傳後再在最終回覆引用下載路徑。
5. 用 fakeharness 做端到端驗證：空會話 → 用戶上傳 → daemon 校驗並讀取 → harness 調 CLI 上傳產物 → 用戶下載，逐字節驗證內容。

測試覆蓋跨用戶、跨會話和跨 daemon 越權，工作令牌範圍與過期，上傳期間租約失效或會話被刪除，同名文件、空文件、惡意文件名、符號鏈接、超限和併發配額，磁盤寫入/提交失敗、崩潰殘留清理、刪除補償，以及下載中斷後的 CLI 錯誤。執行測試覆蓋 capability、租約和 attempt 變化。
