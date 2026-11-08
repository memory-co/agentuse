# 一條命令接入

> **狀態:設計稿,未實施。** 實施計劃見 [roadmap.md](roadmap.md)。

目標:平臺用戶在設置頁點一下,複製一條命令到自己電腦的終端裏跑完,就能在評論裏 @ 自己的 agent。不改配置文件,不找運維要密鑰,不重啓任何東西。參照 Multica:裝一個 CLI、登錄一次,daemon 就在後臺跑起來。

## 1. 用戶看到的

在平臺的「我的 Agent」設置頁:

1. 填一個名字(默認 `<用戶名>-claude`),點「創建」。
2. 頁面出現一條命令和一句提示「在你想讓 agent 幹活的倉庫目錄裏運行」:

   ```bash
   cd ~/work/shop-api
   curl -fsSL https://agentuse.shop.example.com/install.sh | sh -s -- ac_7Hq2mX9pLw
   ```

3. 終端裏:

   ```
   ✓ 已安裝 agentuse 0.3.0 到 ~/.agentuse/bin
   ✓ 找到 claude(已登錄)
   ✓ 已接入 shop:agent @alice-claude → alice-mbp:~/work/shop-api
   ✓ 已設爲開機自啓(launchd)
   在任意工單評論裏 @alice-claude 試試。
   ```

4. 設置頁上 agent 的狀態變成「在線 · alice-mbp」。

## 2. 背後發生的

```
平臺設置頁                 平臺服務端                         hub                          用戶電腦
   │ 點「創建」               │                                 │                               │
   │────────────────────────▶│ 在自己用戶表裏建應用用戶          │                               │
   │                         │  alice-claude                    │                               │
   │                         │ POST /agents(平臺簽名)────────▶│ 建 User、Agent、接入碼 ac_…   │
   │                         │◀──────────── agent + 安裝命令 ────│                               │
   │◀──── 展示安裝命令 ───────│                                  │                               │
   │                                                            │                               │
   │                                        用戶複製命令到終端 ──┼──────────────────────────────▶│
   │                                                            │◀── GET /install.sh ───────────│
   │                                                            │◀── GET /download/<os>/<arch> ─│ 裝二進制
   │                                                            │◀── POST /v1/connect ──────────│ 交接入碼
   │                                                            │  校驗接入碼(一次性,15 分鐘)  │
   │                                                            │  建 Device,綁到 Agent        │
   │                                                            │─── 設備 id + 設備密鑰 ────────▶│ 寫 daemon.json
   │                                                            │                               │ 裝開機自啓
   │                                                            │◀══ WebSocket(設備密鑰)═══════│ daemon 上線
   │  刷新:在線 · alice-mbp ◀─────────── GET /agents ──────────│                               │
```

### 2.1 接入碼

| 性質 | 取值 | 爲什麼 |
|---|---|---|
| 一次性 | 兌換後立刻作廢 | 命令被別人看到也用不了第二次 |
| 時效 | 15 分鐘 | 截圖、聊天記錄裏泄露的風險小 |
| 綁定 | 一個 agent | 兌換隻能把**這個** agent 接到**跑命令的這臺電腦** |
| 形態 | `ac_` + 128 位隨機,hub 只存 sha256 | 數據庫泄露也拿不到可用的碼 |

只有平臺服務端能申請接入碼,而平臺只把它展示給 agent 的主人看。所以「誰能把電腦接成 alice-claude」最終由平臺的登錄態決定,hub 不需要自己的賬號體系。

### 2.2 install.sh

hub 自己提供 `GET /install.sh`,內容固定、可讀、不帶任何祕密:

1. 判斷系統和架構(macOS / Linux,amd64 / arm64);
2. 從 `GET /download/<os>/<arch>` 下載對應的 agentuse 二進制,校驗 sha256,放到 `~/.agentuse/bin/agentuse`,並把這個目錄加進 shell 的 `PATH`(agent 運行時要調 `agentuse reply`);
3. 執行 `agentuse connect <hub 地址> <接入碼> --dir "$PWD"`。

`/download` 由 hub 從 GitHub Release 轉發或重定向,也可以由運維指向內網鏡像。已經裝過的電腦,下載這一步跳過,只接入。

不想 `curl | sh` 的用戶,可以自己裝好二進制,直接跑第 3 步那條命令,效果一樣。

### 2.3 agentuse connect

```bash
agentuse connect https://agentuse.shop.example.com ac_7Hq2mX9pLw --dir ~/work/shop-api
```

1. **檢查前提**:`claude` 在不在、登沒登錄;`--dir` 是不是 git 倉庫、有沒有基準分支。不滿足就停下,說清楚缺什麼,接入碼不消耗。
2. **兌換**:`POST /v1/connect {code, device_name, os, dir, base_ref}`。hub 校驗接入碼、建 Device(或複用這臺電腦已有的設備)、把 agent 綁到這臺設備和目錄,交回設備 id 和設備密鑰。
3. **寫配置**:`~/.agentuse/daemon.json` 里加上這個 hub 和設備密鑰(權限 0600)。同一臺電腦接多個 agent、多個平臺的 hub 時,都追加進這一個文件。
4. **起 daemon**:macOS 寫 launchd 的 `~/Library/LaunchAgents/dev.agentuse.daemon.plist`,Linux 寫 `systemctl --user` 的 unit,然後啓動。已經在跑就讓它重讀配置。
5. **自檢**:等 daemon 連上 hub,打印「已接入」。

同一臺電腦第二次接入(第二個 agent、另一個平臺),設備密鑰和 daemon 都複用,只是多綁一個 agent。

### 2.4 斷開與吊銷

| 場景 | 怎麼做 |
|---|---|
| 不想用了 | 平臺設置頁刪 agent;或在電腦上 `agentuse disconnect alice-claude` |
| 換電腦 | 設置頁「重新接入」拿新命令,在新電腦上跑;agent 搬到新設備,舊設備上的綁定失效 |
| 電腦丟了 | 設置頁吊銷設備;這臺設備的密鑰立刻作廢,綁在上面的 agent 變成未接入 |
| 卸載 | `agentuse uninstall`:停 daemon、刪開機自啓、刪 `~/.agentuse` |

## 3. 運維那一側也要簡單

既然 hub 是平臺自己的實例,起它也應該像起 Redis:

```bash
docker run -d -p 7700:7700 -v agentuse:/data \
  -e AGENTUSE_PLATFORM_ID=shop \
  -e AGENTUSE_PLATFORM_SECRET=… \
  -e AGENTUSE_CALLBACK_URL=https://shop.example.com/agentuse/callback \
  -e AGENTUSE_PUBLIC_URL=https://agentuse.shop.example.com \
  ghcr.io/memory-co/agentuse hub
```

- 單平臺用環境變量就夠,不用寫 hub.json;多平臺或細調策略時再寫 hub.json,兩者都支持。
- `token_key` 不配就在第一次啓動時自動生成並存進數據目錄。
- `AGENTUSE_PUBLIC_URL` 是用戶電腦訪問 hub 的地址,安裝命令裏用的就是它。
- 不再有 `daemon_keys`:設備都是通過接入碼自己登記的。

## 4. 安全

- **接入碼**只經過平臺服務端和平臺的已登錄頁面,只給 agent 的主人看。
- **設備密鑰**只在兌換時交給 daemon 一次,存在用戶電腦 `0600` 的文件裏;平臺永遠看不到它,hub 只存哈希。
- **install.sh** 只從 hub 自己的 TLS 地址下載,二進制校驗 sha256;腳本內容公開、固定,用戶可以先 `curl` 下來看。
- **一臺電腦被別人拿到**:吊銷設備即可,不影響同一個人的其它設備和其它人。
- agent 在電腦上的權限邊界不變,見 [wake-and-trust.md](wake-and-trust.md)。
