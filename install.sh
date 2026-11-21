#!/usr/bin/env bash
# agentuse 一键安装：从 GitHub Release 或 OSS 下载预编译的 agentuse-server,
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
GITHUB_BASE="https://github.com/$REPO/releases/download"
OSS_BASE="https://agentuse.oss-cn-beijing.aliyuncs.com/releases"
OSS_CNAME="https://agentuse.cn-beijing.taihangpfm.cn/releases"
PLATFORMS="linux-amd64 linux-arm64 darwin-amd64 darwin-arm64"

die() { echo "错误: $*" >&2; exit 1; }
info() { echo "==> $*"; }

command -v curl >/dev/null 2>&1 || die "需要 curl"
if command -v sha256sum >/dev/null 2>&1; then
  SHA256=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  SHA256=(shasum -a 256)
else
  die "需要 sha256sum 或 shasum 校验下载文件"
fi

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

latest_version() {
  local metadata tag mirror
  if metadata="$(curl -fsSL --connect-timeout 3 --max-time 5 "https://api.github.com/repos/$REPO/releases/latest")"; then
    tag="$(printf '%s\n' "$metadata" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')"
    if [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then printf '%s\n' "$tag"; return; fi
  fi
  for mirror in "$OSS_BASE" "$OSS_CNAME"; do
    if tag="$(curl -fsSL --connect-timeout 3 --max-time 5 "$mirror/latest")" && [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      printf '%s\n' "$tag"
      return
    fi
  done
  return 1
}

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(latest_version)" || die "查不到最新版本，可以用 VERSION=v0.2.1 指定"
fi
[[ "$VERSION" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || die "无效的版本号: $VERSION"

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

FILES=("agentuse-server-$OS-$ARCH")
for p in $PLATFORMS; do FILES+=("agentuse-$p"); done
SOURCES=("$GITHUB_BASE" "$OSS_BASE" "$OSS_CNAME")

probe_source() {
  local index="$1" seconds
  seconds="$(curl -fsSL --connect-timeout 3 --max-time 6 --max-filesize 65536 \
    -w '%{time_total}' "${SOURCES[$index]}/$VERSION/SHA256SUMS" -o "$TMP/$index.manifest" 2>/dev/null)" || return 1
  [[ "$seconds" =~ ^[0-9]+\.[0-9]+$ ]] || return 1
  awk -v files="${FILES[*]}" '
    BEGIN { count = split(files, names, " "); for (i = 1; i <= count; i++) wanted[names[i]] = 1 }
    { sub(/^\*/, "", $2) }
    $2 in wanted {
      if (NF != 2 || length($1) != 64 || $1 !~ /^[0-9a-fA-F]+$/ || seen[$2]++) exit 1
      print tolower($1), $2
      found++
    }
    END { if (found != count) exit 1 }
  ' "$TMP/$index.manifest" > "$TMP/$index.sums" || return 1
  printf '%s %s\n' "$seconds" "$index" > "$TMP/$index.ready"
}

fetch_verified() {
  local name="$1" digest index
  digest="$(awk -v name="$name" '$2 == name {print $1}' "$manifest")"
  for index in $source_order; do
    if curl -fSL --progress-bar --connect-timeout 5 --speed-limit 1024 --speed-time 15 \
      "${SOURCES[$index]}/$VERSION/$name" -o "$TMP/$name" &&
      (cd "$TMP" && printf '%s  %s\n' "$digest" "$name" | "${SHA256[@]}" -c --quiet); then
      return
    fi
    info "${SOURCES[$index]} 下载或校验 $name 失败，尝试备用源"
  done
  die "$name 下载或 SHA256 校验失败"
}

info "安装 agentuse $VERSION ($OS/$ARCH)"
pids=()
for index in "${!SOURCES[@]}"; do
  probe_source "$index" &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid" || true; done
ready=("$TMP/"*.ready)
[ -f "${ready[0]}" ] || die "下载或校验 $VERSION 的 SHA256SUMS 失败，所有下载源均不可用"
source_order="$(LC_ALL=C sort -k1,1n -k2,2n "${ready[@]}" | awk '{print $2}')"
read -r first_source <<< "$source_order"
manifest="$TMP/$first_source.sums"
# Prefer the GitHub checksums whenever available, even when a mirror is faster.
if [ -f "$TMP/0.ready" ]; then manifest="$TMP/0.sums"; fi
info "优先下载源: ${SOURCES[$first_source]}"
for f in "${FILES[@]}"; do fetch_verified "$f"; done

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
