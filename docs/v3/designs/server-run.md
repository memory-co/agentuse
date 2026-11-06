# runService：輸入、執行與事件

> 本文是配合 [protocol-v2.md](protocol-v2.md) 的精簡目標設計，尚未據此修改程式或資料庫。現行實作仍使用 run_prompts、run_requests、run_controls；現行通信見 [protocol.md](protocol.md)。下列 SQL 是目標結構，不是已部署的 migration。

runService 只管理三張表：

| 表 | 保存什麼 |
|---|---|
| run_inputs | Server 要交給 Client 的輸入：send、reply、cancel |
| runs | 適配器實際產生的一次交互，以及便於查詢的狀態投影 |
| run_events | Client 上報的五種公共事件，保留來源位置供恢復與去重 |

**輸入是 Server 的要求，run 是適配器的執行事實，事件是這個事實的來源。** 不再為問題、回答和取消各建一套表。

## 一、message 與 run 如何連接

使用者訊息 @ 幾個 agent，就建立幾條 `kind=send` 的 run_inputs。每條保存面向該 agent 的正文副本，並固定 agent_version_id；這時還沒有 run_id。

```text
M1 @coder @reviewer → I1、I2
Client 領取 I1，呼叫 harnessctl send
accepted(run_id=R1, input_id=A1) → 建立 R1，I1 綁定 R1/A1
M2 @coder → I3 → 再次 send
accepted(run_id=R1, input_id=A2) → 同一次交互的補充輸入
accepted(run_id=R2, input_id=A2) → 適配器安排了下一次交互
```

後兩行是兩種可能結果，不會同時發生。只有適配器能決定實際歸屬；Server 不根據「前一個 run 還在跑」提前合併。

message 與 run 的 N:M 關係，由 `kind=send` 的 run_inputs 連接。run_inputs.id 是 Server 輸入分配標識，adapter_input_id 是 accepted.input_id；兩者都不是另一個 run UUID。

runs.id 只採用 accepted.run_id。first_message_id 取該 run 第一條 accepted 對應的 message；messages 仍只回覆 message，不增加 run_id。

## 二、回答與取消也是輸入

三種輸入共用同一個領取、回執和恢復流程：

| kind | message_id | run_id | payload | Client 行為 |
|---|---|---|---|---|
| send | 必填 | accepted 前為空，之後固定 | content、固定版本 config、檔案描述 | 新 session 先 init，再 send |
| reply | 空 | 已存在的目標 run | request_id、answer | 核對問題歸屬，再 reply |
| cancel | 空 | 已存在的目標 run | 空物件 | 核對該 run 尚未結束，再 cancel |

reply/cancel 不會建立新 run，也不建立一條假的使用者聊天訊息。target 中的 client_session_id 由既有 run 取得，不在輸入表再保存一份。

run_inputs 的 status 只描述傳輸：pending、sent、applied、rejected、unknown。它不複製 runs 的 running/completed 等執行狀態。

- pending/sent：等待領取，或已交付而尚無確定回執；重送沿用同一 id。
- applied：本地操作已受理；send 的 run/input 關聯仍以 accepted 為準。
- rejected：可證明未受理，例如 init 前的資源準備失敗；只記錄該輸入失敗，不虛構 run。
- unknown：可能已受理，停止自動重送，等待公共事件或人工處理。

回答送達不代表問題已回覆，取消送達也不代表 run 已取消。request/result 事件才決定實際狀態。延遲抵達的 cancel 不能取消同一 session 的下一次 run。

## 三、問題直接從事件讀取

不再建立 run_requests。request 事件已包含 request_id、kind、state、message、questions 和答案；查詢同一 run/request_id 最後一條 request 事件，就是目前的問題快照。

收到使用者回答時，在交易內確認最後狀態仍為 pending，再插入一條 reply 輸入。若同 request_id 已有非 rejected 的 reply 輸入，就拒絕再次提交；applied 也須等 request 事件確認，不能因傳輸完成就接受另一份答案。適配器之後仍可能回報 expired 或拒絕答案，Server 如實展示，不先把問題改成 replied。

request 事件更新 runs 的 waiting/running 投影；result 決定 completed/failed/cancelled。新輸入、傳輸回執及設備斷線都不能覆蓋已確認的執行終態。

## 四、固定版本與通信邊界

保存 message 和 send 輸入時，在同一交易固定 agent_version_id、agent_id 與交付 device。新 Agent 版本不改舊輸入，Client 按版本選擇新 session/events；同一 run 不接受跨 Agent 版本的輸入。

入隊時保存目標 agent 的正文副本。首次 pull 在原行 payload 補齊 Server 組織的上下文、固定 config、Work 授權和檔案描述，並同時標記 sent；之後 payload 不再改動，不另存一份 dispatch。

Client 只接收準備好的正文與版本資源，不推斷協調者角色。原生 RPC、hook、權限策略、插話或排隊都在 Client 的 harnessService/適配器內處理。

網路使用 pull/report：每條 command 直接對應一條 run_inputs，統一採用 `i:<run_inputs.id>` 作為 command_id。send、reply、cancel 不再各自擁有控制記錄和 ACK 流程。具體報文見 [protocol-v2.md](protocol-v2.md)。

## 五、事件保存與恢復

run_events 保存 accepted、message、request、result、log；payload 使用原始公共事件 JSON。run_id 允許為空，以容納不屬於任何 run 的 log。來源綁定一旦建立，device/client_session 不可改綁 conversation、Agent 或版本。

事件依 `(device_id, client_session_id, source_pos)` 唯一。相同位置重送時核對內容；不同內容拒絕。每個來源已提交的最大 source_pos 就是 ACK，不另建遊標表。已提交事件隨 conversation 整體清理，在此之前保留來源位置與內容，確保 ACK 單調並能校驗重送。

log 可以不上傳。只有實際保存的事件才推進 Server ACK；尾端被略過的 log 不需要確認，也不保護目錄。Client 恢復後重新掃過這些 log 即可，不增加另一份持久化掃描遊標。

report 在同一交易內保存回執、事件、runs 投影和最終 conversation 回覆，提交後才確認。原生結果中的 output/error/usage/cost 保留在 runs.result 的完整 JSON 投影，不再拆出多份結果欄位。

accepted 同時將對應 send 輸入標記 applied 並綁定 run/input。result.input_ids 只核對該 run 的 send 輸入集合，reply/cancel 不計入其中。

結果回覆以 reply_to_message_id 指向 first_message_id；runs.result_message_id 記錄已建立的回覆，事件重送不重複追加。cancelled 不新增最終 assistant 回覆。設備離線不製造 result，也不將不確定輸入改派其他設備。

## 六、表結構

時間使用 Unix 秒，JSON 使用 TEXT，所有關聯由 service 檢查，不使用資料庫外鍵。conversation/message 使用 v3 目標 UUID；run_inputs 的整數 id 只用於傳輸與排序。

### run_inputs

```sql
CREATE TABLE run_inputs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL,
    message_id TEXT,
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('send','reply','cancel')),
    run_id TEXT,
    adapter_input_id TEXT,
    payload TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK(status IN ('pending','sent','applied','rejected','unknown')),
    error TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK((kind='send' AND message_id IS NOT NULL)
        OR (kind IN ('reply','cancel') AND message_id IS NULL AND run_id IS NOT NULL))
);
CREATE INDEX run_inputs_dispatch ON run_inputs(device_id, status, id);
CREATE INDEX run_inputs_run ON run_inputs(run_id, kind, id);
CREATE UNIQUE INDEX run_inputs_message_agent
    ON run_inputs(message_id, agent_id) WHERE kind='send';
```

adapter_input_id 只在 send 的 accepted 到來後填入；kind、目標版本、交付設備及已公佈的 run/input 歸屬都不改綁。created_by 保存觸發輸入的使用者或業務作者標識，供回答及取消審計，並不改變原 message 的作者。

### runs

```sql
CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    first_message_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    client_session_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK(status IN ('running','waiting','completed','failed','cancelled')),
    result TEXT,
    result_message_id TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX runs_conversation ON runs(conversation_id, created_at, id);
```

harness/revision 記錄 Client 實際上報的執行選擇；名稱與 Agent 配置透過固定 agent_version 讀取，不保存 agent_name/agent_key。result 是 result 事件的查詢投影，不能單獨由 Server 寫成成功或失敗。

### run_events

```sql
CREATE TABLE run_events (
    device_id TEXT NOT NULL,
    client_session_id TEXT NOT NULL,
    source_pos INTEGER NOT NULL CHECK(source_pos > 0),
    conversation_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    agent_version_id TEXT NOT NULL,
    run_id TEXT,
    kind TEXT NOT NULL CHECK(kind IN ('accepted','message','request','result','log')),
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY(device_id, client_session_id, source_pos)
);
CREATE INDEX run_events_run ON run_events(run_id, kind, source_pos);
```

不再給事件另一個自增 id。問題查詢按 run_id/request_id 取最後來源位置；同一 run 固定在同一 Client session，來源順序足以決定快照先後。需要最佳化時可增加 JSON 欄位索引，先不為查詢投影增加新表。

## 七、哪些表被移除

| 舊表 | 精簡後 |
|---|---|
| run_prompts | 更名並收斂為 run_inputs，保留 message 對各 agent 的輸入副本 |
| run_controls | 合併到 run_inputs 的 reply/cancel，複用傳輸狀態與回執 |
| run_requests | 取消，由 request 事件取得最新快照 |
| runs、run_events | 保留：一張查執行，一張保存來源事實 |
| work_run_tokens、run_notifications | 屬於 workService，本文不再重複宣告；這兩張不計入本次刪表數量 |

runService 從五張表縮為三張。Work 是否進一步簡化授權與彙總記錄，應在 [server-work.md](server-work.md) 分別處理，不能將必要狀態藏進其他表來宣稱已刪除。

[Server 總覽](server-main.md) · [conversationService](server-conversation.md) · [agentService](server-agent.md) · [Client runService](client-run.md) · [新通信設計](protocol-v2.md) · [公共事件](../../../harnessctl/EVENT.md)
