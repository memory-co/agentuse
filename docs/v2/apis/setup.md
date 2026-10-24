# setup

用户电脑上的安装命令用到的两个接口，不需要登录。

## GET /setup.sh

返回安装脚本，脚本里的服务地址已替换成当前服务(规则同 [daemons](daemons.md#daemon-对象) 的安装命令)。用法:

```bash
curl -fsSL "https://agentuse.example.com/setup.sh" -o setup.sh && TOKEN="st_…" bash setup.sh
```

脚本读这些环境变量:

| 变量 | 说明 |
|---|---|
| `TOKEN` | 必填，页面上生成的安装令牌 |
| `AGENTUSE_SERVER` | 覆盖服务地址 |
| `AGENTUSE_INSTALL_DIR` | 安装目录，默认 `~/.agentuse/bin` |

它依次做:

1. 下载 `/download/agentuse-<os>-<arch>` 到安装目录(下载失败且本机有 `go` 时，改用 `go install`)。
2. `agentuse daemon register --server … --token …`,见 [daemon](daemon.md#post-apidaemonregister)。
3. 装成后台服务并启动:macOS 用 launchd(`~/Library/LaunchAgents/dev.agentuse.daemon.plist`);Linux 有 `systemd --user` 就用它(`agentuse-daemon.service`);都没有就 `nohup`。日志在 `~/.agentuse/daemon.log`(systemd 下用 `journalctl --user -u agentuse-daemon`)。

## GET /download/{file}

下载 agentuse 二进制。`file` 只能是 `agentuse-<os>-<arch>`,`os` 为 `linux` / `darwin`,`arch` 为 `amd64` / `arm64`。

先在服务的 `-dist` 目录里找同名文件(`make dist` 生成);找不到且请求的平台和服务自己一样时，返回服务自己的可执行文件;都没有返回 404。
