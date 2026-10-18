# 一条命令接入

> **状态:部分实施。** 已经有:接入码(发码、一次性兑换 `POST /v1/connect`)、`agentuse connect` / `agentuse agents` / `agentuse disconnect`、设备密钥认证、hub.json 的 `public_url`。还没有:`install.sh` 与 `/download`、开机自启(launchd / systemd)、接入后等 daemon 上线的自检、环境变量配置 hub、`token_key` 自动生成、Docker 镜像、`agentuse uninstall`。下文标了「(未实施)」的就是这些,计划见 [roadmap.md](roadmap.md)。
>
> 所以现在用户要自己先装好 `agentuse`(`go install`),平台给出的命令是 `agentuse connect <hub 地址> <接入码>`,接入后自己跑 `agentuse daemon start`。一步步的做法见 [onboarding.md](../onboarding.md)。

目标:平台用户在设置页点一下,复制一条命令到自己电脑的终端里跑完,就能在评论里 @ 自己的 agent。不改配置文件,不找运维要密钥,不重启任何东西。参照 Multica:装一个 CLI、登录一次,daemon 就在后台跑起来。

## 1. 用户看到的

在平台的「我的 Agent」设置页:

1. 填一个名字(默认 `<用户名>-claude`),点「创建」。
2. 页面出现一条命令和一句提示「在你想让 agent 干活的仓库目录里运行」:

   ```bash
   cd ~/work/shop-api
   curl -fsSL https://agentuse.shop.example.com/install.sh | sh -s -- ac_7Hq2mX9pLw   # 目标形态(未实施)
   agentuse connect https://agentuse.shop.example.com ac_7Hq2mX9pLw                  # 现在 hub 交回的命令
   ```

3. 终端里(目标形态):

   ```
   ✓ 已安装 agentuse 0.3.0 到 ~/.agentuse/bin
   ✓ 找到 claude(已登录)
   ✓ 已接入 shop:agent @alice-claude → alice-mbp:~/work/shop-api
   ✓ 已设为开机自启(launchd)
   在任意工单评论里 @alice-claude 试试。
   ```

   现在 `agentuse connect` 打出的是:

   ```
   ✓ 找到 claude
   ✓ 仓库 /Users/alice/work/shop-api(基准分支 main)
   ✓ 已接入 shop:agent @alice-claude → alice-mbp:/Users/alice/work/shop-api
   下一步:运行 `agentuse daemon start`(已经在跑的话重启它),然后在评论里 @alice-claude 试试。
   ```

4. daemon 连上 hub 之后,设置页上 agent 的状态变成「在线 · alice-mbp」(只兑换接入码不算在线)。

## 2. 背后发生的

```
平台设置页                 平台服务端                         hub                          用户电脑
   │ 点「创建」               │                                 │                               │
   │────────────────────────▶│ 在自己用户表里建应用用户          │                               │
   │                         │  alice-claude                    │                               │
   │                         │ POST /agents(平台签名)────────▶│ 建 User、Agent、接入码 ac_…   │
   │                         │◀──────────── agent + 接入命令 ────│                               │
   │◀──── 展示接入命令 ───────│                                  │                               │
   │                                                            │                               │
   │                                        用户复制命令到终端 ──┼──────────────────────────────▶│
   │                                                            │◀── GET /install.sh ───────────│(未实施)
   │                                                            │◀── GET /download/<os>/<arch> ─│ 装二进制(未实施)
   │                                                            │◀── POST /v1/connect ──────────│ 交接入码 + 已有设备身份
   │                                                            │  校验接入码(一次性,15 分钟)  │
   │                                                            │  建或复用 Device,绑到 Agent  │
   │                                                            │─── 设备 id (+ 新设备的密钥) ──▶│ 写 daemon.json
   │                                                            │                               │ 装开机自启(未实施,
   │                                                            │                               │  现在手动 daemon start)
   │                                                            │◀══ WebSocket(设备密钥)═══════│ daemon 上线
   │  刷新:在线 · alice-mbp ◀─────────── GET /agents ──────────│                               │
```

### 2.1 接入码

| 性质 | 取值 | 为什么 |
|---|---|---|
| 一次性 | 兑换后立刻作废 | 命令被别人看到也用不了第二次 |
| 时效 | 15 分钟 | 截图、聊天记录里泄露的风险小 |
| 绑定 | 一个 agent | 兑换只能把**这个** agent 接到**跑命令的这台电脑** |
| 唯一 | 每个 agent 同时只有一条有效的码 | 给同一个 agent 发新码,旧码立刻作废 |
| 形态 | `ac_` + 128 位随机,hub 只存 sha256 | 数据库泄露也拿不到可用的码 |

只有平台服务端能申请接入码,而平台只把它展示给 agent 的主人看。所以「谁能把电脑接成 alice-claude」最终由平台的登录态决定,hub 不需要自己的账号体系。

### 2.2 install.sh(未实施)

hub 自己提供 `GET /install.sh`,内容固定、可读、不带任何秘密:

1. 判断系统和架构(macOS / Linux,amd64 / arm64);
2. 从 `GET /download/<os>/<arch>` 下载对应的 agentuse 二进制,校验 sha256,放到 `~/.agentuse/bin/agentuse`,并把这个目录加进 shell 的 `PATH`(agent 运行时要调 `agentuse reply`);
3. 执行 `agentuse connect <hub 地址> <接入码> --dir "$PWD"`。

`/download` 由 hub 从 GitHub Release 转发或重定向,也可以由运维指向内网镜像。已经装过的电脑,下载这一步跳过,只接入。

不想 `curl | sh` 的用户,可以自己装好二进制,直接跑第 3 步那条命令,效果一样。

### 2.3 agentuse connect

```bash
agentuse connect https://agentuse.shop.example.com ac_7Hq2mX9pLw -dir ~/work/shop-api
```

1. **检查前提**:`claude` 在不在 `PATH` 里(`-no-check` 可跳过;登没登录还不查);`-dir`(默认当前目录)是不是在 git 仓库里,是的话取仓库根作为工作目录;基准分支(`-base-ref`,默认仓库当前分支)存不存在。不满足就停下,说清楚缺什么,接入码不消耗。
2. **兑换**:`POST /v1/connect {code, device_name, os, dir, base_ref, devices}`,`devices` 是 daemon.json 里这台电脑在这个 hub 上已有的设备身份 `[{id, key}]`。hub 先认这些设备,再一次性取走接入码;其中有**同平台、同主人**的设备就复用,否则新建一台;把 agent 绑到这台设备和目录(原来绑在别处的就搬过来)。交回 `{platform, agent, device_id, device_key}`,`device_key` 只在新建设备时有。
3. **写配置**:新建了设备时,`~/.agentuse/daemon.json` 的 `hubs` 里加一条 `{url, platform, device_id, device_key}`(权限 0600,按 `device_id` 去重);复用设备时文件不动。同一台电脑接多个平台、多个人的 agent,都在这一个文件里。
4. **起 daemon**(未实施):macOS 写 launchd 的 `~/Library/LaunchAgents/dev.agentuse.daemon.plist`,Linux 写 `systemctl --user` 的 unit,然后启动。已经在跑就让它重读配置。**现在**是提示用户自己跑 `agentuse daemon start`,已经在跑的要重启它。
5. **自检**(未实施):等 daemon 连上 hub,打印「已接入」。

同一台电脑给同一个人在同一个平台上接第二个 agent,设备和密钥复用,只是多绑一个 agent;接另一个平台(或另一个人的 agent)是另一台设备、另一把密钥,daemon.json 多一条,`agentuse daemon start` 每条各跑一个循环。

### 2.4 断开与吊销

| 场景 | 怎么做 |
|---|---|
| 这台电脑不跑这个 agent 了 | 在电脑上 `agentuse disconnect alice-claude`:agent 从这台设备解绑,变成未接入,还留在平台上,可以再用新接入码接到别处 |
| 不想用了 | 平台设置页删 agent:排着和在跑的运行判失败,会话留作记录 |
| 换电脑 | 设置页「重新接入」拿新命令,在新电脑上跑;agent 搬到新设备,旧设备上的绑定失效 |
| 电脑丢了 | 设置页吊销设备;这台设备的密钥立刻作废,绑在上面的 agent 变成未接入 |
| 卸载(未实施) | `agentuse uninstall`:停 daemon、删开机自启、删 `~/.agentuse` |

在电脑上看接了哪些 agent:`agentuse agents`。

## 3. 运维那一侧也要简单

既然 hub 是平台自己的实例,起它也应该像起 Redis(Docker 镜像和环境变量配置**未实施**,现在仍写 hub.json,见 [onboarding.md](../onboarding.md)):

```bash
docker run -d -p 7700:7700 -v agentuse:/data \
  -e AGENTUSE_PLATFORM_ID=shop \
  -e AGENTUSE_PLATFORM_SECRET=… \
  -e AGENTUSE_CALLBACK_URL=https://shop.example.com/agentuse/callback \
  -e AGENTUSE_PUBLIC_URL=https://agentuse.shop.example.com \
  ghcr.io/memory-co/agentuse hub
```

- 单平台用环境变量就够,不用写 hub.json;多平台或细调策略时再写 hub.json,两者都支持。(未实施)
- `token_key` 不配就在第一次启动时自动生成并存进数据目录。(未实施)
- `AGENTUSE_PUBLIC_URL` 是用户电脑访问 hub 的地址,接入命令里用的就是它。现在对应 hub.json 的 `public_url`(已实施;不配就按平台那次请求的 Host 推断)。
- 不再有 `daemon_keys`:设备都是通过接入码自己登记的。(已实施)

## 4. 安全

- **接入码**只经过平台服务端和平台的已登录页面,只给 agent 的主人看。
- **设备密钥**只在兑换时交给 daemon 一次,存在用户电脑 `0600` 的文件里;平台永远看不到它,hub 只存哈希。
- **install.sh**(未实施)只从 hub 自己的 TLS 地址下载,二进制校验 sha256;脚本内容公开、固定,用户可以先 `curl` 下来看。
- **一台电脑被别人拿到**:吊销设备即可,不影响同一个人的其它设备和其它人。
- agent 在电脑上的权限边界不变,见 [wake-and-trust.md](wake-and-trust.md)。
