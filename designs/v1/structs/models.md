# 對象

字段和 [`internal/models/models.go`](../../../internal/models/models.go) 一一對應,JSON 名即字段名。

## Platform

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | string | 平臺 id,出現在 notify 路徑裏,如 `shop` |
| `secret` | string | 平臺與 hub 約定的密鑰;驗 notify 用 |
| `callback_secret` | string | 可選:hub 籤回調用的密鑰;空 = 沿用 `secret` |
| `callback_url` | string | 平臺掛回調的地址,如 `https://shop.example.com/agentuse/callback` |
| `policy` | [Policy](#policy) | 策略 |

## Policy

| 字段 | 類型 | 說明 |
|---|---|---|
| `agent_name` | string | 被 @ 的名字,如 `claude` |
| `agent_user_id` | string | agent 在平臺裏的應用用戶 id;這個作者的評論不喚醒 |
| `wake_roles` | Role[] | 誰能喚醒 |
| `trusted_roles` | Role[] | 誰的評論作爲帶作者標註的材料;其餘是不可信材料 |
| `actions` | Action[] | 運行令牌允許的動作 |
| `workspace_mode` | `readonly` \| `readwrite` | 現場權限 |

**Role**:`owner`(實例主人 / 站長)、`member`(工作區成員 / 維護者)、`guest`(陌生訪客)。只來自平臺簽名。

**Action**:`reply`(寫評論)、`status`(改線程對象的狀態)、`label`(打標籤,預留)。`activity` 不算動作,有效令牌都能發。

## Comment

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | string | 平臺裏的評論 id |
| `thread` | string | 線程鍵 |
| `author` | Author | `{id, name, role}`,由平臺簽名擔保 |
| `text` | string | 正文 |
| `created_at` | RFC 3339 | 可選 |

notify 的請求體是 Comment 加一個可選的 `context: Comment[]`(同一線程裏的其它評論)。

## Material

| 字段 | 類型 | 說明 |
|---|---|---|
| `comment` | Comment | 線程裏的一條其它評論 |
| `trusted` | bool | 作者是叫醒者本人,或角色在 `trusted_roles` 裏 |

## Session

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | string | `s_…` |
| `platform` | string | |
| `thread` | string | 線程鍵;`(platform, thread)` 唯一 |
| `agent_session_id` | string | agent 自己的會話 id,第一次運行結束後由 daemon 回填,之後 `--resume` |
| `branch` | string | 現場分支 |
| `created_at` / `updated_at` | RFC 3339 | |

## Run

| 字段 | 類型 | 說明 |
|---|---|---|
| `id` | string | `r_…` |
| `session_id` | string | |
| `platform` / `thread` | string | 冗餘自會話,寫回時用 |
| `trigger` | Comment | 叫醒它的那條評論 |
| `context` | Material[] | 線程材料 |
| `actions` | Action[] | 建運行時從策略拷過來,之後改策略不影響已排隊的運行 |
| `workspace_mode` | string | 同上 |
| `status` | `queued` \| `claimed` \| `succeeded` \| `failed` | |
| `attempts` | int | 被領取過幾次;每次領取 +1,租約過期回收後再領取繼續加 |
| `daemon_id` | string | 當前持有它的 daemon(回收後清空) |
| `lease_until` | RFC 3339 | claimed 時的租約到期時間;心跳續期,過期即回收 |
| `error` | string | 失敗原因;租約用完是 `lease expired` |
| `created_at` / `claimed_at` / `finished_at` | RFC 3339 | |

## RunToken

`aut_` + base64url(claims JSON) + `.` + base64url(HMAC-SHA256(token_key, claims 段))。claims:

| 字段 | 說明 |
|---|---|
| `run` | 運行 id |
| `attempt` | 第幾次領取;和運行當前的 `attempts` 不一致就拒絕寫回 |
| `platform` / `thread` | 範圍 |
| `actions` | 允許的動作 |
| `exp` | 過期時間(unix 秒),最長簽發後 24 小時 |

令牌自包含,不落盤;運行 complete 後 hub 拒絕它的寫回(看運行狀態)。

## Event

hub 回調平臺的正文。

| 字段 | 類型 | 說明 |
|---|---|---|
| `type` | `reply` \| `status` \| `activity` | |
| `platform` / `thread` | string | 來自運行,不來自 agent |
| `run_id` | string | |
| `in_reply_to` | string | 叫醒評論的 id,平臺可據此把回覆掛成樓中樓 |
| `text` | string | `reply` 的正文 |
| `status` | string | `status` 的目標狀態,如 `in_review`;取值由平臺定 |
| `activity` | `{type, body}` | `activity` 的內容;type 爲 `thought` / `action` / `elicitation` / `response` / `error`(沿用 Linear AgentSession) |
| `created_at` | RFC 3339 | |
