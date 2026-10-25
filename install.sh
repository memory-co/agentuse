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
trap 'rm -rf "$TMP"' EXIT

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
install -m 755 "$TMP/agentuse-server-$OS-$ARCH" "$INSTALL_DIR/agentuse-server"
for p in $PLATFORMS; do install -m 755 "$TMP/agentuse-$p" "$DIST_DIR/agentuse-$p"; done

info "agentuse-server → $INSTALL_DIR/agentuse-server"
info "各平台 agentuse → $DIST_DIR/"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "提示: $INSTALL_DIR 不在 PATH 里，加一行到 shell 配置: export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

DIST_FLAG=""
[ "$DIST_DIR" = "$HOME/.agentuse/dist" ] || DIST_FLAG=" -dist $DIST_DIR"
cat <<EOF

装好了。起服务:

  agentuse-server start$DIST_FLAG

默认监听 :8080,第一次启动会在日志里打印管理员登录链接。
EOF
