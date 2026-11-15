#!/usr/bin/env bash
# agentuse 一键安装：从 GitHub Release 下载预编译的 agentuse-server,
# 以及给用户电脑 setup.sh 下载用的各平台 agentuse(放到 ~/.agentuse/dist)。
#
#   curl -fsSL https://raw.githubusercontent.com/memory-co/agentuse/main/install.sh | bash
#
# 可选环境变量:
#   VERSION=v0.0.5        装指定版本(默认最新)
#   INSTALL_DIR=/usr/local/bin  agentuse-server 装到哪(默认 /usr/local/bin,不可写就用 ~/.local/bin)
#   DIST_DIR=~/.agentuse/dist   各平台 agentuse 放哪(agentuse-server start 默认就读这里)
set -euo pipefail

REPO="memory-co/agentuse"
PLATFORMS="linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"

die() { echo "错误: $*" >&2; exit 1; }
info() { echo "==> $*"; }

command -v curl >/dev/null 2>&1 || die "需要 curl"

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "不支持的系统: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的架构: $(uname -m)" ;;
esac

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || die "查不到最新版本，可以用 VERSION=v0.0.5 指定"
fi
BASE="https://github.com/$REPO/releases/download/$VERSION"

if [ -z "${INSTALL_DIR:-}" ]; then
  INSTALL_DIR=/usr/local/bin
  if [ ! -w "$INSTALL_DIR" ] && [ "$(id -u)" != 0 ]; then
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi
DIST_DIR="${DIST_DIR:-$HOME/.agentuse/dist}"

TMP="$(mktemp -d)"
LOCK_DIR=""
INSTALL_TTY_STATE=""
STAGED_SERVER=""
cleanup() {
  if [ -n "$INSTALL_TTY_STATE" ]; then
    stty "$INSTALL_TTY_STATE" <&9 2>/dev/null || true
    exec 9>&-
  fi
  rm -rf "$TMP"
  if [ -n "$STAGED_SERVER" ]; then rm -f "$STAGED_SERVER"; fi
  if [ -n "$LOCK_DIR" ]; then rmdir "$LOCK_DIR" 2>/dev/null || true; fi
}
trap cleanup EXIT

fetch() { curl -fSL --progress-bar "$BASE/$1" -o "$TMP/$1" || die "下载 $BASE/$1 失败"; }

info "安装 agentuse $VERSION ($OS/$ARCH)"
fetch SHA256SUMS
fetch "agentuse-server-$OS-$ARCH"
for p in $PLATFORMS; do fetch "agentuse-$p"; done

info "校验 SHA256"
# 只校验下载了的文件(macOS 自带的 shasum 不一定支持 --ignore-missing)
FILES="agentuse-server-$OS-$ARCH"
for p in $PLATFORMS; do FILES="$FILES agentuse-$p"; done
for f in $FILES; do grep " \*\{0,1\}$f\$" "$TMP/SHA256SUMS" || die "SHA256SUMS 里没有 $f"; done > "$TMP/SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TMP" && sha256sum -c --quiet SUMS) || die "校验失败"
elif command -v shasum >/dev/null 2>&1; then
  (cd "$TMP" && shasum -a 256 -c --quiet SUMS) || die "校验失败"
else
  echo "警告: 没有 sha256sum / shasum,跳过校验" >&2
fi

mkdir -p "$INSTALL_DIR" "$DIST_DIR"
# Replace atomically so an existing Server may keep running until the optional restart.
STAGED_SERVER="$INSTALL_DIR/.agentuse-server.$$"
install -m 755 "$TMP/agentuse-server-$OS-$ARCH" "$STAGED_SERVER"
mv -f "$STAGED_SERVER" "$INSTALL_DIR/agentuse-server"
STAGED_SERVER=""
for p in $PLATFORMS; do install -m 755 "$TMP/agentuse-$p" "$DIST_DIR/agentuse-$p"; done

info "agentuse-server → $INSTALL_DIR/agentuse-server"
info "各平台 agentuse → $DIST_DIR/"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "提示: $INSTALL_DIR 不在 PATH 里，加一行到 shell 配置: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

CUSTOM_STARTED=0
configure_admin_password() {
  # stdin is the script when invoked through curl | bash; read only from the terminal.
  if ! { exec 9<>/dev/tty; } 2>/dev/null; then return; fi
  set +x
  INSTALL_TTY_STATE="$(stty -g <&9)"
  stty -echo <&9
  local password="" confirmation="" bytes=""
  while :; do
    printf '\n设置管理员登录密码（直接回车保留原方式；输入后将后台启动或重启 Server）: ' >&9
    if ! IFS= read -r -s -u 9 password; then password=""; break; fi
    printf '\n' >&9
    [ -n "$password" ] || break
    bytes="$(printf '%s' "$password" | wc -c | tr -d ' ')"
    if [ "$bytes" -gt 1024 ] || [[ "$password" =~ ^[[:space:]]|[[:space:]]$|[[:cntrl:]] ]] || [[ "$password" == dk_* ]]; then
      printf '密码最多 1024 字节，不能包含首尾空白、控制字符或使用 dk_ 前缀。\n' >&9
      continue
    fi
    printf '再次输入密码: ' >&9
    if ! IFS= read -r -s -u 9 confirmation; then password=""; break; fi
    printf '\n' >&9
    if [ "$password" = "$confirmation" ]; then break; fi
    printf '两次输入不一致，请重新输入。\n' >&9
  done
  stty "$INSTALL_TTY_STATE" <&9
  INSTALL_TTY_STATE=""
  exec 9>&-
  [ -n "$password" ] || return 0

  umask 077
  local state="$HOME/.agentuse" pidfile="$HOME/.agentuse/server.pid" log="$HOME/.agentuse/server.log"
  local pid="" args="" usage="" attempt=0 started=0
  usage="$("$INSTALL_DIR/agentuse-server" start --help 2>&1 || true)"
  case "$usage" in
    *-admin-token-stdin*-pid-file*) ;;
    *) die "所安装的 $VERSION 尚不支持安装时设置密码，请发布或选择包含此功能的新 Server 版本；原 Server 的登录凭据和进程未改动" ;;
  esac
  mkdir -p "$state"
  if ! mkdir "$state/server.install.lock" 2>/dev/null; then die "已有安装进程正在启动 Server"; fi
  LOCK_DIR="$state/server.install.lock"

  if [ -f "$pidfile" ]; then
    IFS= read -r pid <"$pidfile" || true
    case "$pid" in ''|*[!0-9]*|0|1) die "无效的 Server PID 文件: $pidfile" ;; esac
    if kill -0 "$pid" 2>/dev/null; then
      args="$(ps -p "$pid" -o args= 2>/dev/null || true)"
      case "$args" in
        *agentuse-server*start*--pid-file*"$pidfile"*) ;;
        *) die "$pidfile 未指向本安装脚本管理的 Server，请检查后再运行" ;;
      esac
      info "停止原 Server (pid $pid)"
      kill -TERM "$pid"
      while kill -0 "$pid" 2>/dev/null; do
        case "$(ps -p "$pid" -o stat= 2>/dev/null || true)" in Z*|'') break ;; esac
        attempt=$((attempt + 1))
        [ "$attempt" -lt 50 ] || die "Server 未能正常退出，请查看 $log"
        sleep 0.2
      done
    fi
    rm -f "$pidfile"
  fi
  [ ! -f "$log" ] || mv -f "$log" "$log.previous"
  info "设置管理员登录密码并启动 Server"
  printf '%s' "$password" | nohup "$INSTALL_DIR/agentuse-server" start \
    --dist "$DIST_DIR" --pid-file "$pidfile" --admin-token-stdin >"$log" 2>&1 &
  pid=$!
  unset password confirmation
  attempt=0
  while [ "$attempt" -lt 100 ]; do
    kill -0 "$pid" 2>/dev/null || die "Server 启动失败，请查看 $log"
    if [ -f "$pidfile" ] && grep -q 'agentuse 服务已启动:' "$log"; then started=1; break; fi
    attempt=$((attempt + 1))
    sleep 0.1
  done
  if [ "$started" != 1 ]; then
    kill -TERM "$pid" 2>/dev/null || true
    die "Server 启动超时，请查看 $log"
  fi
  CUSTOM_STARTED=1
  info "管理员登录密码已设置，Server 已在后台启动 (pid $pid)"
  grep 'agentuse 服务已启动:' "$log"
  info "请在登录页面输入刚才设置的密码。日志: $log；PID: $pidfile"
}

configure_admin_password
if [ "$CUSTOM_STARTED" = 1 ]; then
  info "装好了。以后可再次运行安装脚本并输入新密码，以修改密码并重启。"
  exit 0
fi

DIST_FLAG=""
[ "$DIST_DIR" = "$HOME/.agentuse/dist" ] || DIST_FLAG=" -dist $DIST_DIR"
cat <<EOF

装好了。起服务:

  agentuse-server start$DIST_FLAG

默认监听 :8080,第一次启动会在日志里打印管理员登录链接。
EOF
