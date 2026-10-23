# 一条命令接入

> **状态:设计稿,未实施。** 实施计划见 [roadmap.md](roadmap.md)。

目标:平台用户在设置页点一下,复制一条命令到自己电脑的终端里跑完,就能在评论里 @ 自己的 agent。不改配置文件,不找运维要密钥,不重启任何东西。参照 Multica:装一个 CLI、登录一次,daemon 就在后台跑起来。

## 1. 用户看到的

在平台的「我的 Agent」设置页:

1. 填一个名字(默认 `<用户名>-claude`),点「创建」。
2. 页面出现一条命令和一句提示「在你想让 agent 干活的仓库目录里运行」:

   ```bash
   cd ~/work/shop-api
   curl -fsSL https://agentuse.shop.example.com/install.sh | sh -s -- ac_7Hq2mX9pLw
   ```

3. 终端里:

   ```
   ✓ 已安装 agentuse 0.3.0 到 ~/.agentuse/bin
   ✓ 找到 claude(已登录)
   ✓ 已接入 shop:agent @alice-claude → alice-mbp:~/work/shop-api
   ✓ 已设为开机自启(launchd)
   在任意工单评论里 @alice-claude 试试。
   ```

4. 设置页上 agent 的状态变成「在线 · alice-mbp」。

## 2. 背后发生的

```
平台设置页                 平台服务端                         hub                          用户电脑
   │ 点「创建」               │                                 │                               │
   │────────────────────────▶│ 在自己用户表里建应用用户          │                               │
   │                         │  alice-claude                    │                               │
   │                         │ POST /agents(平台签名)────────▶│ 建 User、Agent、接入码 ac_…   │
   │                         │◀──────────── agent + 安装命令 ────│                               │
   │◀──── 展示安装命令 ───────│                                  │                               │
   │                                                            │                               │
   │                                        用户复制命令到终端 ──┼──────────────────────────────▶│
   │                                                            │◀── GET /install.sh ───────────│
   │                                                            │◀── GET /download/<os>/<arch> ─│ 装二进制
   │                                                            │◀── POST /v1/connect ──────────│ 交接入码
   │                                                            │  校验接入码(一次性,15 分钟)  │
   │                                                            │  建 Device,绑到 Agent        │
   │                                                            │─── 设备 id + 设备密钥 ────────▶│ 写 daemon.json
   │                                                            │                               │ 装开机自启
   │                                                            │◀══ WebSocket(设备密钥)═══════│ daemon 上线
   │  刷新:在线 · alice-mbp ◀─────────── GET /agents ──────────│                               │
```

### 2.1 接入码

| 性质 | 取值 | 为什么 |
|---|---|---|
| 一次性 | 兑换后立刻作废 | 命令被别人看到也用不了第二次 |
| 时效 | 15 分钟 | 截图、聊天记录里泄露的风险小 |
| 绑定 | 一个 agent | 兑换只能把**这个** agent 接到**跑命令的这台电脑** |
| 形态 | `ac_` + 128 位随机,hub 只存 sha256 | 数据库泄露也拿不到可用的码 |

只有平台服务端能申请接入码,而平台只把它展示给 agent 的主人看。所以「谁能把电脑接成 alice-claude」最终由平台的登录态决定,hub 不需要自己的账号体系。

### 2.2 install.sh

hub 自己提供 `GET /install.sh`,内容固定、可读、不带任何秘密:

1. 判断系统和架构(macOS / Linux,amd64 / arm64);
2. 从 `GET /download/<os>/<arch>` 下载对应的 agentuse 二进制,校验 sha256,放到 `~/.agentuse/bin/agentuse`,并把这个目录加进 shell 的 `PATH`(agent 运行时要调 `agentuse reply`);
3. 执行 `agentuse connect <hub 地址> <接入码> --dir "$PWD"`。

`/download` 由 hub 从 GitHub Release 转发或重定向,也可以由运维指向内网镜像。已经装过的电脑,下载这一步跳过,只接入。

不想 `curl | sh` 的用户,可以自己装好二进制,直接跑第 3 步那条命令,效果一样。

### 2.3 agentuse connect

```bash
agentuse connect https://agentuse.shop.example.com ac_7Hq2mX9pLw --dir ~/work/shop-api
```

1. **检查前提**:`claude` 在不在、登没登录;`--dir` 是不是 git 仓库、有没有基准分支。不满足就停下,说清楚缺什么,接入码不消耗。
2. **兑换**:`POST /v1/connect {code, device_name, os, dir, base_ref}`。hub 校验接入码、建 Device(或复用这台电脑已有的设备)、把 agent 绑到这台设备和目录,交回设备 id 和设备密钥。
3. **写配置**:`~/.agentuse/daemon.json` 里加上这个 hub 和设备密钥(权限 0600)。同一台电脑接多个 agent、多个平台的 hub 时,都追加进这一个文件。
4. **起 daemon**:macOS 写 launchd 的 `~/Library/LaunchAgents/dev.agentuse.daemon.plist`,Linux 写 `systemctl --user` 的 unit,然后启动。已经在跑就让它重读配置。
5. **自检**:等 daemon 连上 hub,打印「已接入」。

同一台电脑第二次接入(第二个 agent、另一个平台),设备密钥和 daemon 都复用,只是多绑一个 agent。

### 2.4 断开与吊销

| 场景 | 怎么做 |
|---|---|
| 不想用了 | 平台设置页删 agent;或在电脑上 `agentuse disconnect alice-claude` |
| 换电脑 | 设置页「重新接入」拿新命令,在新电脑上跑;agent 搬到新设备,旧设备上的绑定失效 |
| 电脑丢了 | 设置页吊销设备;这台设备的密钥立刻作废,绑在上面的 agent 变成未接入 |
| 卸载 | `agentuse uninstall`:停 daemon、删开机自启、删 `~/.agentuse` |

## 3. 运维那一侧也要简单

既然 hub 是平台自己的实例,起它也应该像起 Redis:

```bash
docker run -d -p 7700:7700 -v agentuse:/data \
  -e AGENTUSE_PLATFORM_ID=shop \
  -e AGENTUSE_PLATFORM_SECRET=… \
  -e AGENTUSE_CALLBACK_URL=https://shop.example.com/agentuse/callback \
  -e AGENTUSE_PUBLIC_URL=https://agentuse.shop.example.com \
  ghcr.io/memory-co/agentuse hub
```

- 单平台用环境变量就够,不用写 hub.json;多平台或细调策略时再写 hub.json,两者都支持。
- `token_key` 不配就在第一次启动时自动生成并存进数据目录。
- `AGENTUSE_PUBLIC_URL` 是用户电脑访问 hub 的地址,安装命令里用的就是它。
- 不再有 `daemon_keys`:设备都是通过接入码自己登记的。

## 4. 安全

- **接入码**只经过平台服务端和平台的已登录页面,只给 agent 的主人看。
- **设备密钥**只在兑换时交给 daemon 一次,存在用户电脑 `0600` 的文件里;平台永远看不到它,hub 只存哈希。
- **install.sh** 只从 hub 自己的 TLS 地址下载,二进制校验 sha256;脚本内容公开、固定,用户可以先 `curl` 下来看。
- **一台电脑被别人拿到**:吊销设备即可,不影响同一个人的其它设备和其它人。
- agent 在电脑上的权限边界不变,见 [wake-and-trust.md](wake-and-trust.md)。
