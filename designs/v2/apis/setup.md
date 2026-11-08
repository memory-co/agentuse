# setup

用戶電腦上的安裝命令用到的兩個接口，不需要登錄。

## GET /setup.sh

返回安裝腳本，腳本里的服務地址已替換成當前服務(規則同 [daemons](daemons.md#daemon-對象) 的安裝命令)。用法:

```bash
curl -fsSL "https://agentuse.example.com/setup.sh" -o setup.sh && TOKEN="st_…" bash setup.sh
```

腳本讀這些環境變量:

| 變量 | 說明 |
|---|---|
| `TOKEN` | 必填，頁面上生成的安裝令牌 |
| `AGENTUSE_SERVER` | 覆蓋服務地址 |
| `AGENTUSE_INSTALL_DIR` | 安裝目錄，默認 `~/.agentuse/bin` |

它依次做:

1. 下載 `/download/agentuse-<os>-<arch>` 到安裝目錄(下載失敗且本機有 `go` 時，改用 `go install`)。
2. `agentuse register -server … -token …`,見 [daemon](daemon.md#post-apidaemonregister)。
3. 裝成後臺服務並啓動:macOS 用 launchd(`~/Library/LaunchAgents/dev.agentuse.daemon.plist`);Linux 有 `systemd --user` 就用它(`agentuse-daemon.service`);都沒有就 `nohup`。日誌在 `~/.agentuse/daemon.log`(systemd 下用 `journalctl --user -u agentuse-daemon`)。

## GET /download/{file}

下載 agentuse 二進制。`file` 只能是 `agentuse-<os>-<arch>`,`os` 爲 `linux` / `darwin`,`arch` 爲 `amd64` / `arm64`。

從服務的 `-dist` 目錄裏取同名文件(`make dist` 生成),沒有返回 404。
