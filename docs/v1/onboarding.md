# 接入指南:从零到第一个 @claude

这篇按顺序走完一次完整的接入:**部署 hub → 接入平台 → 接上用户电脑 → 验证**。读完之后,平台评论区里的一个 `@claude` 就能叫醒你电脑上的 Claude Code,在你的仓库里干活,再把回复写回评论区。

机制和取舍见 [designs/](designs/README.md),字段见 [structs/](structs/README.md),接口见 [apis/](apis/README.md)。这里只讲怎么做。

---

## 0. 三个角色,先分清谁做什么

| 角色 | 是谁 | 要做的事 | 章节 |
|---|---|---|---|
| **实例主人** | 部署 hub 的人或团队(个人博客就是站长自己) | 起 hub、登记平台和 daemon、发密钥 | [§1](#1-部署-hub实例主人) |
| **平台开发者** | 工单系统 / 博客 / 论坛的开发者 | 评论存库后 notify、实现回调、建 agent 应用用户 | [§2](#2-接入平台平台开发者) |
| **用户** | 让 agent 在自己电脑上干活的人 | 装 Claude Code 和 agentuse、起 daemon、配仓库 | [§3](#3-接上用户电脑用户) |

一个人可以同时是三个角色(比如给自己博客接 agent 的站长),也可以分别是三拨人(公司工单系统:平台组接入、运维组部署 hub、每个工程师在自己电脑上起 daemon)。

三方之间只交换三类密钥,各自只拿自己那份:

```
            secret / callback_secret           daemon key
  平台  ◀───────────────────────────▶  hub  ◀────────────────▶  daemon(用户电脑)
                                         │
                       token_key、admin_key 只在 hub 自己手里
```

- **平台密钥**(`secret`,可选再加一把 `callback_secret`):实例主人发给平台开发者。
- **daemon 密钥**:实例主人为每台用户电脑发一把。
- **token_key / admin_key**:只留在 hub,谁也不给。

整个过程中,平台不接触用户电脑,用户电脑也不需要能被访问。

---

## 1. 部署 hub(实例主人)

### 1.1 准备

- 一台平台和用户电脑都能访问到的机器(云主机、家里的 NAS,或者本地开发时就是你自己的电脑);
- Go 1.24+(用来装 agentuse);
- 对外提供服务时要一个域名和 TLS,比如 `hub.alice.dev`。

### 1.2 安装

```bash
go install github.com/memory-co/agentuse/client/cmd/agentuse@latest
agentuse version
```

### 1.3 生成密钥

每把密钥都是一段随机串,用不同的值:

```bash
openssl rand -hex 32   # token_key:签运行令牌
openssl rand -hex 32   # admin_key:管理接口
openssl rand -hex 32   # 平台 shop 的 secret
openssl rand -hex 32   # 平台 shop 的 callback_secret(可选,推荐)
openssl rand -hex 32   # daemon alice-mbp 的 key
```

### 1.4 写 hub.json

放在 `~/.agentuse/hub.json`(`AGENTUSE_HOME` 可以改目录)。先只登记 hub 自己的配置,平台和 daemon 在 §2、§3 里加:

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

| 字段 | 说明 |
|---|---|
| `listen` | 只监听本机,对外由反向代理加 TLS |
| `store` | `sqlite` 会把会话和运行存进 `~/.agentuse/hub.db`,重启不丢 |
| `lease_seconds` / `max_attempts` | daemon 掉线后多久把运行收回重排、最多重试几次;默认值一般不用改 |

完整字段见 [structs/filesystem.md](structs/filesystem.md#hubjson)。

### 1.5 加 TLS,起服务

用任意反向代理把 `https://hub.alice.dev` 转到 `127.0.0.1:7700`。它要能转发 WebSocket(daemon 用 `/v1/daemon/connect` 长连)。Caddy 默认就行:

```
hub.alice.dev {
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
    proxy_read_timeout 1h;          # 推送连接会空闲很久,别被代理掐掉
}
```

起服务:

```bash
agentuse hub start
# agentuse hub listening on 127.0.0.1:7700 (0 platforms, store sqlite)

curl https://hub.alice.dev/v1/health
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

**改 hub.json 之后要重启 hub**,配置只在启动时读一次。

---

## 2. 接入平台(平台开发者)

以一个工单系统 `shop` 为例。博客、论坛的接法一样,区别只在策略(§2.2)。

### 2.1 跟实例主人对齐四件事

| 事 | 例子 | 谁定 |
|---|---|---|
| 平台 id | `shop` | 双方约定,出现在 notify 的地址里 |
| 回调地址 | `https://shop.example.com/agentuse/callback` | 平台 |
| agent 应用用户 | 名字 `claude`,id `u_claude` | 平台(§2.3) |
| 策略 | 谁能唤醒、agent 能做什么 | 双方商量(§2.2) |

实例主人把 `secret`(和 `callback_secret`)交给平台开发者。

### 2.2 实例主人登记平台

在 hub.json 的 `platforms` 里加一项,然后重启 hub:

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

策略按平台的性质挑一行改,理由见 [designs/wake-and-trust.md](designs/wake-and-trust.md):

| 平台 | `wake_roles` | `trusted_roles` | `actions` | `workspace_mode` |
|---|---|---|---|---|
| 公司内工单系统 | `member` | `member` | `reply` `status` | `readwrite` |
| 个人公开博客 | `owner` | `owner` | `reply` | `readonly` |
| 开源论坛 | `member`(维护者) | `member` | `reply` `label` | `readonly` |

**公开平台一定只让 `owner` / 维护者唤醒**。陌生访客的评论只会作为「不可信材料」交给 agent,不会被当成指令。

### 2.3 在平台里建 agent 应用用户

在用户表里加一个代表 agent 的用户,比如名字 `claude`、id `u_claude`:

- 评论里 `@claude` 提到的就是它,名字要和策略的 `agent_name` 一致;
- agent 的回复以它的名义发出;
- 它的 id 填进策略的 `agent_user_id`,hub 据此忽略 agent 自己的评论,防止它叫醒自己。

它不需要能登录,也不占席位。

### 2.4 给平台配置地址和密钥

平台从配置或设置页读三个值:

```bash
AGENTUSE_URL=https://hub.alice.dev
AGENTUSE_PLATFORM=shop
AGENTUSE_SECRET=<shop secret>
AGENTUSE_CALLBACK_SECRET=<shop callback_secret>   # 没拆就和 AGENTUSE_SECRET 一样
```

换 hub 实例只需要改这几个值,平台代码不动。

### 2.5 评论存库后 notify

每条评论**存库成功之后**都调一次 notify,不用自己判断有没有 @ agent,hub 会判断。

**线程键**是平台定的字符串,同一个线程的评论必须用同一个键,同一个键就续接同一段 agent 会话。惯例是 `<类型>:<键>`:`issue:MUL-42`、`blog:/posts/tmux`、`topic:8812`。

**作者角色**(`owner` / `member` / `guest`)由平台根据自己的权限算出来。这是 hub 判断能不能唤醒的唯一依据,不要从评论内容里推断。

**context**(可选,推荐带):同一线程里最近的其它评论。agent 能看到前文,hub 会按策略把它们标成可信 / 不可信。

Go 平台用 SDK:

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
		log.Printf("agentuse notify: %v", err) // 不影响评论本身,记日志即可
		return
	}
	if res.Woken {
		showThinkingPlaceholder(c.IssueKey) // 可选:先放一个「正在思考」
	}
}
```

其它语言照 [apis/platform.md](apis/platform.md#签名) 自己签名:`HMAC-SHA256(secret, "<unix 秒>.<原始请求体>")`,hex 编码,前缀 `v1=`。用 shell 演示一遍:

```bash
BODY='{"id":"c_1001","thread":"issue:MUL-42","author":{"id":"u_bob","name":"bob","role":"member"},"text":"@claude 退款金额算错了"}'
TS=$(date +%s)
SIG="v1=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$AGENTUSE_SECRET" -hex | awk '{print $NF}')"

curl -sS "$AGENTUSE_URL/v1/platforms/shop/notify" \
  -H 'Content-Type: application/json' \
  -H "X-AgentUse-Timestamp: $TS" \
  -H "X-AgentUse-Signature: $SIG" \
  -d "$BODY"
# 202 {"woken":true,"reason":"woken","session_id":"s_…","run_id":"r_…"}
```

签名覆盖的是**原样发出去的字节**:先序列化成字符串,签这个字符串,再原样发送,中间不要重新格式化。

### 2.6 实现回调

hub 会 POST 到 `callback_url`,带 hub 的签名。平台要做三件事:**验签、处理事件、回 2xx**。

```go
http.Handle("/agentuse/callback", hub.Handler(func(ctx context.Context, ev sdk.Event) error {
	switch ev.Type {
	case "reply":
		// 以 agent 应用用户的名义写一条评论;InReplyTo 是叫醒它的那条评论,可以挂成楼中楼
		return comments.Create(ctx, ev.Thread, "u_claude", ev.Text, ev.InReplyTo)
	case "status":
		// 可选:博客这类没有状态的平台不用实现,策略里也别给 status
		return issues.SetStatus(ctx, ev.Thread, ev.Status) // 如 in_review
	case "activity":
		// 可选:把「正在运行测试」之类的进度显示出来,或直接忽略
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
- 装好 `git`。
- 装 agentuse,并确认它在 `PATH` 里。agent 在运行里要调 `agentuse reply`,找不到命令就交不了活:

```bash
go install github.com/memory-co/agentuse/client/cmd/agentuse@latest
which agentuse
```

### 3.2 跟实例主人要一把 daemon 密钥

给这台电脑起个 id,比如 `alice-mbp`。实例主人把它登记进 hub.json 并重启 hub:

```json
"daemon_keys": {
  "alice-mbp": "<alice-mbp key>"
}
```

一台电脑一个 id。要在同一台电脑上并行跑多条运行,就多登记几个 id、多起几个 daemon。

### 3.3 写 daemon.json

放在用户电脑的 `~/.agentuse/daemon.json`:

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

| 字段 | 说明 |
|---|---|
| `workspaces` | 平台 id → 在哪个仓库干活。每个线程会从 `base_ref` 切出一个独立的 git worktree,分支名像 `agent/claude/MUL-42`,不会碰你正在用的工作目录 |
| `blog: {}` | 没有代码可改的平台(博客问答)给一个空目录就行 |
| `transport` | `ws`(默认):评论一来马上开工。公司代理不支持 WebSocket 时改成 `poll` |

`repo` 必须是一个已经 clone 好、有 `base_ref` 分支的本地仓库。worktree 放在 `~/.agentuse/worktrees/<平台>/<会话>/`。

### 3.4 起 daemon

```bash
agentuse daemon start
# agentuse daemon alice-mbp → https://hub.alice.dev (ws)
```

daemon 只往外连 hub,电脑在 NAT 或公司内网后面都没关系。断网后它每 5 秒自动重连。

想开机自启:macOS 写一个 launchd plist,`ProgramArguments` 填 `agentuse daemon start`;Linux 用 `systemctl --user` 跑同样的命令。要让 daemon 进程的 `PATH` 里能找到 `claude`、`git` 和 `agentuse`。

---

## 4. 验证:走一遍 @claude

1. **在平台上发一条评论**:用一个 `wake_roles` 里的账号,在工单 MUL-42 下写「@claude 退款金额算错了」。
   - notify 返回 202、`woken: true`:hub 收下了。
   - 返回 200:没唤醒,看 `reason`(见 §5)。
2. **看 hub 里的运行**(实例主人):

   ```bash
   export AGENTUSE_URL=https://hub.alice.dev AGENTUSE_ADMIN_KEY=<admin_key>
   agentuse admin runs -limit 5
   ```

   `status` 从 `queued` 很快变成 `claimed`,`daemon_id` 是 `alice-mbp`。
3. **看用户电脑**:daemon 打出 `run r_…: issue:MUL-42 (s_…, attempt 1)`,`~/work/shop-api` 旁边多了一个 worktree,分支是 `agent/claude/MUL-42`。
4. **看平台**:agent 回复以 `claude` 的名义出现在评论区;策略允许的话,工单状态变成 `in_review`。
5. **再 @ 一次**:同一线程的下一条评论会续接同一段 Claude 会话(`--resume`),复用同一个 worktree,agent 记得上次做了什么。

---

## 5. 排错

| 现象 | 看哪 | 常见原因 |
|---|---|---|
| notify 401 `bad_signature` | 平台日志 | 密钥不对;签的不是原始请求体;机器时钟和 hub 差超过 5 分钟 |
| notify 404 `unknown_platform` | hub.json | 平台 id 没登记,或改完 hub.json 没重启 hub |
| notify 200,`reason: no_mention` | 评论正文 | `@` 后面的名字和 `agent_name` 不一致;`@claudette`、`a@claude.dev` 这种不算 |
| notify 200,`reason: role_denied` | 平台算的角色 | 作者角色不在 `wake_roles` 里 |
| notify 200,`reason: self_comment` | — | agent 自己的评论,正常现象 |
| 运行一直 `queued` | `agentuse admin runs`、daemon 日志 | daemon 没起或连不上;daemon 日志里 `401` 说明 id / key 和 hub.json 对不上;同一线程上一条还在跑(同线程串行,正常) |
| daemon 日志 `connect: …` 反复出现 | 反向代理 | 代理没放行 WebSocket 升级;临时可改 `transport: poll` |
| 运行 `failed`,`error: lease expired` | daemon 日志 | daemon 跑到一半掉线超过 `lease_seconds`,重试 `max_attempts` 次都没成 |
| 运行 `failed`,`error: git worktree add: …` | daemon.json | `repo` 路径不对,或没有 `base_ref` 分支 |
| 运行 `succeeded` 但平台没收到回复 | hub 日志、平台回调日志 | agent 没调 `agentuse reply`(多半是 `PATH` 里没有 agentuse);回调验签失败(平台用错了 `callback_secret`);回调地址不通 |
| agent 调 `agentuse status` 得到 403 | 策略 | `actions` 里没有 `status`,这是策略在起作用 |

---

## 6. 上线前检查

- [ ] 每把密钥都是独立的随机值,没有进代码仓库;`token_key`、`admin_key` 只在 hub 上。
- [ ] hub 只监听 `127.0.0.1`,对外只有带 TLS 的反向代理。
- [ ] 公开平台的 `wake_roles` 只有 `owner` / 维护者,`workspace_mode` 是 `readonly`,`actions` 只给 `reply`。
- [ ] 平台拆了 `callback_secret`。
- [ ] 平台只在评论**存库之后** notify,并且角色由平台权限算出,不取自评论内容。
- [ ] daemon 每台电脑一个 id;电脑不用了就从 `daemon_keys` 里删掉并重启 hub。
- [ ] 知道公开平台的风险:「只读」目前靠 Claude Code 的权限模式保证,容器隔离还在 [roadmap](designs/roadmap.md) 上。陌生人能看到 agent 回复的平台,不要让 agent 接触带凭据的仓库。
