# 倉庫目錄整理方案

> 狀態：已按六步完成實現。參考：[Go 大工程的代碼目錄分析](https://twwy.net/blog/go-project-layout/)。

## 一、結論

client / server 兩個頂層目錄，下面都按 **cli / frontend / controller / service / store** 組織，兩邊結構一致：

```
server/  cli  frontend  controller  service  store  (+ hub)
client/  cli  frontend  controller  service  store
```

幾條約定：

1. **用 `internal/` 讓編譯器守住 client / server 的邊界。** 目錄分成 `server/internal/…` 和 `client/internal/…` 後，client 想 import server 的代碼，編譯直接失敗。兩邊共用的東西（daemon ⇄ 服務的協議、harness.yaml）留在倉庫根的 `internal/`，HTTP API 的類型統一用 `sdk/golang`。
2. **兩邊都要有 store 層。** server 的 store 是 SQLite 主庫；client 的 store 是本機賬本，記錄這臺機器收到過的命令和 token 用量。
3. **同一個詞，兩邊的角色要寫清楚：**

| 層 | server | client |
|---|---|---|
| cli | `agentuse-server` 的命令 | `agentuse` 的命令 |
| frontend | 管理網頁 | 本機只讀頁面：命令清單、token 統計 |
| controller | HTTP API，可讀可寫 | 本機 HTTP，**只有 GET** |
| service | 業務編排，**由 controller 調用** | 常駐主循環：連服務端、領活、跑 harness、彙報、寫賬本，**自己運行，不被 controller 調用** |
| store | SQLite 主庫 | SQLite 本機賬本，分成 Writer（給 service）和 Reader（給 controller） |

client 的 service 和 server 的 service 不一樣：它是現在的 `internal/daemon` 整包平移過來的，`agentuse daemon` 命令跑的就是它。讀代碼時記住一點就夠了：**client 的 controller 不調用 service，兩者只通過 store 交換數據。**

## 二、現狀和問題

```
cmd/agentuse/            cmd/agentuse-server/       兩個 10 行的 main
internal/cli/            兩個二進制的命令行混在一個包裏
internal/server/         HTTP 路由 + handler + WebSocket hub + setup.sh + 託管網頁
internal/store/          SQLite，同時承擔了大量業務（run 領取 / 租約 / 重排、work、配額）
internal/daemon/         連接循環、配置、harness 探測與執行、codex、插話、提問
internal/protocol/       daemon ⇄ 服務的消息
internal/attachment/     附件類型 + 又一個 HTTP 客戶端
harness/                 內置 harness.yaml + 解析（server 和 daemon 都用）
frontend/                React 網頁，embed 進 server
sdk/golang/ …            對外 SDK，也是一套 HTTP 客戶端和 API 類型
```

當前所有代碼都在同一個 `internal/` 下，編譯器攔不住任何跨端引用。已經出現的問題：

| 問題 | 位置 |
|---|---|
| 用戶電腦上的 CLI 依賴服務端存儲層：`agentuse session/work` 用 `store.Session`、`store.Run` 解析 HTTP 響應 | `internal/cli/session.go`、`work.go`、`client.go` |
| 兩個二進制的命令放在一個包裏，`agentuse` 也會鏈接 `server`、`store` | `internal/cli/` |
| 同一套 HTTP API 有三個客戶端：`cli.apiClient`、`attachment.Client`、`sdk/golang.Client` | |
| 業務邏輯一部分在 `server`（handler 裏），一部分在 `store`（事務裏），沒有固定歸屬 | `server/runs.go`、`store/run_lifecycle.go`、`store/run_work.go` |
| `server` 包同時是 HTTP 層和 daemon 長連接層 | `server/hub.go` |
| 本機沒有任何執行記錄：run 結束後只剩會話目錄，token 用量只上報了 `cost_usd`，`Dispatch` 裏也沒有發起人 | `internal/daemon/harnesses.go` 的 `Result`、`internal/protocol` 的 `Dispatch` |

## 三、目標結構

```
agentuse/
├── go.mod                      仍是單 module（理由見第六節）
├── server/                     ── agentuse-server，服務端
│   ├── cmd/agentuse-server/    main.go，只調 cli.Main
│   ├── internal/
│   │   ├── cli/                start / login / user；管理命令通過 sdk/golang 調 HTTP API
│   │   ├── controller/         HTTP 路由、鑑權中間件、handler、setup.sh / download、託管網頁
│   │   ├── hub/                daemon 的 WebSocket 長連接：握手、收發、派活、叫醒
│   │   ├── service/            業務編排：session 消息與 @、run 生命週期、work、daemon 註冊、附件配額
│   │   └── store/              SQLite 主庫：建表、查詢、事務原語
│   └── frontend/               管理網頁（React + Vite），embed.go 把 dist/ 嵌進二進制
├── client/                     ── agentuse，用戶電腦
│   ├── cmd/agentuse/           main.go，只調 cli.Main
│   ├── internal/
│   │   ├── cli/                register / daemon / status / harnesses / ui / session / work / attachment
│   │   ├── controller/         本機只讀 HTTP：只有 GET，只拿得到 store.Reader
│   │   ├── service/            常駐主循環（現在的 internal/daemon 整包平移）：daemon.json、連接與重連、
│   │   │                       收 dispatch、準備會話目錄和附件、跑 harness、插話、提問、彙報、對賬、寫賬本
│   │   └── store/              本機賬本 ~/.agentuse/ledger.db：Writer（給 service）、Reader（給 controller）
│   └── frontend/               本機只讀頁面：命令清單、token 統計；embed 進 agentuse
├── internal/                   ── client 和 server 共用，對倉庫外不公開
│   ├── protocol/               daemon ⇄ 服務的註冊接口和 WebSocket 消息
│   ├── harness/                內置 harness 描述（claude/、codex/ 的 harness.yaml）+ Spec 解析
│   ├── attachment/             附件文件名、ID 校驗（去掉其中的 HTTP 客戶端）
│   ├── command/                兩個 CLI 共用的子命令分發、flag、表格 / JSON 輸出
│   └── version/                Version，構建時用 -ldflags 注入
├── sdk/                        golang / nodejs / python，對外 SDK；兩個 CLI 也用 sdk/golang
├── test/e2e/                   e2e 場景（需要兩個二進制，所以留在頂層）
├── docs/
├── install.sh  Makefile  README.md  LICENSE
```

說明：

- cli 留在各自一側，不提到頂層：兩個 CLI 只有一半命令是調 sdk 的（server 的 login / user，client 的 session / work / attachment）。另一半直接用本端內部代碼：`start` 要打開 store、起 controller 和 hub；`daemon` 要起 service 和本機頁面；`register` / `status` / `harnesses` / `ui` 要讀寫 daemon.json、探測本機 harness。連調 sdk 的那幾個 client 命令也和本機綁在一起：session / attachment 沒傳參數時從 daemon.json 讀服務地址和密鑰，work 讀的是 service 起 harness 時設好的環境變量。cli 放到頂層的話就 import 不了 `server/internal`、`client/internal`，只能把這些 internal 去掉，client / server 的編譯期邊界也就沒了。
- `server/frontend`、`client/frontend` 都是 npm 工程加一個 embed 包，不放進 internal。
- `server/internal/hub` 是 server 獨有的一層：daemon 的 WebSocket 長連接是第二種入口，和 HTTP controller 平級。
- client 的 service 先保持一個包，不往下拆。以後如果它變得太大（比如再加幾種 harness），優先在包內按文件整理；真要拆，再在 `service/` 下建子包（比如 `service/harness`），不新增頂層的層。
- Go 社區更常見的叫法是 `handler`（對應 `net/http.Handler`）。這裏統一用 `controller`，兩邊一致即可。

### 術語

client 上「服務」這個詞容易混，文檔和代碼註釋裏統一這樣叫：

| 叫法 | 指什麼 |
|---|---|
| service、主循環 | `client/internal/service` 這個包，由 `agentuse daemon` 啓動 |
| 後臺服務 | `setup.sh` 把 `agentuse daemon` 裝成的 launchd / systemd 服務 |
| 本機頁面 | `client/internal/controller` + `client/frontend` 提供的只讀網頁 |
| daemon | 服務端視角下的「一臺用戶電腦」，對應 `daemons` 表和 `agentuse daemon` 命令 |

## 四、本機頁面

### 4.1 數據從哪來

**本機自己記，不去問服務端。** 用戶要的是「這臺機器實際接收過什麼」，這份記錄應該由這臺機器自己保存：服務端的數據可能被刪除，也可能不完整（比如派過來但被本機拒掉的命令），斷網時也能看。

```
服務端 ──run──▶ service（收到時寫一行；跑 harness；結束時回填狀態、token、耗時）
                  │
                  ▼ store.Writer
            ~/.agentuse/ledger.db
                  ▲ store.Reader（只讀連接）
                  │
            controller（只有 GET）◀── 瀏覽器
```

賬本第一版只建一張 `runs` 表（以 `run_id + attempt_id` 爲鍵）：

| 字段 | 來源 |
|---|---|
| 收到時間、結束時間、狀態（running / done / failed / canceled / rejected）、錯誤 | service |
| 服務地址、daemon_id | daemon.json |
| 發起人（用戶 id / 名字）、agent、session、work、harness | `protocol.Dispatch`（**需要擴展**，見 4.4） |
| prompt 摘要（默認前 200 字）、插話次數、附件數 | `Dispatch` / `Steer` |
| input / output / cache_read / cache_write tokens、cost_usd | service 從 harness 輸出解析（**需要擴展**，見 4.4） |

完整 prompt 默認不存。它本來就在會話目錄裏，賬本只保留能索引到那裏的信息。另外保留天數可以在 daemon.json 裏配置，默認 90 天。

### 4.2 「只讀」怎麼保證

三道防線，前兩道由編譯器和數據庫保證，不依賴開發者自覺：

1. **類型層**：client 的 store 對外分成 `Writer` 和 `Reader` 兩個接口。`controller.New` 只接受 `store.Reader`，controller 包裏拿不到任何寫方法。
2. **數據庫層**：Reader 用一條單獨的連接打開 `file:ledger.db?mode=ro`，並設置 `PRAGMA query_only=1`。即使有人在 Reader 裏寫了 UPDATE 也會執行失敗。賬本開 WAL，service 寫的時候讀不會被阻塞。
3. **HTTP 層**：只註冊 `GET` 路由，其他方法一律 405；不返回任何 CORS 頭。

### 4.3 進程和安全

- **跑在 `agentuse daemon` 進程裏。** 這個進程已經被後臺服務託管、一直在線，用戶隨時打開就能看，不用再管一個進程。`cli daemon` 啓動時同時起 service 和 controller，兩者共用一個 store 實例，但分別只拿 Writer 和 Reader。daemon.json 裏新增 `ui_addr`，默認 `127.0.0.1:7801`，設爲空就關掉本機頁面。
- **只監聽迴環地址**，並校驗 `Host` 頭只能是 `127.0.0.1:<port>` / `localhost:<port>`，防止 DNS rebinding 讓外部網頁讀到數據。
- **訪問令牌**：多用戶機器上，其他系統賬號也能連本機迴環端口，而賬本里有 prompt 摘要。啓動時在 `~/.agentuse/ui.token`（權限 0600）生成一個令牌，`agentuse ui` 打印（或直接打開）帶令牌的鏈接；瀏覽器第一次訪問後寫入 cookie。做法和 Jupyter 一樣。
- `agentuse status` 增加一行，顯示本機頁面的地址。

### 4.4 需要配合改動的地方

| 改動 | 位置 | 說明 |
|---|---|---|
| `Dispatch` 增加發起人和 work 信息 | `internal/protocol`、`server/internal/service` | 現在只有 `agent_id`、`session_id`，回答不了「來自誰」。增加 `sender`（id、name）、`work_id`。只增加字段，舊 daemon 會忽略 |
| `Result` 增加 `usage` | `client/internal/service` | Claude 從 stream-json 的 `result.usage` 取，Codex 從 app-server 的 token usage 通知取；自定義 harness 可以在結果裏自己帶 `usage`，不帶就留空 |
| `done` / `failed` 的 payload 帶上 usage | `internal/protocol` | 結果本來就會上報，順帶讓服務端也拿到 token 數，以後服務端想做統計不用再改協議 |

### 4.5 本機網頁

`client/frontend` 是一個獨立的 Vite 工程，技術棧和 `server/frontend` 相同（React + shadcn/ui），第一版只有兩頁：

- **命令清單**：按時間倒序，按發起人 / agent / 狀態 / 時間過濾，點開看詳情；有會話目錄時顯示路徑。
- **統計**：按天、按發起人、按 agent、按 harness 彙總 token 和次數。

兩個前端先各自獨立。等真正重複的組件多起來，再用 npm workspaces 抽出一個共用包，不提前抽。

## 五、依賴規則

```
server/cmd ──▶ server/internal/cli ──▶ controller, hub ──▶ service ──▶ store
                                           │                              ▲
                                           └──── 簡單讀：可以直接查 ───────┘

client/cmd ──▶ client/internal/cli ──┬──▶ service ────▶ store.Writer
                                     └──▶ controller ─▶ store.Reader

server/**, client/** ──▶ internal/*, sdk/golang
```

1. **client 和 server 互不 import。** Go 的 internal 規則會保證這一點，不需要靠 code review。
2. **根 `internal/*` 不 import client 或 server。** 它只放跨進程的約定，不放業務。不叫 `common`、`util`、`shared`，每個包按「提供什麼」命名。
3. **server 內部單向依賴**：controller / hub → service → store；store 不 import 上層。寫操作和跨表編排必須經過 service；純讀的接口（列表、詳情）允許 controller 直接查 store，這是參考文中 multica 讀路徑的寫法，不用爲了分層再加一層轉發。
4. **client 的 controller 和 service 互不 import**：兩者唯一的交集是 store，而且一個只拿 Reader、一個只拿 Writer。裝配在 `cli daemon` 裏完成。這和第 3 條是同一個原則：client 的 controller 只讀，所以永遠走「直接查 store」那條路。
5. **hub 和 service 的雙向聯繫用接口解開**：server 的 service 在新消息、取消、插話時要叫醒 daemon，就在 service 裏定義 `Notifier` 接口，由 hub 實現，在 `cli start` 裏裝配。
6. **HTTP API 的類型只有一份，就是 `sdk/golang`**：兩個 CLI 刪掉自己的 apiClient，改用 `sdk/golang.Client`。這樣 SDK 自己也一直有人在用，接口變了第一時間就會發現。

## 六、爲什麼仍用單 module

文中 etcd 用 go.work 把 14 個 module 串起來，前提是這些 module 各自獨立發版、依賴輕重差別很大。agentuse 現在的情況：

- Go 代碼約 1 萬行，client 和 server 一起發版；README 裏也寫了「服務端和 daemon 要一起升級」。
- 兩邊的第三方依賴基本相同：client 的 store 也用 SQLite（同一個純 Go 驅動 `modernc.org/sqlite`，`CGO_ENABLED=0` 交叉編譯不受影響），拆 module 不能減少任何依賴。

所以保持單 module。以後如果 `sdk/golang` 要單獨打版本號給外部用戶 `go get`，再給它加一個 go.mod，那是第一個值得拆出去的 module。

> 賬本沒有選 JSONL 追加文件：寫起來更簡單，但按發起人、按天聚合，以及分頁、保留期清理都要自己實現，也做不到數據庫層的只讀。現在的 `agentuse` 本來就因爲鏈接了 server 的 store 而帶着 SQLite（約 11 MB），所以用 SQLite 不會讓二進制變大。

## 七、文件去向

### server

| 現在 | 去向 |
|---|---|
| `cmd/agentuse-server/main.go` | `server/cmd/agentuse-server/main.go` |
| `internal/cli/server.go` | `server/internal/cli/`（start、bootstrapAdmin、user） |
| `internal/cli/client.go` | `server/internal/cli/`（只保留 login 和 server-cli.json，apiClient 換成 sdk/golang） |
| `internal/server/server.go` | `server/internal/controller/`（routes、writeJSON / readJSON、storeErr） |
| `internal/server/api.go` | `server/internal/controller/`（鑑權、act-as、各 handler）；其中的業務判斷移到 service |
| `internal/server/runs.go`、`attachments.go` | `server/internal/controller/`（HTTP 部分）+ `service/`（附件配額、清理、創建 session 的編排） |
| `internal/server/setup.go`、`web.go` | `server/internal/controller/` |
| `internal/server/hub.go` | `server/internal/hub/` |
| `internal/store/run_lifecycle.go`、`run_work.go` | 業務規則（領取、租約、結束、重排、對賬、work 令牌）移到 `service/`；SQL 和事務原語留在 `store/` |
| `internal/store/*` 其餘 | `server/internal/store/` |
| `frontend/` | `server/frontend/` |

### client

| 現在 | 去向 |
|---|---|
| `cmd/agentuse/main.go` | `client/cmd/agentuse/main.go` |
| `internal/cli/agentuse.go`、`session.go`、`work.go`、`attachment.go` | `client/internal/cli/`；`store.*` 類型和 `attachment.Client` 換成 sdk/golang |
| `internal/daemon/*`（全部文件，包括 config.go） | `client/internal/service/`，包名 `daemon` 改爲 `service` |
| —— | 新建 `client/internal/store/`、`client/internal/controller/`、`client/frontend/` |

### 共用

| 現在 | 去向 |
|---|---|
| `internal/protocol/` | 不動 |
| `harness/` | `internal/harness/`（yaml 跟着走，`//go:embed` 是相對路徑，不受影響） |
| `internal/attachment/types.go` | 不動；`client.go` 刪掉 |
| `internal/cli/common.go` | `internal/command/`（dispatch、flag、表格）；`Version` 移到 `internal/version/` |

## 八、遷移步驟

每一步單獨一個 PR，`make test`、`make e2e` 和前端構建都要通過。

1. **先消除跨端依賴**（不移動目錄）
   - client 側 CLI 改用 `sdk/golang`，刪掉對 `store` 的引用和 `attachment.Client`；server 側管理命令同樣改用 `sdk/golang`。
   - `Version` 移到 `internal/version`，Makefile 和 e2e 裏的 `-X` 路徑一起改。
   - 這一步不做，後面一移目錄就會編譯失敗；先做也方便單獨 review。
2. **拆 cli**：`internal/cli` 拆成 client、server 兩部分，共用部分放進 `internal/command`。
3. **移動目錄**（純 `git mv` 加改 import，不改邏輯）
   - `cmd/*` → `{client,server}/cmd/`；`internal/{server,store}` → `server/internal/`；`internal/daemon` → `client/internal/service`；`frontend/` → `server/frontend/`；`harness/` → `internal/harness/`。
   - 同步修改：Makefile（`./cmd/…`、`frontend` 路徑）、`.github/workflows/*`（`cache-dependency-path`、`working-directory`）、`test/e2e/e2eutil`（二進制的包路徑）、README 的「佈局」一節、docs 裏指向代碼的鏈接。
   - `install.sh` 和 release 產物的文件名不變，用戶這邊感知不到。
4. **server 拆層**：hub 獨立成包；server 包改名爲 controller；定義 `service.Notifier`。
5. **本機頁面**（依賴第 3 步）
   - 協議：`Dispatch` 增加 sender、work_id；`Result` 增加 usage；client 的 service 解析 Claude、Codex 的 token 用量。
   - 新建 `client/internal/store`（Writer / Reader、保留期清理），service 在收到和結束時寫賬本。
   - 新建 `client/internal/controller`、`client/frontend`、`agentuse ui`；Makefile 的 `web`、`dist` 和 CI 都要先構建 `client/frontend`，因爲 `agentuse` 也要 embed 網頁。
   - e2e 用 fakeharness 跑一次 run，斷言本機頁面的 API 能查到這條記錄和 token 數，並斷言非 GET 請求返回 405。
6. **逐步抽出 server 的 service**：按 run 生命週期 → session 消息與 @ → work → daemon 註冊 → 附件配額的順序，一次搬一塊。store 現在是單連接加 `BEGIN IMMEDIATE`，很多規則寫在事務裏，搬的方式是：store 提供 `Tx(ctx, func(q) error)` 和細粒度查詢，事務邊界由 service 決定。

第 1–3 步是機械改動，風險低，可以很快合入。第 4、5、6 步互不依賴，可以並行。

## 九、待確認

- [x] 共用代碼放根 `internal/`（對外不公開），還是放 `pkg/`（像 multica 那樣表示「跨進程約定」，但對外公開）。方案傾向 `internal/`：能不公開就不公開。
- [x] 本機頁面的默認端口（暫定 7801）、是否默認開啓（暫定開啓，只監聽 127.0.0.1）。
- [x] 賬本存不存完整 prompt：暫定只存前 200 字摘要，完整內容去會話目錄看。
- [x] 「來自哪」要細到什麼程度：暫定爲服務地址 + 發起人 + agent / session / work。如果一臺機器以後要連多個服務，賬本已經按服務地址區分，不需要再改表。
- [x] 兩個前端什麼時候抽共用組件包：暫定等重複明顯以後再說。

實現採用根 `internal/`、默認開啓的迴環端口 7801、200 字摘要和 90 天保留期。兩個前端保持獨立；本機發起人字段表示會話創建者。SQL、schema 和行解碼保留在 server store，事務編排、通知與附件文件生命週期由 service 負責。
