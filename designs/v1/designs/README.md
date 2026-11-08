# agentuse —— 總設計

> **狀態:定位已改,代碼待跟進。** 2026-10-18 定位從「用戶自己的實例」改爲「**平臺側管理的實例**」(像平臺自己的 Redis),並補上賬號與 agent 歸屬、一條命令接入。喚醒、會話、運行隊列、租約、WebSocket 推送、運行令牌、回調這些已實施的部分不變;賬號、歸屬、接入碼、安裝腳本還沒寫,計劃見 [roadmap.md](roadmap.md)。在代碼跟上之前,[onboarding.md](../onboarding.md) 描述的是現有實現的接入方式。
>
> 分篇:[accounts.md](accounts.md)(**新**:用戶、agent、設備三個對象,運行只派給 agent 主人的電腦)、[connect.md](connect.md)(**新**:用戶一條命令接入:接入碼、安裝腳本、自動登記)、[hub.md](hub.md)(hub 實例:會話表、運行隊列、租約、推送、存儲)、[identity.md](identity.md)(身份三段式)、[wake-and-trust.md](wake-and-trust.md)(誰能喚醒誰的 agent、誰的話只是材料)、[daemon.md](daemon.md)(用戶電腦上的 daemon:連接、驅動 CLI、現場)、[roadmap.md](roadmap.md)(實施計劃)。

---

## 1. 要解決的問題

評論區是人與人交流的地方。我們想讓平臺評論裏的 `@alice-claude` 直接叫醒 **alice 自己電腦上**的 agent:它在 alice 的倉庫裏開分支改代碼,回一條評論,把工單改成「待審覈」。

一個 @ 背後,平臺真正關心的只有兩件:**評論存儲**和**工單對象與狀態**。其餘六件與平臺無關,卻每個平臺都要重寫一遍:

| # | 事 | agentuse 裏誰做 |
|---|---|---|
| 1 | 喚醒判定:是不是在 @ 某個 agent;忽略 agent 自己發的評論 | hub([wake-and-trust.md](wake-and-trust.md)) |
| 2 | 會話映射:MUL-42 對應這個 agent 的哪段會話,後續評論續接 | hub([hub.md](hub.md)) |
| 3 | 連接:用戶電腦出站連接穿透 NAT,心跳 | daemon([daemon.md](daemon.md)) |
| 4 | 驅動 CLI:`claude -p --output-format stream-json` | daemon |
| 5 | 現場:工作目錄 + 分支 `agent/alice-claude/MUL-42` | daemon |
| 6 | 反向令牌:每次運行簽發、最長 24 小時的臨時令牌 | hub([identity.md](identity.md)) |

**agentuse 就是把這六件抽出來的中間件**,再加上第七件每個平臺都得做、又最容易做砸的事:**讓用戶把自己的電腦接進來**。平臺只做「通知」「接收回調」和「在設置頁放一個接入按鈕」。

## 2. 定位:平臺側管理的實例

博客裏的類比是 Redis:**平臺自己起一個 Redis 實例,應用只認地址和密鑰**。agentuse 的 hub 就是這樣一個實例:

| | Redis | agentuse hub |
|---|---|---|
| 誰部署、誰運維 | 平臺的運維 | 平臺的運維(或買託管版) |
| 應用怎麼用 | 地址 + 密碼 + 協議 | 地址 + 密鑰 + 協議(notify / 回調) |
| 存什麼 | 應用的緩存、隊列 | 會話表、運行隊列、誰的 agent 在哪臺電腦 |
| 不存什麼 | 應用的主數據 | **評論**(留在平臺) |
| 可替換 | 換 Valkey 不改應用 | 自部署 / 託管版同一套代碼和協議,換實例不改平臺 |

和多說的區別也在這裏。多說是一家公司託管的免費服務,關停後數據跟着沒了;agentuse 的 hub 是**平臺自己的實例**,數據在平臺自己手裏,代碼和協議開源,不依賴任何一家公司活着。

於是有三類人,各自的接入成本:

| 誰 | 做什麼 | 成本 |
|---|---|---|
| **平臺運維** | 起一個 hub 實例,配上平臺密鑰和回調地址 | 一次,和起一個 Redis 差不多 |
| **平臺開發者** | 評論存庫後 notify;回調裏寫評論、改狀態;設置頁加「接入我的 agent」 | 幾十行,用 SDK |
| **平臺用戶** | 在設置頁點一下,複製一條命令到自己電腦的終端裏跑 | **一條命令**,不碰配置文件,不找任何人要密鑰 |

## 3. 爲什麼不是評論框那樣的 iframe

評論框服務(Disqus)做四件事:線程映射、身份(站點 HMAC 簽名用戶信息,第三方只驗籤)、存儲、治理。agent 版本里這四件都有對應,多出「執行」和「接入」。照搬 iframe 行不通:

1. iframe 裏的 agent 拿不到平臺數據,改不了工單狀態;
2. 對話被劈成平臺評論和中間件兩處;
3. 中間件能看到所有需求、代碼片段和結論。

所以 agentuse **不存評論**:評論留在平臺,agent 的回覆由平臺寫成一條普通評論,作者是平臺裏代表這個 agent 的應用用戶。

## 4. 三方分工

```
平臺(工單 / 博客 / 論壇)            hub(平臺側的實例)                      各個用戶的電腦
┌────────────────────────┐ notify   ┌────────────────────────────┐ 出站連接 ┌─────────────────────┐
│ 評論存庫後 → hub       │ ───────▶ │ 喚醒判定:@ 的是誰的 agent  │ ◀─────── │ alice 的 daemon     │
│ 設置頁:接入我的 agent  │ 建 agent │ 用戶 / agent / 設備        │ 領取運行 │  只領 alice 的運行  │
│  → 拿到一條安裝命令    │ ───────▶ │ 會話表 運行隊列 租約       │          ├─────────────────────┤
│ /agentuse/callback     │ ◀─────── │ 運行令牌 → 回調平臺        │ ◀─────── │ bob 的 daemon       │
│ 寫評論 / 改狀態        │ hub 簽名 └────────────────────────────┘          │  只領 bob 的運行    │
└────────────────────────┘                                                └─────────────────────┘
```

- **平臺**:評論寫入後 `notify`;在回調裏把 `reply` 寫成 agent 應用用戶的評論、把 `status` 寫回對象;設置頁替用戶向 hub 建 agent,把 hub 交回的安裝命令展示給用戶。
- **hub**:平臺側的實例。知道每個 agent 屬於誰、在哪臺電腦上([accounts.md](accounts.md));判喚醒、找會話、排隊、籤令牌、轉發寫回;發安裝腳本、兌換接入碼([connect.md](connect.md))。
- **daemon**:用戶電腦上常駐,由安裝命令自動裝好、登記、設爲開機自啓;主動連出 hub,**只領自己主人的 agent 的運行**,在 worktree 裏驅動 agent CLI。

## 5. 一次 @ 的完整經過

前提:alice 已經在平臺設置頁建了 agent `alice-claude`,並在自己電腦的 `~/work/shop-api` 裏跑過那條安裝命令([connect.md](connect.md))。

1. bob 在 MUL-42 評論「@alice-claude 退款金額算錯了」,平臺存庫後 notify hub,用平臺密鑰簽名。
2. hub 驗籤,在這個平臺的 agent 裏找到被 @ 的 `alice-claude`,按它的喚醒規則判斷 bob 能不能叫它([wake-and-trust.md](wake-and-trust.md))。能叫的話,找 `(issue:MUL-42, alice-claude)` 的會話(沒有就建,分支 `agent/alice-claude/MUL-42`),排一條**屬於 alice-claude** 的運行。
3. alice 電腦上的 daemon 連着 hub 等着;hub 只把這條運行推給 alice-claude 綁定的那臺設備,附上運行令牌。bob 的電腦看不到它。
4. daemon 在 `~/work/shop-api` 上爲會話準備 worktree,注入令牌,用 `claude -p` 啓動(有舊會話就 `--resume`)。
5. agent 改代碼、推分支,然後 `agentuse reply "…"`、`agentuse status in_review`;daemon 每 15 秒心跳續租。
6. hub 校驗令牌,用 hub 簽名回調平臺;平臺以 `alice-claude` 的名義寫評論、改狀態。
7. agent 退出,daemon 交回結果;同一線程裏下一次 @alice-claude 續接同一段會話。

alice 的電腦不在線時,運行留在隊列裏等它上線;notify 的響應會告訴平臺「alice-claude 目前離線」,平臺可以在評論下提示。

## 6. 成本與可替換性

- 推理費用由每個用戶自己的 Claude / ChatGPT 訂閱承擔,平臺不付;
- hub 只是消息路由 + 幾張小表 + 隊列,成本接近一個小 Redis;
- 數據在平臺自己手裏:評論在平臺主庫,會話與運行記錄在平臺自己的 hub 實例;
- 協議開放:自部署和託管版是同一套代碼,平臺代碼只依賴「地址 + 密鑰 + 協議」。

## 7. 代碼佈局

```
cmd/agentuse/            單一二進制入口
internal/
├── cli/                 命令行:hub start / daemon start / reply / status / activity / run
├── config/              hub.json、daemon.json
├── models/              數據結構(對應 docs/structs)
├── hub/                 hub 業務(hub.go)+ HTTP 接口(server.go)+ daemon 推送連接(connect.go)
├── services/
│   ├── wake/            喚醒判定
│   ├── token/           運行令牌
│   └── callback/        hub → 平臺回調
├── store/               會話表與運行隊列(接口 + SQLite / 內存實現;storetest 是共用的行爲測試)
└── daemon/              領取循環(WebSocket / 輪詢)、心跳、worktree 現場、驅動、提示詞
pkg/
├── signature/           平臺 ⇄ hub 的 HMAC 簽名(公開,其它語言照此實現)
└── sdk/                 平臺側 Go SDK:Notify + Handler
docs/                    designs / structs / apis
```
