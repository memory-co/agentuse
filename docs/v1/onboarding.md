# 接入指南:從零到第一個 @claude

這篇按順序走完一次完整的接入:**部署 hub → 接入平臺 → 接上用戶電腦 → 驗證**。讀完之後,平臺評論區裏的一個 `@claude` 就能叫醒你電腦上的 Claude Code,在你的倉庫裏幹活,再把回覆寫回評論區。

機制和取捨見 [designs/](designs/README.md),字段見 [structs/](structs/README.md),接口見 [apis/](apis/README.md)。這裏只講怎麼做。

---

## 0. 三個角色,先分清誰做什麼

| 角色 | 是誰 | 要做的事 | 章節 |
|---|---|---|---|
| **實例主人** | 部署 hub 的人或團隊(個人博客就是站長自己) | 起 hub、登記平臺和 daemon、發密鑰 | [§1](#1-部署-hub實例主人) |
| **平臺開發者** | 工單系統 / 博客 / 論壇的開發者 | 評論存庫後 notify、實現回調、建 agent 應用用戶 | [§2](#2-接入平臺平臺開發者) |
| **用戶** | 讓 agent 在自己電腦上幹活的人 | 裝 Claude Code 和 agentuse、起 daemon、配倉庫 | [§3](#3-接上用戶電腦用戶) |

一個人可以同時是三個角色(比如給自己博客接 agent 的站長),也可以分別是三撥人(公司工單系統:平臺組接入、運維組部署 hub、每個工程師在自己電腦上起 daemon)。

三方之間只交換三類密鑰,各自只拿自己那份:

```
            secret / callback_secret           daemon key
  平臺  ◀───────────────────────────▶  hub  ◀────────────────▶  daemon(用戶電腦)
                                         │
                       token_key、admin_key 只在 hub 自己手裏
```

- **平臺密鑰**(`secret`,可選再加一把 `callback_secret`):實例主人發給平臺開發者。
- **daemon 密鑰**:實例主人爲每臺用戶電腦發一把。
- **token_key / admin_key**:只留在 hub,誰也不給。

整個過程中,平臺不接觸用戶電腦,用戶電腦也不需要能被訪問。

---

## 1. 部署 hub(實例主人)

### 1.1 準備

- 一臺平臺和用戶電腦都能訪問到的機器(雲主機、家裏的 NAS,或者本地開發時就是你自己的電腦);
- Go 1.24+(用來裝 agentuse);
- 對外提供服務時要一個域名和 TLS,比如 `hub.alice.dev`。

### 1.2 安裝

```bash
go install github.com/memory-co/agentuse/client/cmd/agentuse@latest
agentuse version
```

### 1.3 生成密鑰

每把密鑰都是一段隨機串,用不同的值:

```bash
openssl rand -hex 32   # token_key:籤運行令牌
openssl rand -hex 32   # admin_key:管理接口
openssl rand -hex 32   # 平臺 shop 的 secret
openssl rand -hex 32   # 平臺 shop 的 callback_secret(可選,推薦)
openssl rand -hex 32   # daemon alice-mbp 的 key
```

### 1.4 寫 hub.json

放在 `~/.agentuse/hub.json`(`AGENTUSE_HOME` 可以改目錄)。先只登記 hub 自己的配置,平臺和 daemon 在 §2、§3 里加:

```json
{
  "listen": "127.0.0.1:7700",
  "store": "sqlite",
  "token_key": "<token_key>",
  "admin_key": "<admin_key>",
  "lease_seconds": 120,
  "max_attempts": 3,
  "daemon_keys": {},
  "platforms": []
}
```

| 字段 | 說明 |
|---|---|
| `listen` | 只監聽本機,對外由反向代理加 TLS |
| `store` | `sqlite` 會把會話和運行存進 `~/.agentuse/hub.db`,重啓不丟 |
| `lease_seconds` / `max_attempts` | daemon 掉線後多久把運行收回重排、最多重試幾次;默認值一般不用改 |

完整字段見 [structs/filesystem.md](structs/filesystem.md#hubjson)。

### 1.5 加 TLS,起服務

用任意反向代理把 `https://hub.alice.dev` 轉到 `127.0.0.1:7700`。它要能轉發 WebSocket(daemon 用 `/v1/daemon/connect` 長連)。Caddy 默認就行:

```
hub.alice.dev {
	reverse_proxy 127.0.0.1:7700
}
```

nginx 要顯式放行升級頭:

```nginx
location / {
    proxy_pass http://127.0.0.1:7700;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 1h;          # 推送連接會空閒很久,別被代理掐掉
}
```

起服務:

```bash
agentuse hub start
# agentuse hub listening on 127.0.0.1:7700 (0 platforms, store sqlite)

curl https://hub.alice.dev/v1/health
# {"status":"ok"}
```

長期運行時交給 systemd(Linux):

```ini
# /etc/systemd/system/agentuse-hub.service
[Unit]
Description=agentuse hub
After=network-online.target

[Service]
User=agentuse
ExecStart=/home/agentuse/go/bin/agentuse hub start
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

**改 hub.json 之後要重啓 hub**,配置只在啓動時讀一次。

---

## 2. 接入平臺(平臺開發者)

以一個工單系統 `shop` 爲例。博客、論壇的接法一樣,區別只在策略(§2.2)。

### 2.1 跟實例主人對齊四件事

| 事 | 例子 | 誰定 |
|---|---|---|
| 平臺 id | `shop` | 雙方約定,出現在 notify 的地址裏 |
| 回調地址 | `https://shop.example.com/agentuse/callback` | 平臺 |
| agent 應用用戶 | 名字 `claude`,id `u_claude` | 平臺(§2.3) |
| 策略 | 誰能喚醒、agent 能做什麼 | 雙方商量(§2.2) |

實例主人把 `secret`(和 `callback_secret`)交給平臺開發者。

### 2.2 實例主人登記平臺

在 hub.json 的 `platforms` 里加一項,然後重啓 hub:

```json
{
  "id": "shop",
  "secret": "<shop secret>",
  "callback_secret": "<shop callback_secret>",
  "callback_url": "https://shop.example.com/agentuse/callback",
  "policy": {
    "agent_name": "claude",
    "agent_user_id": "u_claude",
    "wake_roles": ["member"],
    "trusted_roles": ["member"],
    "actions": ["reply", "status"],
    "workspace_mode": "readwrite"
  }
}
```

策略按平臺的性質挑一行改,理由見 [designs/wake-and-trust.md](designs/wake-and-trust.md):

| 平臺 | `wake_roles` | `trusted_roles` | `actions` | `workspace_mode` |
|---|---|---|---|---|
| 公司內工單系統 | `member` | `member` | `reply` `status` | `readwrite` |
| 個人公開博客 | `owner` | `owner` | `reply` | `readonly` |
| 開源論壇 | `member`(維護者) | `member` | `reply` `label` | `readonly` |

**公開平臺一定只讓 `owner` / 維護者喚醒**。陌生訪客的評論只會作爲「不可信材料」交給 agent,不會被當成指令。

### 2.3 在平臺裏建 agent 應用用戶

在用戶表裏加一個代表 agent 的用戶,比如名字 `claude`、id `u_claude`:

- 評論裏 `@claude` 提到的就是它,名字要和策略的 `agent_name` 一致;
- agent 的回覆以它的名義發出;
- 它的 id 填進策略的 `agent_user_id`,hub 據此忽略 agent 自己的評論,防止它叫醒自己。

它不需要能登錄,也不佔席位。

### 2.4 給平臺配置地址和密鑰

平臺從配置或設置頁讀三個值:

```bash
AGENTUSE_URL=https://hub.alice.dev
AGENTUSE_PLATFORM=shop
AGENTUSE_SECRET=<shop secret>
AGENTUSE_CALLBACK_SECRET=<shop callback_secret>   # 沒拆就和 AGENTUSE_SECRET 一樣
```

換 hub 實例只需要改這幾個值,平臺代碼不動。

### 2.5 評論存庫後 notify

每條評論**存庫成功之後**都調一次 notify,不用自己判斷有沒有 @ agent,hub 會判斷。

**線程鍵**是平臺定的字符串,同一個線程的評論必須用同一個鍵,同一個鍵就續接同一段 agent 會話。慣例是 `<類型>:<鍵>`:`issue:MUL-42`、`blog:/posts/tmux`、`topic:8812`。

**作者角色**(`owner` / `member` / `guest`)由平臺根據自己的權限算出來。這是 hub 判斷能不能喚醒的唯一依據,不要從評論內容裏推斷。

**context**(可選,推薦帶):同一線程裏最近的其它評論。agent 能看到前文,hub 會按策略把它們標成可信 / 不可信。

Go 平臺用 SDK:

```go
import "github.com/memory-co/agentuse/pkg/sdk"

var hub = sdk.New(os.Getenv("AGENTUSE_URL"), os.Getenv("AGENTUSE_PLATFORM"), os.Getenv("AGENTUSE_SECRET"))

func init() { hub.CallbackSecret = os.Getenv("AGENTUSE_CALLBACK_SECRET") }

func onCommentSaved(ctx context.Context, c Comment, recent []Comment) {
	res, err := hub.Notify(ctx, sdk.Comment{
		ID:      c.ID,
		Thread:  "issue:" + c.IssueKey,
		Author:  sdk.Author{ID: c.Author.ID, Name: c.Author.Name, Role: roleOf(c.Author)},
		Text:    c.Body,
		Context: toSDK(recent),
	})
	if err != nil {
		log.Printf("agentuse notify: %v", err) // 不影響評論本身,記日誌即可
		return
	}
	if res.Woken {
		showThinkingPlaceholder(c.IssueKey) // 可選:先放一個「正在思考」
	}
}
```

其它語言照 [apis/platform.md](apis/platform.md#簽名) 自己簽名:`HMAC-SHA256(secret, "<unix 秒>.<原始請求體>")`,hex 編碼,前綴 `v1=`。用 shell 演示一遍:

```bash
BODY='{"id":"c_1001","thread":"issue:MUL-42","author":{"id":"u_bob","name":"bob","role":"member"},"text":"@claude 退款金額算錯了"}'
TS=$(date +%s)
SIG="v1=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$AGENTUSE_SECRET" -hex | awk '{print $NF}')"

curl -sS "$AGENTUSE_URL/v1/platforms/shop/notify" \
  -H 'Content-Type: application/json' \
  -H "X-AgentUse-Timestamp: $TS" \
  -H "X-AgentUse-Signature: $SIG" \
  -d "$BODY"
# 202 {"woken":true,"reason":"woken","session_id":"s_…","run_id":"r_…"}
```

簽名覆蓋的是**原樣發出去的字節**:先序列化成字符串,籤這個字符串,再原樣發送,中間不要重新格式化。

### 2.6 實現回調

hub 會 POST 到 `callback_url`,帶 hub 的簽名。平臺要做三件事:**驗籤、處理事件、回 2xx**。

```go
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
	switch ev.Type {
	case "reply":
		// 以 agent 應用用戶的名義寫一條評論;InReplyTo 是叫醒它的那條評論,可以掛成樓中樓
		return comments.Create(ctx, ev.Thread, "u_claude", ev.Text, ev.InReplyTo)
	case "status":
		// 可選:博客這類沒有狀態的平臺不用實現,策略裏也別給 status
		return issues.SetStatus(ctx, ev.Thread, ev.Status) // 如 in_review
	case "activity":
		// 可選:把「正在運行測試」之類的進度顯示出來,或直接忽略
		return progress.Show(ctx, ev.Thread, ev.Activity.Type, ev.Activity.Body)
	}
	return nil
}))
```

非 Go 平臺驗籤的寫法(Node / TypeScript):

```ts
import { createHmac, timingSafeEqual } from "node:crypto";

function verify(req: { headers: Record<string, string>; rawBody: string }, secret: string): boolean {
  const ts = req.headers["x-agentuse-timestamp"];
  const sig = req.headers["x-agentuse-signature"] ?? "";
  if (!ts || Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false;
  const want = "v1=" + createHmac("sha256", secret).update(`${ts}.${req.rawBody}`).digest("hex");
  return sig.length === want.length && timingSafeEqual(Buffer.from(sig), Buffer.from(want));
}
```

要點:

- 用 `callback_secret` 驗(沒拆就是 `secret`);
- 驗**原始請求體**,不要先解析再序列化;
- 處理失敗就回非 2xx,hub 會把失敗交還給 agent,由它決定重試;
- 事件裏的 `thread` 和 `run_id` 由 hub 擔保,可以直接用來定位工單。

事件格式見 [apis/platform.md](apis/platform.md#post-callback_url)。

---

## 3. 接上用戶電腦(用戶)

### 3.1 準備

- 裝好 [Claude Code](https://claude.com/claude-code) 並登錄(`claude` 能在終端裏直接跑)。推理用的是你自己的訂閱。
- 裝好 `git`。
- 裝 agentuse,並確認它在 `PATH` 裏。agent 在運行裏要調 `agentuse reply`,找不到命令就交不了活:

```bash
go install github.com/memory-co/agentuse/client/cmd/agentuse@latest
which agentuse
```

### 3.2 跟實例主人要一把 daemon 密鑰

給這臺電腦起個 id,比如 `alice-mbp`。實例主人把它登記進 hub.json 並重啓 hub:

```json
"daemon_keys": {
  "alice-mbp": "<alice-mbp key>"
}
```

一臺電腦一個 id。要在同一臺電腦上並行跑多條運行,就多登記幾個 id、多起幾個 daemon。

### 3.3 寫 daemon.json

放在用戶電腦的 `~/.agentuse/daemon.json`:

```json
{
  "hub_url": "https://hub.alice.dev",
  "id": "alice-mbp",
  "key": "<alice-mbp key>",
  "driver": "claude",
  "transport": "ws",
  "workspaces": {
    "shop": { "repo": "~/work/shop-api", "base_ref": "main" },
    "blog": {}
  }
}
```

| 字段 | 說明 |
|---|---|
| `workspaces` | 平臺 id → 在哪個倉庫幹活。每個線程會從 `base_ref` 切出一個獨立的 git worktree,分支名像 `agent/claude/MUL-42`,不會碰你正在用的工作目錄 |
| `blog: {}` | 沒有代碼可改的平臺(博客問答)給一個空目錄就行 |
| `transport` | `ws`(默認):評論一來馬上開工。公司代理不支持 WebSocket 時改成 `poll` |

`repo` 必須是一個已經 clone 好、有 `base_ref` 分支的本地倉庫。worktree 放在 `~/.agentuse/worktrees/<平臺>/<會話>/`。

### 3.4 起 daemon

```bash
agentuse daemon start
# agentuse daemon alice-mbp → https://hub.alice.dev (ws)
```

daemon 只往外連 hub,電腦在 NAT 或公司內網後面都沒關係。斷網後它每 5 秒自動重連。

想開機自啓:macOS 寫一個 launchd plist,`ProgramArguments` 填 `agentuse daemon start`;Linux 用 `systemctl --user` 跑同樣的命令。要讓 daemon 進程的 `PATH` 裏能找到 `claude`、`git` 和 `agentuse`。

---

## 4. 驗證:走一遍 @claude

1. **在平臺上發一條評論**:用一個 `wake_roles` 裏的賬號,在工單 MUL-42 下寫「@claude 退款金額算錯了」。
   - notify 返回 202、`woken: true`:hub 收下了。
   - 返回 200:沒喚醒,看 `reason`(見 §5)。
2. **看 hub 裏的運行**(實例主人):

   ```bash
   export AGENTUSE_URL=https://hub.alice.dev AGENTUSE_ADMIN_KEY=<admin_key>
   agentuse admin runs -limit 5
   ```

   `status` 從 `queued` 很快變成 `claimed`,`daemon_id` 是 `alice-mbp`。
3. **看用戶電腦**:daemon 打出 `run r_…: issue:MUL-42 (s_…, attempt 1)`,`~/work/shop-api` 旁邊多了一個 worktree,分支是 `agent/claude/MUL-42`。
4. **看平臺**:agent 回覆以 `claude` 的名義出現在評論區;策略允許的話,工單狀態變成 `in_review`。
5. **再 @ 一次**:同一線程的下一條評論會續接同一段 Claude 會話(`--resume`),複用同一個 worktree,agent 記得上次做了什麼。

---

## 5. 排錯

| 現象 | 看哪 | 常見原因 |
|---|---|---|
| notify 401 `bad_signature` | 平臺日誌 | 密鑰不對;籤的不是原始請求體;機器時鐘和 hub 差超過 5 分鐘 |
| notify 404 `unknown_platform` | hub.json | 平臺 id 沒登記,或改完 hub.json 沒重啓 hub |
| notify 200,`reason: no_mention` | 評論正文 | `@` 後面的名字和 `agent_name` 不一致;`@claudette`、`a@claude.dev` 這種不算 |
| notify 200,`reason: role_denied` | 平臺算的角色 | 作者角色不在 `wake_roles` 裏 |
| notify 200,`reason: self_comment` | — | agent 自己的評論,正常現象 |
| 運行一直 `queued` | `agentuse admin runs`、daemon 日誌 | daemon 沒起或連不上;daemon 日誌裏 `401` 說明 id / key 和 hub.json 對不上;同一線程上一條還在跑(同線程串行,正常) |
| daemon 日誌 `connect: …` 反覆出現 | 反向代理 | 代理沒放行 WebSocket 升級;臨時可改 `transport: poll` |
| 運行 `failed`,`error: lease expired` | daemon 日誌 | daemon 跑到一半掉線超過 `lease_seconds`,重試 `max_attempts` 次都沒成 |
| 運行 `failed`,`error: git worktree add: …` | daemon.json | `repo` 路徑不對,或沒有 `base_ref` 分支 |
| 運行 `succeeded` 但平臺沒收到回覆 | hub 日誌、平臺回調日誌 | agent 沒調 `agentuse reply`(多半是 `PATH` 裏沒有 agentuse);回調驗籤失敗(平臺用錯了 `callback_secret`);回調地址不通 |
| agent 調 `agentuse status` 得到 403 | 策略 | `actions` 裏沒有 `status`,這是策略在起作用 |

---

## 6. 上線前檢查

- [ ] 每把密鑰都是獨立的隨機值,沒有進代碼倉庫;`token_key`、`admin_key` 只在 hub 上。
- [ ] hub 只監聽 `127.0.0.1`,對外只有帶 TLS 的反向代理。
- [ ] 公開平臺的 `wake_roles` 只有 `owner` / 維護者,`workspace_mode` 是 `readonly`,`actions` 只給 `reply`。
- [ ] 平臺拆了 `callback_secret`。
- [ ] 平臺只在評論**存庫之後** notify,並且角色由平臺權限算出,不取自評論內容。
- [ ] daemon 每臺電腦一個 id;電腦不用了就從 `daemon_keys` 裏刪掉並重啓 hub。
- [ ] 知道公開平臺的風險:「只讀」目前靠 Claude Code 的權限模式保證,容器隔離還在 [roadmap](designs/roadmap.md) 上。陌生人能看到 agent 回覆的平臺,不要讓 agent 接觸帶憑據的倉庫。
