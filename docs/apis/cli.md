# CLI

一个二进制 `agentuse`,三类使用者。

## 实例主人

```bash
agentuse hub start [-config ~/.agentuse/hub.json]
```

按 [hub.json](../structs/filesystem.md#hubjson) 起 hub(默认 SQLite 存在 `~/.agentuse/hub.db`),同时每 10 秒回收一次租约过期的运行、把排队超过 `queue_ttl_seconds` 的运行判失败(`queue_ttl: device offline`,并回调平台一条 `error` 进度)。Ctrl-C 优雅退出。

```bash
agentuse admin sessions [-platform shop] [-limit 20]
agentuse admin runs     [-platform shop] [-session s_…] [-agent ag_…] [-status claimed] [-limit 20]
agentuse admin run <run id>
```

只读查看会话和运行,输出 JSON。读 `AGENTUSE_URL` / `AGENTUSE_ADMIN_KEY`,或用 `-hub` / `-key`。见 [admin.md](admin.md)。

## 用户电脑

```bash
agentuse connect <hub 地址> <接入码> [-dir .] [-base-ref <当前分支>] [-name <主机名>] [-config ~/.agentuse/daemon.json] [-no-check]
```

把这台电脑接成你的 agent。命令由平台设置页给出(`agentuse connect https://agentuse.shop.example.com ac_…`),在你想让 agent 干活的仓库目录里跑。参数和位置参数可以混排。

1. **检查前提**:`claude` 在 `PATH` 里(`-no-check` 跳过);`-dir` 在一个 git 仓库里(取仓库根目录作为 agent 的工作目录);基准分支存在(默认是仓库当前分支,detached HEAD 时是 `main`)。任何一项不满足就停,**接入码不消耗**。
2. **兑换**:`POST /v1/connect`([daemon.md](daemon.md#post-v1connect)),把 daemon.json 里这个 hub 已有的设备身份一并带上,同一个主人就复用那台设备。接入码不对、用过或过期时提示回平台重新生成。
3. **写配置**:新建了设备时把 `{url, platform, device_id, device_key}` 写进 daemon.json(权限 0600);复用设备时不改文件。

每一步成功打一行 `✓ …`,最后提示运行 `agentuse daemon start`(已经在跑的话重启它)。**只兑换不会让 agent 在线**:要 daemon 连上 hub 才算。

```bash
agentuse daemon start [-config ~/.agentuse/daemon.json]
```

按 [daemon.json](../structs/filesystem.md#daemonjson) 里每一条 `hubs` 各起一条循环,并行连各自的 hub(默认出站 WebSocket,`transport: poll` 改轮询),领取绑在这台设备上的 agent 的运行,在 agent 目录的 worktree 里驱动 agent,跑的时候每 15 秒心跳。连不上 hub 时每 5 秒重试。daemon.json 没有任何 `hubs` 时报错,提示先 `agentuse connect`。

```bash
agentuse agents [-config ~/.agentuse/daemon.json]
```

对 daemon.json 里的每一条,调 `GET /v1/device`,打印这台设备(名字、id、在线 / 离线)和绑在上面的 agent(`@handle`、工作目录、`wake`)。

```bash
agentuse disconnect <agent> [-config ~/.agentuse/daemon.json]
```

把 `@<agent>`(`@` 可省)从这台电脑上解绑(`POST /v1/device/disconnect`)。依次试 daemon.json 里的每一条,第一个解绑成功的就停;都不成功报「这台电脑上没有绑定 @…」。解绑后 agent 还在平台上,变成未接入,可以用新接入码接到别处。

## agent(运行里)

这些命令只在 daemon 启动的 agent 里能用,读 `AGENTUSE_URL` / `AGENTUSE_TOKEN`:

| 命令 | 等价于 | 说明 |
|---|---|---|
| `agentuse reply "<文本>"` | `POST /v1/run/reply` | 回复评论;文本为 `-` 时从 stdin 读 |
| `agentuse status <状态>` | `POST /v1/run/status` | 改线程对象的状态,如 `in_review` |
| `agentuse activity <type> "<文本>"` | `POST /v1/run/activity` | 报进度 |
| `agentuse run` | `GET /v1/run` | 打印这次运行的令牌范围 |

## 其它

```bash
agentuse version
agentuse help
```

退出码:0 成功,1 出错(错误打到 stderr),2 用法不对。
