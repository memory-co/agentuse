# Structs

agentuse 的數據結構:對象、字段語義、ID、配置與磁盤佈局。**是什麼**;機制見 [`../designs/`](../designs/README.md),HTTP 契約見 [`../apis/`](../apis/README.md)。Go 定義在 [`internal/models/`](../../../internal/models/)。

## 對象清單

| 對象 | 是什麼 | 存哪 | 文檔 |
|---|---|---|---|
| Platform | 接入 hub 的一個平臺:id、密鑰、回調地址、策略 | `hub.json` | [models.md](models.md#platform) |
| Policy | 按平臺的策略:誰能喚醒、誰可信、能做什麼、現場權限 | `hub.json`(掛在 Platform 下) | [models.md](models.md#policy) |
| Comment / Author | 平臺 notify 進來的一條評論及其作者;**hub 不存** | 只掛在 Run 上 | [models.md](models.md#comment) |
| Material | 線程裏的其它評論,標了可不可信 | 掛在 Run 上 | [models.md](models.md#material) |
| Session | 線程鍵 → agent 會話,帶現場分支 | 會話表 | [models.md](models.md#session) |
| Run | 一次喚醒對應的一次運行 | 運行隊列 | [models.md](models.md#run) |
| RunToken | 只給一次運行的令牌,不落盤 | 無(自包含簽名) | [models.md](models.md#runtoken) |
| Event / Activity | hub 回調平臺的事件 | 不落盤 | [models.md](models.md#event) |

配置文件與目錄:[filesystem.md](filesystem.md)。

## ID

| 對象 | 形態 | 示例 |
|---|---|---|
| Session | `s_<12 hex>` | `s_31a0c9e2b7f4` |
| Run | `r_<12 hex>` | `r_1180a4c3d2e1` |
| RunToken | `aut_<base64url claims>.<base64url sig>` | `aut_eyJydW4iOi….Qx9…` |
| 線程鍵 | 平臺定,`<類型>:<鍵>` 是慣例 | `issue:MUL-42`、`blog:/posts/tmux`、`topic:8812` |
| 分支 | `agent/<agent_name>/<線程鍵安全形式>` | `agent/claude/MUL-42` |

前綴 = 類型。線程鍵 hub 不解析,只在算分支名時去掉第一個 `:` 之前的部分,把非 `[A-Za-z0-9._-]` 的字符折成 `-`。

## 引用關係

```
Platform ──(policy)──▶ Policy
Session (platform, thread) ◀──(session_id)── Run ──(trigger)──▶ Comment
                                              └──(context)──▶ Material[] ──▶ Comment
RunToken ──(run)──▶ Run
Event ──(run_id, in_reply_to)──▶ Run, 叫醒評論
```

全部是裸 id 引用,沒有外鍵;評論 id 指向平臺那邊的對象,hub 不校驗。
