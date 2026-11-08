# 用戶、agent、設備

> **狀態:設計稿,未實施。** 實施計劃見 [roadmap.md](roadmap.md)。

hub 是平臺側的實例,一個 hub 服務這個平臺上的所有用戶。所以它必須知道兩件事:**被 @ 的 agent 是誰的**,**這個人的電腦是哪臺**。現在的實現沒有這一層,同一平臺的運行誰的 daemon 都能領,多用戶下會把 bob 叫的活派到 alice 的電腦上。這篇補上這一層。

## 1. 三個對象

```
User(平臺裏的一個人)
 ├── Agent(這個人在平臺上的一個 agent,如 @alice-claude)── 綁定 ──▶ Device + 目錄
 └── Device(這個人接進來的一臺電腦,上面跑着 daemon)
```

| 對象 | 是什麼 | 誰建 | 鍵 |
|---|---|---|---|
| **User** | 平臺裏的一個人。hub 不管註冊登錄,身份由平臺簽名擔保 | hub 第一次見到時自動建(建 agent 或 notify 時) | `(platform, user_id)`,`user_id` 是平臺自己的用戶 id |
| **Agent** | 一個人在平臺上的一個 agent:名字(`alice-claude`)、主人、跑在哪臺設備的哪個目錄、用什麼驅動、權限 | 平臺替用戶建(設置頁「接入我的 agent」) | `ag_…`;`handle` 在平臺內唯一 |
| **Device** | 一個人接進來的一臺電腦,上面的 daemon 用設備密鑰連 hub | 用戶在電腦上跑安裝命令時自動建([connect.md](connect.md)) | `dv_…` |

hub **不存用戶的密碼、郵箱或權限**,只存平臺給的 `user_id` 和顯示名。平臺裏誰是誰、誰有什麼角色,永遠以平臺每次簽名送來的爲準。

## 2. Agent

| 字段 | 說明 |
|---|---|
| `id` | `ag_…` |
| `platform` | 屬於哪個平臺 |
| `handle` | 評論裏 @ 的名字,平臺內唯一,如 `alice-claude`。平臺在自己的用戶表裏也要有一個同名的應用用戶 |
| `agent_user_id` | 平臺裏那個應用用戶的 id;agent 的回覆以它的名義寫,它發的評論不喚醒任何 agent |
| `owner` | 主人的 `user_id` |
| `device` | 綁定的設備 `dv_…`;沒接入時爲空 |
| `workdir` | 設備上的工作目錄,如 `/Users/alice/work/shop-api`;接入時取用戶跑命令的那個目錄 |
| `driver` | `claude`(之後加 `codex` 等) |
| `wake` | 誰能叫醒它:`owner`(只有主人)/ `members`(平臺策略 `wake_roles` 裏的人)。默認 `owner` |
| `actions` / `workspace_mode` | agent 能做什麼、現場權限;**不能超過平臺策略**,超出部分按平臺策略截斷 |

**一個 agent 綁一臺設備上的一個目錄。** 現場是本機倉庫裏的 worktree,換機器就是換現場,所以不做「一個 agent 在多臺電腦間漂」。想在兩臺電腦上各有一個,就建兩個 agent(`alice-claude`、`alice-claude-mini`)。在新電腦上用同一個 agent 的新接入碼再接一次,就是把它搬過去。

一個人可以有多個 agent(不同倉庫、不同驅動),平臺決定每人最多幾個。

## 3. Device

| 字段 | 說明 |
|---|---|
| `id` | `dv_…` |
| `owner` | 主人的 `user_id` |
| `name` | 主機名,如 `alice-mbp`,只用於展示 |
| `key_hash` | 設備密鑰的 sha256;密鑰只在接入時交給 daemon 一次,hub 不存明文 |
| `last_seen` | 最近一次連接 / 心跳;用來回答「alice-claude 在線嗎」 |
| `revoked_at` | 吊銷時間;吊銷後這臺設備的連接、領取、心跳全部 401 |

設備密鑰取代現在 hub.json 裏手寫的 `daemon_keys`。

## 4. 運行只派給主人的設備

運行多一個字段 `agent_id`,會話的鍵從 `(platform, thread)` 變成 `(platform, thread, agent)`:同一個工單裏 @alice-claude 和 @bob-claude 是兩段獨立的會話,各自的分支(`agent/alice-claude/MUL-42`、`agent/bob-claude/MUL-42`)。

領取時多一個條件:

```
可領 = status 是 queued
     且 所屬會話沒有進行中的運行        ← 已實施:同一線程同一 agent 串行
     且 run.agent.device = 來領的設備   ← 新增
```

於是 bob 的電腦永遠領不到 alice-claude 的運行。設備不在線時運行就在隊列裏等;等多久由平臺策略 `queue_ttl` 決定(默認 24 小時),過期判失敗,並回調一條 `activity`(type `error`,「alice-claude 的電腦不在線」),平臺據此提示。

## 5. 一條評論 @ 了好幾個 agent

「@alice-claude @bob-claude 你們倆各看一下」會給每個能被叫醒的 agent 各排一條運行,互不影響。notify 的響應逐個交代:

```json
{
  "woken": true,
  "agents": [
    {"handle": "alice-claude", "woken": true,  "reason": "woken", "online": true,  "run_id": "r_…"},
    {"handle": "bob-claude",   "woken": false, "reason": "role_denied"}
  ]
}
```

頂層的 `woken` 是「至少叫醒了一個」。

## 6. 平臺怎麼管 agent

平臺用平臺密鑰簽名調 hub(和 notify 同一套簽名),全在服務端調,用戶瀏覽器不直接碰 hub:

| 調用 | 用途 |
|---|---|
| `POST /v1/platforms/{p}/agents` | 替某個用戶建 agent:`{owner, owner_name, handle, agent_user_id, wake?}`,交回 agent 和一條安裝命令 |
| `GET /v1/platforms/{p}/agents?owner=` | 列某人的 agent,帶在線狀態,給設置頁展示 |
| `POST /v1/platforms/{p}/agents/{id}/connect-code` | 重新生成接入碼(換電腦、命令過期了) |
| `PATCH /v1/platforms/{p}/agents/{id}` | 改 `wake` / `actions` 等 |
| `DELETE /v1/platforms/{p}/agents/{id}` | 刪 agent:排隊中的運行判失敗,會話歸檔 |
| `DELETE /v1/platforms/{p}/devices/{id}` | 吊銷一臺設備(電腦丟了) |

這些接口的正式契約在實施時寫進 [apis/](../apis/README.md)。

## 7. 不做什麼

- **不做賬號體系**:hub 沒有註冊、登錄、密碼。用戶是誰、在不在團隊裏,問平臺。
- **不跨平臺認人**:同一個人在兩個平臺上是兩個 User。一臺電腦可以同時接兩個平臺的 hub,daemon 各連各的。
- **不讓 agent 漂移**:一個 agent 一臺設備一個目錄,原因見 §2。
