# agentuse

普通用户在自己电脑上用的 CLI。日常不用手动敲：页面上的安装命令(`setup.sh`)会替你下载、注册并装成后台服务。下面这些命令是给排查问题和手动接入用的。

本机配置在 `~/.agentuse/daemon.json`(所有命令都可以用 `-config` 换路径),格式见 [apis/daemon](../apis/daemon.md#本机配置)。

## 命令

| 命令 | 说明 |
|---|---|
| `agentuse register -server URL -token st_…` | 注册这台电脑 |
| `agentuse daemon` | 前台运行 daemon |
| `agentuse status` | 看本机的注册和连接状态 |
| `agentuse agents` | 看本机检测到哪些 agent |
| `agentuse version` | 版本 |

### register

```bash
agentuse register -server https://agentuse.example.com -token st_…
```

用页面上生成的一次性安装令牌换长期密钥，写进配置文件(保留已有的 `agents` 配置)。`-token` 不给时读环境变量 `TOKEN`。已经注册过的再注册一次，会覆盖成新的 daemon。

调用 [`POST /api/daemon/register`](../apis/daemon.md#post-apidaemonregister)。

### daemon

```bash
agentuse daemon
```

前台运行：连上服务、上报本机 agent、领任务、执行、汇报;断线自动重连。Ctrl-C / SIGTERM 退出。

`setup.sh` 装的后台服务(launchd / systemd --user / nohup)跑的就是这条命令。日志打到 stderr。

### status

```
$ agentuse status
服务     https://agentuse.example.com
daemon   dm_5c1c8602536e55cb
配置     ~/.agentuse/daemon.json
服务连通 正常(密钥有效)
后台服务 systemd --user: active
```

只读：读配置、调 [`GET /api/daemon/me`](../apis/daemon.md#get-apidaemonme) 验证密钥(不建 WebSocket,不影响正在跑的 daemon)、看后台服务是否在跑。没注册时提示去页面复制安装命令。

### agents

```
$ agentuse agents
名字        命令                       版本
claude      /opt/node/bin/claude       2.1.310 (Claude Code)
codex       -                          未安装
my-harness  /usr/local/bin/my-harness
```

按 daemon 的检测逻辑列出本机 agent,和 daemon 连上时上报的一致。是否开启在页面上设置，这里不管。

## 不做的

- 不在这里开关 agent、建任务、管用户：这些在网页 / [HTTP API](../apis/README.md) 上做。
- 卸载先手动：停掉后台服务、删 `~/.agentuse/`。以后可以加 `agentuse uninstall`。
