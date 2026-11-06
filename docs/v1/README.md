# agentuse 文檔

agentuse 是 [AgentHub 設想](https://twwy.net/blog/agenthub-comment-box-for-agents/)的實現:讓任意平臺評論區裏的一個 @,觸達用戶自己電腦上的 agent,像接一個評論框一樣接入。

**定位**:agentuse 是平臺側的中間件,hub 像平臺自己管的 Redis 實例;平臺用戶一條命令把自己的電腦接進來。見 [designs/README.md §2](designs/README.md#2-定位平臺側管理的實例)。

**接入**:[onboarding.md](onboarding.md) 一步步走到第一個 @claude(按現有實現寫;一條命令接入實施後會按 [designs/connect.md](designs/connect.md) 重寫)。

三個目錄各管一件事:

| 目錄 | 回答什麼 | 入口 |
|---|---|---|
| [`designs/`](designs/README.md) | **爲什麼、怎麼運轉**:定位、三方分工、身份三段式、喚醒與信任、現場 | [designs/README.md](designs/README.md) |
| [`structs/`](structs/README.md) | **是什麼**:對象、字段、ID、配置文件與磁盤佈局 | [structs/README.md](structs/README.md) |
| [`apis/`](apis/README.md) | **怎麼調**:平臺 ⇄ hub、daemon ⇄ hub、agent ⇄ hub 的 HTTP 契約,以及 CLI | [apis/README.md](apis/README.md) |

讀法:先 designs/README.md 看全貌,再按需要查 structs 和 apis。代碼與文檔衝突時以代碼爲準,並修文檔。
