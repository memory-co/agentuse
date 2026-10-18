# 接入指南:从零到第一个 @alice-claude

这篇按顺序走完一次完整的接入:**运维起 hub → 平台接入、替用户建 agent → 用户在自己的仓库里跑 `agentuse connect` → `agentuse daemon start` → 在评论里 @ 验证**。读完之后,平台评论区里的一个 `@alice-claude` 就能叫醒 alice 电脑上的 Claude Code,在她的仓库里干活,再把回复写回评论区。

机制和取舍见 [designs/](designs/README.md),字段见 [structs/](structs/README.md),接口见 [apis/](apis/README.md)。这里只讲怎么做。

> 安装脚本(`curl … | sh`)、开机自启、环境变量配置 hub、Docker 镜像还没做([roadmap](designs/roadmap.md)),所以下面 agentuse 都用 `go install` 装,daemon 手动起。

---

## 0. 三个角色,先分清谁做什么

| 角色 | 是谁 | 要做的事 | 章节 |
|---|---|---|---|
| **运维(实例主人)** | 部署 hub 的人或团队(个人博客就是站长自己) | 起 hub、登记平台、配 `public_url` | [§1](#1-部署-hub运维) |
| **平台开发者** | 工单系统 / 博客 / 论坛的开发者 | 设置页替用户建 agent、评论存库后 notify、实现回调 | [§2](#2-接入平台平台开发者) |
| **用户** | 让 agent 在自己电脑上干活的人 | 在设置页建 agent,在自己仓库里跑一条命令,起 daemon | [§3](#3-接上用户电脑用户) |

一个人可以同时是三个角色(比如给自己博客接 agent 的站长),也可以分别是三拨人(公司工单系统:运维组部署 hub、平台组接入、每个工程师在自己电脑上接自己的 agent)。

三方之间交换的凭据,各自只拿自己那份:

```
            secret / callback_secret                  设备密钥(自动)
  平台  ◀───────────────────────────▶  hub  ◀────────────────────────▶  daemon(用户电脑)
    │                                   │                                   ▲
    │                     token_key、admin_key 只在 hub 自己手里              │
    └──── 接入码(一次性,15 分钟):平台只展示给 agent 的主人 ────────────────┘
```

- **平台密钥**(`secret`,可选再加一把 `callback_secret`):运维发给平台开发者。
- **接入码**:平台调 hub 建 agent 时拿到,只展示给 agent 的主人;用户兑换一次就作废。
- **设备密钥**:用户兑换接入码时 hub 自动发给那台电脑,写进它的 `daemon.json`;没有人需要手动传。
- **token_key / admin_key**:只留在 hub,谁也不给。

整个过程中,平台不接触用户电脑,用户电脑也不需要能被访问。

---

## 1. 部署 hub(运维)

### 1.1 准备

- 一台平台和用户电脑都能访问到的机器(云主机、家里的 NAS,或者本地开发时就是你自己的电脑);
- Go 1.24+(用来装 agentuse);
- 对外提供服务时要一个域名和 TLS,比如 `agentuse.shop.example.com`。

### 1.2 安装

```bash
go install github.com/memory-co/agentuse/cmd/agentuse@latest
agentuse version
```

### 1.3 生成密钥

每把密钥都是一段随机串,用不同的值:

```bash
openssl rand -hex 32   # token_key:签运行令牌
openssl rand -hex 32   # admin_key:管理接口
openssl rand -hex 32   # 平台 shop 的 secret
openssl rand -hex 32   # 平台 shop 的 callback_secret(可选,推荐)
```

用户电脑的密钥不用在这里生成:每台电脑接入时 hub 自己发。

### 1.4 写 hub.json

放在 `~/.agentuse/hub.json`(`AGENTUSE_HOME` 可以改目录):

```json
{
  "listen": "127.0.0.1:7700",
  "public_url": "https://agentuse.shop.example.com",
  "store": "sqlite",
  "token_key": "<token_key>",
  "admin_key": "<admin_key>",
  "lease_seconds": 120,
  "max_attempts": 3,
  "platforms": [
    {
      "id": "shop",
      "secret": "<shop secret>",
      "callback_secret": "<shop callback_secret>",
      "callback_url": "https://shop.example.com/agentuse/callback",
      "policy": {
        "wake_roles": ["member"],
        "trusted_roles": ["member"],
        "actions": ["reply", "status"],
        "workspace_mode": "readwrite",
        "queue_ttl_seconds": 86400
      }
    }
  ]
}
```

| 字段 | 说明 |
|---|---|
| `listen` | 只监听本机,对外由反向代理加 TLS |
| `public_url` | **用户电脑**访问 hub 的地址,写进给用户的接入命令。不配就按平台调 hub 时的 Host 推断——平台走内网地址时会推断错,对外部署一定要配 |
| `store` | `sqlite` 会把用户、agent、设备、会话和运行存进 `~/.agentuse/hub.db`,重启不丢 |
| `lease_seconds` / `max_attempts` | daemon 掉线后多久把运行收回重排、最多重试几次;默认值一般不用改 |
| `platforms[].id` | 平台 id,和平台开发者约定,出现在接口路径里 |
| `platforms[].policy` | 这个平台上**每个 agent 的上限**,见下表 |

策略按平台的性质挑一行改,理由见 [designs/wake-and-trust.md](designs/wake-and-trust.md):

| 平台 | `wake_roles` | `trusted_roles` | `actions` | `workspace_mode` |
|---|---|---|---|---|
| 公司内工单系统 | `member` | `member` | `reply` `status` | `readwrite` |
| 个人公开博客 | `owner` | `owner` | `reply` | `readonly` |
| 开源论坛 | `member`(维护者) | `member` | `reply` `label` | `readonly` |

- `wake_roles` 只对设成 `wake=members` 的 agent 起作用:agent 的主人总能叫醒自己的 agent,默认(`wake=owner`)也只有主人能叫醒。
- `actions` / `workspace_mode` 是上限:每个 agent 可以设得更严,不能更松。`workspace_mode` 不配按只读算。
- `queue_ttl_seconds`:agent 的电脑不在线时,运行最多排多久(默认 24 小时);过期判失败,并回调平台一条「电脑一直不在线」的提示。

**公开平台一定只让 `owner` / 维护者唤醒**。陌生访客的评论只会作为「不可信材料」交给 agent,不会被当成指令。

完整字段见 [structs/filesystem.md](structs/filesystem.md#hubjson)。

### 1.5 加 TLS,起服务

用任意反向代理把 `https://agentuse.shop.example.com` 转到 `127.0.0.1:7700`。它要能转发 WebSocket(daemon 用 `/v1/daemon/connect` 长连)。Caddy 默认就行:

```
agentuse.shop.example.com {
	reverse_proxy 127.0.0.1:7700
}
```

nginx 要显式放行升级头:

```nginx
location / {
    proxy_pass http://127.0.0.1:7700;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 1h;          # 推送连接会空闲很久,别被代理掐掉
}
```

起服务:

```bash
agentuse hub start
# agentuse hub listening on 127.0.0.1:7700 (1 platforms, store sqlite)

curl https://agentuse.shop.example.com/v1/health
# {"status":"ok"}
```

长期运行时交给 systemd(Linux):

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

**改 hub.json 之后要重启 hub**,配置只在启动时读一次。agent 和设备不在 hub.json 里,增删它们不用重启。

> 从旧版本升级:hub.db 的表结构变了,启动时发现版本不对会**删表重建**,旧的会话和运行记录会丢。hub.json 里的 `daemon_keys` 和策略里的 `agent_name` / `agent_user_id` 已经不用了,删掉即可。

最后把平台 id、`secret`(和 `callback_secret`)、hub 地址交给平台开发者。

---

## 2. 接入平台(平台开发者)

以一个工单系统 `shop` 为例。博客、论坛的接法一样,区别只在运维配的策略。

### 2.1 配置地址和密钥

平台从配置读这几个值:

```bash
AGENTUSE_URL=https://agentuse.shop.example.com   # 平台服务端访问 hub 的地址,内网地址也行
AGENTUSE_PLATFORM=shop
AGENTUSE_SECRET=<shop secret>
AGENTUSE_CALLBACK_SECRET=<shop callback_secret>   # 没拆就和 AGENTUSE_SECRET 一样
```

换 hub 实例只需要改这几个值,平台代码不动。

Go 平台用 SDK:

```go
import "github.com/memory-co/agentuse/pkg/sdk"

var hub = sdk.New(os.Getenv("AGENTUSE_URL"), os.Getenv("AGENTUSE_PLATFORM"), os.Getenv("AGENTUSE_SECRET"))

func init() { hub.CallbackSecret = os.Getenv("AGENTUSE_CALLBACK_SECRET") }
```

### 2.2 设置页:「接入我的 agent」

每个用户可以在设置页建自己的 agent。平台服务端要做两件事:

1. **在自己的用户表里建一个同名的应用用户**,比如 `alice-claude`。评论里 `@alice-claude` 提到的就是它,agent 的回复以它的名义发出。它不需要能登录,也不占席位。
2. **调 hub 建 agent**,把交回的命令**只展示给 alice**:

```go
created, err := hub.CreateAgent(ctx, sdk.NewAgent{
	Owner:       alice.ID,        // 主人
	OwnerName:   alice.Name,
	Handle:      "alice-claude",  // 小写 2–40 个 a-z 0-9 - _,首尾是字母或数字;平台内唯一
	AgentUserID: appUser.ID,      // 第 1 步建的应用用户
	// Wake: "members",           // 可选:让 wake_roles 里的人也能叫醒;默认只有主人
	// Actions / WorkspaceMode:   // 可选:比平台策略更严
})
var e *sdk.Error
if errors.As(err, &e) && e.Code == "handle_taken" {
	// 名字被占了,让用户换一个
}
show(created.Connect.Command)   // agentuse connect https://agentuse.shop.example.com ac_…
show(created.Connect.ExpiresAt) // 15 分钟后过期
```

页面上配一句提示:「先装好 Claude Code 和 agentuse(`go install github.com/memory-co/agentuse/cmd/agentuse@latest`),然后在你想让 agent 干活的仓库目录里运行这条命令,再运行 `agentuse daemon start`」。

设置页其余按钮对应的 SDK 方法:

| 按钮 | 调用 | 说明 |
|---|---|---|
| 列表 / 状态 | `hub.Agents(ctx, alice.ID)` | 每个 agent 带 `Connected()`、`Online`、`Device{Name, OS, Online, LastSeen}`,显示「在线 · alice-mbp」「离线」「未接入」 |
| 重新接入 / 换电脑 | `hub.ConnectCode(ctx, agentID)` | 新命令,旧的作废;在新电脑上跑就把 agent 搬过去 |
| 修改 | `hub.UpdateAgent(ctx, agentID, sdk.AgentPatch{…})` | 改 `Wake` / `Actions` / `WorkspaceMode` / `AgentUserID` |
| 删除 | `hub.DeleteAgent(ctx, agentID)` | 排着和在跑的运行判失败;平台自己的应用用户由平台处理 |
| 这台电脑丢了 | `hub.RevokeDevice(ctx, deviceID)` | 设备密钥立刻作废,绑在上面的 agent 变成未接入 |

`members` 会让别人用 alice 的电脑和订阅干活,只能由 alice 自己打开,平台不要替她默认打开。

非 Go 平台:这些接口签的是「方法 + 路径 + 请求体」,不是只签请求体,见 [apis/platform.md](apis/platform.md#签名)。用 shell 演示建一个 agent:

```bash
URI='/v1/platforms/shop/agents'
BODY='{"owner":"u_alice","owner_name":"alice","handle":"alice-claude","agent_user_id":"u_app_alice_claude"}'
TS=$(date +%s)
SIG="v1=$(printf '%s.POST %s\n%s' "$TS" "$URI" "$BODY" | openssl dgst -sha256 -hmac "$AGENTUSE_SECRET" -hex | awk '{print $NF}')"

curl -sS "$AGENTUSE_URL$URI" \
  -H 'Content-Type: application/json' \
  -H "X-AgentUse-Timestamp: $TS" \
  -H "X-AgentUse-Signature: $SIG" \
  -d "$BODY"
# 201 {"agent":{"id":"ag_…","handle":"alice-claude",…,"online":false},"connect":{"code":"ac_…","expires_at":"…","command":"agentuse connect https://agentuse.shop.example.com ac_…"}}
```

### 2.3 评论存库后 notify

每条评论**存库成功之后**都调一次 notify,不用自己判断有没有 @ agent,hub 会判断:它找出正文里所有 `@handle`,逐个在这个平台的 agent 里找,找不到的当作普通用户忽略。

**线程键**是平台定的字符串,同一个线程的评论必须用同一个键;同一个键、同一个 agent 就续接同一段会话。惯例是 `<类型>:<键>`:`issue:MUL-42`、`blog:/posts/tmux`、`topic:8812`。

**作者**的 `id` 要和建 agent 时的 `owner` / `agent_user_id` 是同一套用户 id:hub 靠它认出「这是 agent 的主人」和「这是某个 agent 自己发的评论」。**角色**(`owner` / `member` / `guest`)由平台根据自己的权限算出来,不要从评论内容里推断。

**context**(可选,推荐带):同一线程里最近的其它评论。agent 能看到前文,hub 会按策略把它们标成可信 / 不可信。

```go
func onCommentSaved(ctx context.Context, c Comment, recent []Comment) {
	res, err := hub.Notify(ctx, sdk.Comment{
		ID:      c.ID,
		Thread:  "issue:" + c.IssueKey,
		Author:  sdk.Author{ID: c.Author.ID, Name: c.Author.Name, Role: roleOf(c.Author)},
		Text:    c.Body,
		Context: toSDK(recent),
	})
	if err != nil {
		log.Printf("agentuse notify: %v", err) // 不影响评论本身,记日志即可
		return
	}
	for _, a := range res.Agents {
		switch {
		case a.Woken:
			runs.Remember(a.RunID, a.Handle)          // 回调时据此知道是哪个 agent 在回
			showThinkingPlaceholder(c, a.Handle)      // 可选:先放一个「正在思考」
			if !a.Online {
				showHint(c, "@"+a.Handle+" 的电脑目前离线,上线后会处理")
			}
		case a.Reason == "not_owner":
			showHint(c, "@"+a.Handle+" 只有主人能叫醒")
		}
	}
}
```

notify 只签请求体(和管理接口不同)。其它语言照 [apis/platform.md](apis/platform.md#签名) 自己签名:`HMAC-SHA256(secret, "<unix 秒>.<原始请求体>")`,hex 编码,前缀 `v1=`:

```bash
BODY='{"id":"c_1001","thread":"issue:MUL-42","author":{"id":"u_alice","name":"alice","role":"member"},"text":"@alice-claude 退款金额算错了"}'
TS=$(date +%s)
SIG="v1=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$AGENTUSE_SECRET" -hex | awk '{print $NF}')"

curl -sS "$AGENTUSE_URL/v1/platforms/shop/notify" \
  -H 'Content-Type: application/json' \
  -H "X-AgentUse-Timestamp: $TS" \
  -H "X-AgentUse-Signature: $SIG" \
  -d "$BODY"
# 202 {"woken":true,"reason":"woken","agents":[{"handle":"alice-claude","agent_id":"ag_…","woken":true,"reason":"woken","online":true,"session_id":"s_…","run_id":"r_…"}]}
```

签名覆盖的是**原样发出去的字节**:先序列化成字符串,签这个字符串,再原样发送,中间不要重新格式化。

### 2.4 实现回调

hub 会 POST 到 `callback_url`,带 hub 的签名。平台要做三件事:**验签、处理事件、回 2xx**。

回调事件带着 `agent`(handle)和 `agent_id`,平台按它找到对应的应用用户,以它的名义写评论:

```go
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
	switch ev.Type {
	case "reply":
		// 以 agent 应用用户的名义写一条评论;InReplyTo 是叫醒它的那条评论,可以挂成楼中楼
		author := appUserOf(ev.Agent)
		return comments.Create(ctx, ev.Thread, author, ev.Text, ev.InReplyTo)
	case "status":
		// 可选:博客这类没有状态的平台不用实现,策略里也别给 status
		return issues.SetStatus(ctx, ev.Thread, ev.Status) // 如 in_review
	case "activity":
		// 可选:把「正在运行测试」之类的进度显示出来,或直接忽略。
		// type=error 也可能是 hub 发的:电脑一直不在线,运行排队超时了
		return progress.Show(ctx, ev.Thread, ev.Activity.Type, ev.Activity.Body)
	}
	return nil
}))
```

非 Go 平台验签的写法(Node / TypeScript):

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

要点:

- 用 `callback_secret` 验(没拆就是 `secret`);
- 验**原始请求体**,不要先解析再序列化;
- 处理失败就回非 2xx,hub 会把失败交还给 agent,由它决定重试;
- 事件里的 `thread` 和 `run_id` 由 hub 担保,可以直接用来定位工单。

事件格式见 [apis/platform.md](apis/platform.md#post-callback_url)。

---

## 3. 接上用户电脑(用户)

### 3.1 准备

- 装好 [Claude Code](https://claude.com/claude-code) 并登录(`claude` 能在终端里直接跑)。推理用的是你自己的订阅。
- 装好 `git`,要让 agent 干活的仓库已经 clone 到本地。
- 装 agentuse,并确认它在 `PATH` 里。agent 在运行里要调 `agentuse reply`,找不到命令就交不了活:

```bash
go install github.com/memory-co/agentuse/cmd/agentuse@latest
which agentuse
```

### 3.2 在设置页建 agent,在仓库里跑那条命令

在平台设置页建好 agent(比如 `alice-claude`),复制页面上的命令,**到你想让 agent 干活的仓库目录里**运行:

```bash
cd ~/work/shop-api
agentuse connect https://agentuse.shop.example.com ac_7Hq2mX9pLwQ…
# ✓ 找到 claude
# ✓ 仓库 /Users/alice/work/shop-api(基准分支 main)
# ✓ 已接入 shop:agent @alice-claude → alice-mbp:/Users/alice/work/shop-api
# 下一步:运行 `agentuse daemon start`(已经在跑的话重启它),然后在评论里 @alice-claude 试试。
```

它做了三件事:

1. **检查**:`claude` 在不在 `PATH` 里;当前目录是不是在 git 仓库里(用仓库根目录作为 agent 的工作目录);基准分支存不存在(默认是仓库当前所在的分支)。任何一项不过就停下说明原因,**接入码不消耗**,改好再跑同一条命令即可。
2. **兑换接入码**:hub 把 `alice-claude` 绑到这台电脑和这个仓库。
3. **写 `~/.agentuse/daemon.json`**(权限 0600):这台电脑的设备 id 和设备密钥。

常用参数(可以写在命令后面):

| 参数 | 默认 | 说明 |
|---|---|---|
| `-dir <目录>` | `.` | 不在仓库目录里跑时指定仓库 |
| `-base-ref <分支>` | 仓库当前分支 | 每个线程的 worktree 从这个分支切 |
| `-name <名字>` | 主机名 | 设置页上显示的电脑名 |
| `-no-check` | | 跳过 `claude` 命令检查 |

同一台电脑再接自己的第二个 agent(比如另一个仓库):在那个仓库里跑它的命令就行,设备和密钥复用,daemon.json 不变。接另一个平台的 agent,daemon.json 会多一条。

### 3.3 起 daemon

```bash
agentuse daemon start
# agentuse daemon dv_9e41b07c2a3d → https://agentuse.shop.example.com [shop] (ws)
```

daemon 只往外连 hub,电脑在 NAT 或公司内网后面都没关系;断网后每 5 秒自动重连。**daemon 连上之后 agent 才算在线**,只跑 `agentuse connect` 不算。daemon 在跑的时候又接了新的平台,要重启 daemon 才会连上新的那条。

每个线程会在仓库里切出一个独立的 git worktree(放在 `~/.agentuse/worktrees/<平台>/<会话>/`,分支名像 `agent/alice-claude/MUL-42`),不会碰你正在用的工作目录。公司代理不支持 WebSocket 时,在 daemon.json 里加 `"transport": "poll"`。

想开机自启(还没有自动配置):macOS 写一个 launchd plist,`ProgramArguments` 填 `agentuse daemon start`;Linux 用 `systemctl --user` 跑同样的命令。要让 daemon 进程的 `PATH` 里能找到 `claude`、`git` 和 `agentuse`。

### 3.4 看和断开

```bash
agentuse agents
# https://agentuse.shop.example.com [shop] 设备 alice-mbp(dv_9e41b07c2a3d,在线)
#   @alice-claude  /Users/alice/work/shop-api  wake=owner

agentuse disconnect alice-claude
# ✓ @alice-claude 已从这台电脑解绑(https://agentuse.shop.example.com [shop])
```

解绑之后 agent 还在平台上,只是变成未接入;想再接,回设置页「重新接入」拿新命令。

---

## 4. 验证:走一遍 @alice-claude

1. **在平台上发一条评论**:用 alice 自己的账号,在工单 MUL-42 下写「@alice-claude 退款金额算错了」。
   - notify 返回 202、`woken: true`,`agents[0].online: true`:hub 收下了,电脑在线。
   - 返回 200:没唤醒,看 `agents[].reason`(见 §5)。
2. **看 hub 里的运行**(运维):

   ```bash
   export AGENTUSE_URL=https://agentuse.shop.example.com AGENTUSE_ADMIN_KEY=<admin_key>
   agentuse admin runs -limit 5
   ```

   `status` 从 `queued` 很快变成 `claimed`,`agent_id` 是 `alice-claude` 的 `ag_…`,`device_id` 是 alice 电脑的 `dv_…`。
3. **看用户电脑**:daemon 打出 `run r_…: @alice-claude in issue:MUL-42 (s_…, attempt 1)`,`~/.agentuse/worktrees/shop/` 下多了一个 worktree,分支是 `agent/alice-claude/MUL-42`。
4. **看平台**:agent 回复以 `alice-claude` 的名义出现在评论区;策略允许的话,工单状态变成 `in_review`。
5. **再 @ 一次**:同一线程里再 @alice-claude 会续接同一段 Claude 会话(`--resume`),复用同一个 worktree,agent 记得上次做了什么。同一线程里 @ 另一个人的 agent 是另一段独立的会话。

---

## 5. 排错

| 现象 | 看哪 | 常见原因 |
|---|---|---|
| 建 agent 401 `bad_signature` | 平台日志 | 管理接口签的是「方法 + 空格 + 路径(含查询串)+ 换行 + 请求体」,不是只签请求体;密钥不对;时钟差超过 5 分钟 |
| 建 agent 400 `invalid` / 409 `handle_taken` | 响应的 `message` | handle 不合法(大写会被转小写,空格、点号不行),缺 `owner` / `agent_user_id`;名字已被占用 |
| 接入命令里的地址不对 | hub.json | 没配 `public_url`,hub 用了平台调它时的内网 Host |
| `agentuse connect` 报「接入码不对、已经用过或已过期」(401 `bad_code`) | 设置页 | 码只能用一次、15 分钟有效;之后又点过「重新接入」,旧码作废了;agent 已被删。回设置页重新生成 |
| `agentuse connect` 报「没找到 claude 命令」/「不是 git 仓库」/「仓库里没有基准分支」 | 终端 | 按提示装好 Claude Code、换到仓库目录、用 `-base-ref` 指定存在的分支;码没消耗,修好再跑 |
| notify 401 `bad_signature` | 平台日志 | 密钥不对;签的不是原始请求体;机器时钟和 hub 差超过 5 分钟 |
| notify 404 `unknown_platform` | hub.json | 平台 id 没登记,或改完 hub.json 没重启 hub |
| notify 200,`reason: no_mention` | 评论正文、agent 列表 | `@` 后面的名字不是这个平台上任何 agent 的 handle;`a@alice-claude.dev` 这种不算 |
| notify 200,`agents[].reason: not_owner` | agent 的 `wake` | agent 是默认的 `wake=owner`,只有主人能叫醒;让主人在设置页改成 `members` |
| notify 200,`agents[].reason: role_denied` | 平台算的角色 | agent 是 `wake=members`,但作者角色不在平台策略的 `wake_roles` 里 |
| notify 200,`reason: self_comment` | — | 作者是某个 agent 的应用用户(agent 自己的回复),正常现象;如果是真人,检查平台传的作者 id 是不是填成了 `agent_user_id` |
| notify 202 但 `online: false`,运行一直 `queued` | `agentuse agents`、daemon 日志 | 电脑上没起 `agentuse daemon start`,或起了但在接入新平台之前起的(要重启);daemon 日志里 `401` 说明设备被吊销或 daemon.json 里的密钥不对,重新接入 |
| 运行 `queued`、电脑在线 | `agentuse admin runs` | 同一线程同一 agent 上一条还在跑(串行,正常);这台电脑上别的 agent 的运行在跑(一台设备一次一条) |
| 运行 `failed`,`error: queue_ttl: device offline` | 平台收到的 `error` 进度 | 电脑在 `queue_ttl_seconds`(默认 24 小时)内一直没上线;上线后重新 @ 一次 |
| 运行 `failed`,`error: agent deleted` | — | agent 在运行排队或进行中被删了 |
| daemon 日志 `connect: …` 反复出现 | 反向代理 | 代理没放行 WebSocket 升级;临时可改 `transport: poll` |
| 运行 `failed`,`error: lease expired` | daemon 日志 | daemon 跑到一半掉线超过 `lease_seconds`,重试 `max_attempts` 次都没成 |
| 运行 `failed`,`error: git worktree add: …` | 仓库 | 接入后仓库被挪走了,或基准分支被删了;在新位置重新接入 |
| 运行 `succeeded` 但平台没收到回复 | hub 日志、平台回调日志 | agent 没调 `agentuse reply`(多半是 `PATH` 里没有 agentuse);回调验签失败(平台用错了 `callback_secret`);回调地址不通 |
| agent 调 `agentuse status` 得到 403 | 策略、agent 设置 | 生效的 `actions` 里没有 `status`(agent 设置与平台策略取交集),这是策略在起作用 |

---

## 6. 上线前检查

- [ ] 每把密钥都是独立的随机值,没有进代码仓库;`token_key`、`admin_key` 只在 hub 上。
- [ ] hub 只监听 `127.0.0.1`,对外只有带 TLS 的反向代理;`public_url` 是用户电脑能访问的 https 地址。
- [ ] 公开平台的 `wake_roles` 只有 `owner` / 维护者,`workspace_mode` 是 `readonly`,`actions` 只给 `reply`。
- [ ] 平台拆了 `callback_secret`。
- [ ] 平台只在评论**存库之后** notify,作者 id 和建 agent 用的是同一套用户 id,角色由平台权限算出,不取自评论内容。
- [ ] 接入命令只展示给 agent 的主人;设置页提供「吊销这台电脑」。
- [ ] 知道公开平台的风险:「只读」目前靠 Claude Code 的权限模式保证,容器隔离还在 [roadmap](designs/roadmap.md) 上。陌生人能看到 agent 回复的平台,不要让 agent 接触带凭据的仓库。
