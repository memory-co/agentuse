# fileService：按命名空間存儲文件

> 通用 fileService、本地/S3 後端與 conversation/skill 文件入口均已實現。fileService 無數據庫表；逐消息附件關聯的元數據屬於 conversationService，文件字節統一走 conversations 命名空間。

## 職責

fileService 統一封裝本地文件系統與 S3 協議存儲。調用方提供**命名空間、相對路徑和文件流**；fileService 負責完整寫入、讀取、列舉及刪除，業務 service 負責對象歸屬、授權、版本和清理時機。

```text
Put(namespace, path, stream, false)  普通完整寫入，可按業務要求覆蓋
Put(namespace, path, stream, true)   不存在時創建，已有路徑不覆蓋
Get(namespace, path)                  返回文件流
List(namespace, prefix)               列舉某一前綴
Delete(namespace, path)               刪除一個對象
DeletePrefix(namespace, prefix)       清理一個業務空間
```

fileService 不新增全局 files/attachments 元數據表。存儲後端是字節內容和存在性的事實來源。AgentUse 不用一個 conversation_id 參數硬套所有文件，更不把 skill 包僞裝爲某個 conversation 的附件。

## 實現分工

fileService 對應 `server/internal/service/file.Service`，本地和 S3 後端都位於 `service/file/`。Conversation 的文件及附件業務歸入獨立的 `service/conversation/` 包，由 `conversation.Service` 編排，並持有自己的文件鎖與附件限制；這些文件不另建 attachmentService，也不實現第二套存儲後端。根 `service.Service` 只組裝並配置這些服務，完整分工見 [Server 當前源碼組織](server-main.md#四當前源碼組織)。

| 源碼 | 所屬職責 |
|---|---|
| `service/file/service.go`、`service/file/s3.go` | 通用字節存儲、命名空間邊界及本地/S3 差異 |
| `service/conversation/files.go` | Conversation 普通文件的授權讀取、寫入策略與整個文件空間的清理 |
| `service/conversation/attachments.go` | 附件訪問權限、容量限制及元數據事務 |
| `service/conversation/attachment_files.go` | 附件發佈、元數據與字節提交協調、失敗補償及刪除 |
| `service/conversation/attachment_paths.go` | 重名分配與已登記附件路徑保護 |
| `service/conversation/message_attachments.go` | 消息附件快照和執行產物到回覆的關聯 |

HTTP 普通文件讀取通過 `OpenConversationFile`，寫入通過 `PutConversationFile`；業務入口先校驗 conversation 權限，再調用 fileService。附件額度由 `ConfigureConversationAttachments` 設置，普通文件寫入、附件發佈和清理共用 conversation 的文件協調鎖。SQL、設備/run 授權、配額及消息引用均留在 conversation 業務層，fileService 只接收命名空間、相對存儲路徑和字節流。

## 命名空間與目錄

| 命名空間 | 業務擁有者 | 相對路徑 | 寫入約束 |
|---|---|---|---|
| conversations | conversationService | `<conversation_id>/<file_path>` | 普通文件可覆蓋；已登記的消息附件不可經通用 PUT 覆蓋 |
| skills | skillService | `<skill_id>/<skill_version_id>/skill.zip` | 每個版本一個獨立路徑，發佈後只讀 |

本地後端：

```text
<root>/
  conversations/<conversation_id>/inputs/data.csv
  conversations/<conversation_id>/reports/result.pdf
  skills/<skill_id>/<skill_version_id>/skill.zip
```

S3 後端：相同 key 前加可選 prefix，存入配置的 bucket；例如 `agentuse/skills/<skill_id>/<skill_version_id>/skill.zip`。命名空間來自 server 業務代碼，不由上傳請求隨意指定以越權訪問其他空間。

本地 root、S3 endpoint/bucket/prefix/憑據屬於部署配置。業務表只存命名空間內的相對路徑，client 使用業務下載 API，不感知實際後端或持有存儲憑據。

## 文件能力與業務規則

| FileService 負責 | 業務 Service 負責 |
|---|---|
| 流式存儲、讀取及後端差異 | 誰能上傳、下載哪個業務對象 |
| 普通寫入和不存在時創建 | 哪些路徑可覆蓋，哪些版本發佈後不可變 |
| 原子可見的完整對象；失敗不暴露半個文件 | Skill ZIP 校驗、SHA-256、版本號和依賴關係 |
| 路徑邊界及文件系統鏈接限制 | Conversation 刪除、skill 版本保留、無引用對象清理 |

路徑不接受絕對路徑、.. 或通過符號鏈接越出指定空間。本地寫入先寫臨時文件並完成落盤後提交；S3 只有完整上傳成功才發佈對象。PutIfAbsent 必須保留“已存在則不覆蓋”的語義，不能用不受保護的先查後寫冒充。

fileService 不解析 SKILL.md，不解壓技能包，不知道 agent 當前版本，也不決定一箇舊文件是否可刪除。壓縮包摘要和大小存於 skill_versions，是 skillService 的版本校驗信息，不是 fileService 新建的元數據賬本。

## HTTP 入口由業務對象定義

```text
PUT /api/conversations/{conversation_id}/files/{path...}
GET /api/conversations/{conversation_id}/files/{path...}

POST /api/skills
POST /api/skills/{skill_id}/versions
GET /api/skills/{skill_id}/versions/{version_id}/package
```

Conversation 入口先經 conversationService 校驗歸屬，再操作 conversations 空間。附件 API 保留 ID、relative_path、大小與摘要等消息引用元數據，字節使用 conversations/<conversation_id>/<relative_path>，不再使用獨立 attachments 存儲目錄。通用 PUT 與附件發佈共享寫入邊界，拒絕覆蓋已登記附件。設備寫入需提供活動 run 的 X-Agentuse-Run，AI 不能寫 user_input/；詳見 [conversation-files.md](conversation-files.md)。

Skill 上傳經 skillService 校驗、分配不可變版本路徑，並調用 fileService。下載同樣由 skillService 校驗用戶或已分配輸入的設備權限，然後返回文件流；不開放任意覆蓋已發佈 skill 包的通用 PUT 接口。

## 發佈、刪除與異常清理

上傳 Skill 時先寫新對象，確認完整後再提交版本表。SQLite 與文件後端沒有跨系統事務；數據庫提交失敗時刪除本次候選對象，異常中斷可能留下未引用文件。當前未啓用自動孤立對象清理。

刪除 conversation 只清理 conversations 下該 conversation 的前綴，不清理 skills。軟刪除 agent/skill 不刪除已發佈且仍被版本引用的包。Skill 清理必須由 skillService 檢查全部歷史版本和正在發佈的對象後進行，不能只保留 latest。沒有顯式版本淘汰流程時，只清理確認未發佈且無版本記錄引用的孤立對象，不刪除任何已發佈 skill_version 對應的包。

文件刪除與 SQLite 刪除不能當成一件原子操作。業務 service 保留錯誤、重試或補償清理的責任，fileService 提供可重複執行的刪除原語，不維護另一套業務數據庫。

## 表結構

無。新增命名空間不需要新增 fileService 表；業務元數據由擁有它的 service 管理。

[Server 總覽](server-main.md) · [skillService](server-skill.md) · [agentService](server-agent.md) · [conversationService](server-conversation.md) · [runService](server-run.md)

## 啓動配置

默認使用本地 `~/.agentuse/files`，支持 `-files-dir` / `AGENTUSE_FILES_DIR`。選擇 S3：

```bash
agentuse-server start -files-backend s3 \
  -s3-endpoint https://s3.example.com -s3-bucket agentuse \
  -s3-region us-east-1 -s3-prefix production
```

憑據讀取 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY；參數對應 AGENTUSE_FILES_BACKEND、AGENTUSE_S3_ENDPOINT、AGENTUSE_S3_BUCKET、AGENTUSE_S3_REGION、AGENTUSE_S3_PREFIX 環境變量。部署配置在啓動時讀取，修改後重啓。S3 兼容後端須支持條件 PutObject（If-None-Match: *）；不支持時發佈失敗，不降級覆蓋。

Conversation 刪除提交後清理對應文件前綴；失敗寫入 server 日誌，當前由運維重試清理，不提供另一份文件元數據賬本。Skills 前綴不隨 conversation 刪除。

實現使用 Go 1.25 的 os.Root 原子 Link/Rename，在限定根目錄內發佈文件；項目最低 Go 版本相應爲 1.25。
