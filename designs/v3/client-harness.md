# Client harnessService：harnessctl 命令與事件橋接

harnessService 只接收準備好的 UTF-8 文本、工作目錄、適配器和固定事件地址。它不接收 caller run/attempt 身份，不生成執行 UUID，不推斷主子對話或其他業務角色。

## 適配器包與啓動快照

```text
~/.agentuse/harness/<name>/<revision>/
  manifest.json
  harnessctl
  EVENT.md
  event.schema.json
  lib/...
```

內置包從 client 二進制釋放；自定義 <name>/local/ 優先，local/disabled 可禁用。啓動時校驗協議、入口、平臺及聲明的依賴，固定可用性和內容版本；local 包複製到內容版本目錄，防止運行中編輯改變隨後執行的代碼。修改包或依賴配置後重啓 client，不提供重新探測或熱切換。

AgentUse 來源註冊名沿用 codex、claude，獨立包分別爲 codex、claudecode。每個獨立包可單獨分發，不依賴 AgentUse Go 模塊。當前內置包需要 Linux、Python 3.9+ 和已配置的原生 CLI。

## 五個命令（harnessctl/3）

```text
harnessctl init <events.jsonl> [--cwd DIR] [--model MODEL]
                [--agents-md FILE] [--skills-dir DIR ...]
harnessctl send <events.jsonl>
harnessctl reply <events.jsonl> <request_id>
harnessctl cancel <events.jsonl>
harnessctl status <events.jsonl>
```

init 綁定配置並持久化資源快照，不啓動原生進程或模型。驅動先調用 init --cwd，再通過 stdin 向 send 提交正文；reply 的答案也走 stdin，不通過 shell 拼接。首次 send 自動建立原生上下文；後續 send 自動複用。client 不調用 start、resume、steer 或 queue。

Client 的 session API 使用 send/reply/cancel，status API 返回公開狀態。Server 的 prompt/reply/cancel 只傳業務輸入與控制，不選擇原生投遞策略。

外部指令和技能由適配器的 init 統一接收：--agents-md 指令快照、可重複的 --skills-dir 技能根目錄保存在 events.jsonl.d/resources/，不寫入共享 workspace。原始資源刪除後仍可恢復相同快照；改配置使用新的事件地址。完整約定見 [harnessctl 協議](../../harnessctl/README.md)。當前 client 自動調用 init 並傳入固定版本的模型、指令和技能；本地調試頁面也支持模型、AGENTS.md 內容和本機 skill ZIP 路徑。Server Agent 版本接入已實現：runService 從 server skillService 的版本下載接口獲取並解包固定 skill 包，sessionService 按 agent_version_id 選擇新事件地址，harnessService 接收準備好的指令路徑、模型和技能目錄後調用 init，不理解 skill 發佈或 latest。

## 五種公共事件

| 類型 | 含義 |
|---|---|
| accepted | 完整輸入、input_id 和適配器確認的 run_id；據此登記執行 |
| message | 以 message_id 標識的完整正文快照 |
| request | question/permission 的 pending/replied/expired 完整狀態 |
| result | 一個 run 的終態、input_ids、正文和計量 |
| log | 可丟的進度與診斷，不參與關鍵業務判斷 |

前四種可靠保存。所有字段和狀態約束以倉庫 harnessctl/EVENT.md、event.schema.json 爲準。Client 只解析這些公共記錄，不解釋原生 Codex RPC、Claude control_request、hook 或 .d/ 私有文件。

run_id、input_id、request_id 都屬於適配器。Codex 確認的補充輸入可以進入同一 run；完成競態可能轉成新 run。Claude 忙時通過 priority=next 向原生提交，按消費回執將補充輸入歸入同一 run；在上一輪結束後才消費時產生新 UUID。send 等待消費邊界期間，client 繼續讀取事件並處理 reply/cancel。client 只觀察結果，不預言或要求某一種歸屬。

## 驅動與失敗處理

驅動在內存中串行處理 session 的輸入、回覆和取消，讀取完整 JSONL 行；末尾半行留待下次讀取。每個 accepted 都交給相同的 run 索引機制。驅動窗口結束可以包含多個 run，它不是額外的執行身份，也沒有自己的 UUID。

回覆使用 request_id。權限答案是 allow/deny；問題答案是 question id 到文本或文本數組的 JSON 對象。無效 reply 返回錯誤，不能因此悄悄終止仍在等待回答的交互。

取消後使用公開 status 確認沒有前臺工作，確認失敗則保留目錄佔用。啓動恢復也使用公開命令，不通過私有 PID 或原生 ID 猜測狀態。後臺運行進程的啓動、空閒退出和恢復完全由適配器決定。

## 權限與輸入來源

內置 Codex 創建和恢復時使用 approvalPolicy=never、sandbox=danger-full-access；Claude 使用 bypassPermissions。普通操作不經過額外確認，真實問題及原生例外權限請求才經 reply。認證、組織策略或執行失敗不靠僞造批准繞過。

本地輸入按用戶給出的文本或 JSON 文本發送。遠端消息上下文由 Server 準備，主、子 conversation 使用同一輸入鏈路；harnessService 不追加 Work 或協調角色提示詞。

每次輸入來源的環境可能不同，適配器只存環境摘要；空閒時可內部重建進程並恢復上下文，讓新憑據生效，忙時拒絕更換環境。這是每次調用的執行上下文，不是配置熱加載。

啓動恢復時，runService 根據 sessionService 的元數據提供需要恢復的 harness、revision 和事件地址清單，harnessService 只通過對應適配器確認取消。harnessService 不查詢 session.yaml、不遍歷業務目錄，也不依賴 sessionService；包安裝使用 fileutil 的通用路徑與文件工具。
