# Client sessionService：固定事件地址與工作目錄

一個 client session 對應一個固定的 events.jsonl 地址。原生 Codex thread 或 Claude session 的身份及恢復狀態封裝在 harnessctl 內，client 不保存也不選擇原生 ID。

## 關係

```text
endpoint / conversation
  workspace/                       所有本機 session 共用的 cwd
  sessions/<client_session_uuid>/
    session.yaml
    events.jsonl                   傳給 harnessctl 的唯一會話地址
    events.jsonl.d/                適配器擁有
    runs/<adapter_run_uuid>.jsonl  一個實際交互的引用
```

一個 conversation 可以有多個 Codex、Claude Code 或自定義 harness session，並可以同時在同一 workspace 中工作。工作文件共享，原生上下文各自獨立，不自動獲得併發文件事務。

一個 session 可以包含多個實際 run；run UUID 必須來自 accepted。同一次交互中的追加輸入可能沿用該 UUID。Session 與進程不是同一實體：適配器空閒退出後仍可根據同一事件地址恢復上下文。

## 元數據

```yaml
id: 7877d33b-788f-4f6c-9ff0-987c2128064e
endpoint_id: local
conversation_id: 550e8400-e29b-41d4-a716-446655440000
agent_id: local-codex
harness: codex
harness_revision: package-revision
status: idle
created_at: 1793443200000
last_used_at: 1793443205100
updated_at: 1793443205100
```

id 只是目錄定位用的 client UUID，不傳給原生 CLI 充當其 session ID。路徑來自啓動配置和 endpoint/conversation，不冗餘保存 work_root、目錄映射、原生 ID 或 server 歷史遊標。時間爲 Unix 毫秒。

## 選擇、佔用與控制

當前實現按 endpoint/conversation/agent/agent_version/harness/revision 選擇最近的有效空閒 session。顯式向指定 session send 時使用該地址；不能因其忙碌就換成另一個 session 或自行決定新 run ID。

同一個受管 session 由一個驅動處理 send/reply/cancel，追加輸入交給適配器裁定插話或排隊。全部輸入有結果並確認前臺工作停止後，session 才恢復 idle。活動階段爲 running，問題是否等待回答從公共事件或 status 獲取，不維護重複的問題賬本。

包版本不同或歷史不可恢復時，不靜默更換既有 session 的適配器和事件地址。新會話創建新目錄；舊引用仍可讀取，顯式恢復不可用版本時返回錯誤。原生全局歷史由原生工具管理，不擅自刪除 ~/.codex 或 ~/.claude。

cancel 取消 session 當前和排隊工作，保留原生上下文；reply 使用公開 request_id。Native status 返回 state、當前 run_id、queued 和公開 requests。Client 不讀取 .d/ 的 PID、鎖或 native.json 來替代這些命令。

## Agent 版本綁定（已實現）

Server agent 版本設計見 [server-agent.md](server-agent.md)，共享技能的獨立發佈和包格式見 [server-skill.md](server-skill.md)。session.yaml 增加 `agent_version_id`，它是來源提供的不可變配置身份，不是原生 session ID 或 run UUID。選擇和複用的完整鍵變爲 endpoint/conversation/agent/agent_version/harness/適配器 revision。

同一 agent 發佈新版本後，不改舊 session.yaml 的版本字段，也不向舊 events.jsonl 重新 init。下一條新版輸入創建新的本地 session 目錄及事件地址，按該版資源初始化；舊版工作繼續結束，兩版仍使用同一 `<conversation>/workspace/`。不存在自動搬遷或拼接原生上下文，新的上下文由 server 共享歷史隨輸入提供。

首次初始化的臨時輸入放在 session 目錄內、workspace 之外：

```text
sessions/<session_id>/
  session.yaml                     固定 agent_id、agent_version_id
  init-resources/                  準備期間的臨時資源
    AGENTS.md
    skills/<skill_name>/SKILL.md
    skills/<skill_name>/scripts/...
  events.jsonl
  events.jsonl.d/resources/         init 成功後的適配器快照
  runs/<native_run_id>.jsonl
```

Client 下載並校驗指定 ZIP，解包到技能根目錄，然後把指令文件、模型和技能根目錄交給 harnessctl init；空指令/空 skills/默認模型分別省略對應參數。init 成功後立即刪除 init-resources，後續 send 和原生進程恢復依賴適配器快照，不重新讀取 server 的 latest。初始化失敗不發送正文，不假造 run。

已存在的 session 只能複用同一 agent_version_id。重啓後通過公開 status/init 確認綁定；若需要重新準備資源，只從原輸入固定的版本恢復，不能換成當前 head。歷史包不可讀或校驗失敗時明確失敗。

Local 模式的 agent_version_id 是 harness、model、指令及有序 ZIP 摘要的確定性 UUID，不受 ZIP 文件路徑變化影響。Local 模式也使用相同的不可變配置和 ZIP 準備流程；由本地輸入邊界形成版本快照與本地 agent_version_id，其餘 session 選擇、init、send、校驗和恢復規則與遠端一致，不繞開版本機制。

## 生命週期和回收

工作根目錄是 setting.yaml 的 session.work_root，默認 ~/.agentuse/workspace。目錄固定爲 <root>/<endpoint_id>/<conversation_id>/，實際 cwd 是其 workspace/，session 元數據在同級 sessions/。修改根目錄須停止並重啓，需要保留歷史時完整遷移目錄。

啓動時清理遺留前臺工作並重建 run 引用；不自動重放舊輸入。Session 生命週期狀態不承擔 server 派發或確認狀態。中心事件的 ACK 遊標和輸入/控制收據隔離在 .server/。

聲明 quota 後自動檢查受管目錄總量，按整個 conversation 最近使用時間從舊到新清理空閒目錄。運行、等待回答、清理未確認、預留啓動和中心待確認事件均保護目錄。沒有可回收空間時暫停新啓動，不能殺任務來滿足容量目標。

回收刪除該 conversation 的 workspace、session、引用和輸入資源，不刪除 server 文件或原生工具的全局歷史。舊路徑重新出現不代表舊上下文可恢復；必須用新的 session 記錄。目錄標記只校驗受管歸屬，不是另一張 workspace 映射表。

## 實現歸屬

sessionService 直接管理 conversation.yaml、session.yaml、workspace 和會話目錄，提供 conversation/session 查詢、Acquire、Release、指定 session 複用、重啓元數據恢復以及 quota 回收。所有這些操作位於 service/session，直接通過 fileutil 的通用原子寫入和路徑檢查操作文件，不轉發給 store。

runService 依賴 sessionService 完成佔用與釋放；HTTP controller 只接收其只讀查詢接口。目錄正在執行或被保留時不得回收；中心未確認事件的保護條件由 runService 注入，sessionService 不讀取或理解交付協議。運行互斥和目錄保留計數僅協調進程內併發，文件仍是唯一持久化事實。
