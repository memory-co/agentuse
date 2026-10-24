package server

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// setupScript 是 /setup.sh:下载 agentuse、用 TOKEN 注册、装成后台服务。
// __SERVER__ 在返回时替换成服务地址。
const setupScript = `#!/usr/bin/env bash
# agentuse daemon 安装脚本
# 用法: curl -fsSL "__SERVER__/setup.sh" -o setup.sh && TOKEN="<安装令牌>" bash setup.sh
set -euo pipefail

SERVER="${AGENTUSE_SERVER:-__SERVER__}"
INSTALL_DIR="${AGENTUSE_INSTALL_DIR:-$HOME/.agentuse/bin}"
BIN="$INSTALL_DIR/agentuse"

say() { printf '\033[1;34m[agentuse]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[agentuse]\033[0m %s\n' "$*" >&2; exit 1; }

[ -n "${TOKEN:-}" ] || die "缺少 TOKEN。请从 agentuse 页面复制完整的安装命令。"
command -v curl >/dev/null || die "需要 curl"

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "暂不支持的系统: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "暂不支持的架构: $(uname -m)" ;;
esac

mkdir -p "$INSTALL_DIR"
say "下载 agentuse ($OS/$ARCH) ..."
if ! curl -fsSL "$SERVER/download/agentuse-$OS-$ARCH" -o "$BIN.tmp"; then
  rm -f "$BIN.tmp"
  if command -v go >/dev/null; then
    say "服务上没有 $OS/$ARCH 的二进制，改用 go install 本地编译 ..."
    GOBIN="$INSTALL_DIR" go install github.com/memory-co/agentuse/cmd/agentuse@latest
  else
    die "下载失败：服务上没有 $OS/$ARCH 的二进制(请管理员 make dist 后用 agentuse-server start -dist 指定目录)"
  fi
else
  chmod +x "$BIN.tmp"
  mv "$BIN.tmp" "$BIN"
fi

say "注册这台电脑 ..."
"$BIN" register -server "$SERVER" -token "$TOKEN"

LOG="$HOME/.agentuse/daemon.log"
if [ "$OS" = darwin ]; then
  PLIST="$HOME/Library/LaunchAgents/dev.agentuse.daemon.plist"
  mkdir -p "$(dirname "$PLIST")"
  cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.agentuse.daemon</string>
  <key>ProgramArguments</key><array><string>$BIN</string><string>daemon</string></array>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>$PATH</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$LOG</string>
  <key>StandardErrorPath</key><string>$LOG</string>
</dict></plist>
EOF
  launchctl unload "$PLIST" 2>/dev/null || true
  launchctl load -w "$PLIST"
  say "已用 launchd 启动，日志: $LOG"
elif command -v systemctl >/dev/null && systemctl --user show-environment >/dev/null 2>&1; then
  UNIT="$HOME/.config/systemd/user/agentuse-daemon.service"
  mkdir -p "$(dirname "$UNIT")"
  cat >"$UNIT" <<EOF
[Unit]
Description=agentuse daemon
After=network-online.target

[Service]
ExecStart=$BIN daemon
Environment=PATH=$PATH
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable agentuse-daemon.service >/dev/null
  systemctl --user restart agentuse-daemon.service
  say "已用 systemd --user 启动，查看日志: journalctl --user -u agentuse-daemon -f"
  command -v loginctl >/dev/null && loginctl enable-linger "$(id -un)" 2>/dev/null || true
else
  PIDFILE="$HOME/.agentuse/daemon.pid"
  if [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null; then sleep 1; fi
  nohup "$BIN" daemon >>"$LOG" 2>&1 &
  echo $! >"$PIDFILE"
  say "已在后台启动(pid $(cat "$PIDFILE")),日志: $LOG"
  say "注意：没有 systemd/launchd,重启电脑后需要手动运行: $BIN daemon"
fi

say "完成！回到 agentuse 页面，选择这台电脑上要开启的 agent。"
`

func (s *Server) handleSetupScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.ReplaceAll(setupScript, "__SERVER__", s.baseURL(r))))
}

var binNameRe = regexp.MustCompile(`^agentuse-(linux|darwin)-(amd64|arm64)$`)

// Platforms 是 setup.sh 支持下载的平台。
var Platforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

// MissingDist 返回 dir 里缺二进制的平台。
func MissingDist(dir string) []string {
	var missing []string
	for _, p := range Platforms {
		if dir == "" || !fileExists(filepath.Join(dir, "agentuse-"+p)) {
			missing = append(missing, p)
		}
	}
	return missing
}

// handleDownload 从 DistDir 提供用户电脑上的 agentuse 二进制。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	m := binNameRe.FindStringSubmatch(name)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	path := ""
	if s.cfg.DistDir != "" {
		if p := filepath.Join(s.cfg.DistDir, name); fileExists(p) {
			path = p
		}
	}
	if path == "" {
		http.Error(w, "no binary for "+m[1]+"/"+m[2], http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="agentuse"`)
	http.ServeFile(w, r, path)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
