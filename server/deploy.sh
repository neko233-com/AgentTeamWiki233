#!/usr/bin/env bash
# ghaccel233 一键部署（Linux / macOS）
# 用法:
#   ./deploy.sh                          # 默认端口 8233，productId=default，装到 ~/ghaccel233
#   ./deploy.sh --port 9000 --product-id team-a --service
# 环境变量同样可用: PORT / PRODUCT_ID / INSTALL_DIR / VERSION / MIRRORS
set -euo pipefail

REPO="neko233-com/AgentTeamWiki233"
BIN_NAME="ghaccel233"
PORT="${PORT:-8233}"
PRODUCT_ID="${PRODUCT_ID:-default}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/ghaccel233}"
VERSION="${VERSION:-latest}"
USE_SERVICE=0
# 公共 GitHub 加速镜像（直连失败时按顺序尝试），可用 MIRRORS 环境变量覆盖（空格分隔）
MIRRORS="${MIRRORS:-https://gh-proxy.com/ https://ghproxy.net/ https://ghfast.top/}"

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --product-id) PRODUCT_ID="$2"; shift 2 ;;
    --dir) INSTALL_DIR="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --service) USE_SERVICE=1; shift ;;
    -h|--help) grep '^#' "$0" | head -6; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

log() { printf '\033[32m[deploy]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[deploy]\033[0m %s\n' "$*" >&2; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in linux|darwin) ;; *) die "不支持的系统: $OS（Windows 请用 deploy.ps1）" ;; esac
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的架构: $ARCH" ;;
esac
ASSET="${BIN_NAME}-${OS}-${ARCH}"

mkdir -p "$INSTALL_DIR"
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd)"
BIN_PATH="$INSTALL_DIR/$BIN_NAME"

download() { # download <url> <out>
  curl -fL --connect-timeout 8 --retry 2 -o "$2" "$1" 2>/dev/null
}

fetch_binary() {
  local base="https://github.com/$REPO/releases"
  local path="latest/download/$ASSET"
  [ "$VERSION" != "latest" ] && path="download/$VERSION/$ASSET"
  local url="$base/$path"

  log "尝试直连下载: $url"
  if download "$url" "$BIN_PATH"; then return 0; fi
  for m in $MIRRORS; do
    log "直连失败，尝试镜像: $m"
    if download "${m}${url}" "$BIN_PATH"; then return 0; fi
  done
  return 1
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GO_BIN="$(command -v go || true)"
if [ -z "$GO_BIN" ] && [ -x /usr/local/go/bin/go ]; then GO_BIN=/usr/local/go/bin/go; fi
if [ -f "$SCRIPT_DIR/main.go" ] && [ -n "$GO_BIN" ]; then
  log "检测到源码与 Go，直接编译…"
  (cd "$SCRIPT_DIR" && "$GO_BIN" build -trimpath -ldflags "-s -w" -o "$BIN_PATH" .)
elif fetch_binary; then
  log "下载完成: $BIN_PATH"
else
  die "下载失败且本机无 Go 源码环境。请安装 Go 后在仓库 server/ 目录重试，或手动下载 release: https://github.com/$REPO/releases"
fi
chmod +x "$BIN_PATH"

CONFIG="$INSTALL_DIR/config.json"
if [ ! -f "$CONFIG" ]; then
  cat > "$CONFIG" <<EOF
{
  "listen": ":$PORT",
  "cacheDir": "$INSTALL_DIR/cache",
  "cacheMaxMB": 2048,
  "adminToken": "",
  "products": [
    {
      "productId": "$PRODUCT_ID",
      "name": "$PRODUCT_ID",
      "token": "",
      "githubToken": "",
      "allowRepos": ["*"]
    }
  ]
}
EOF
  log "已生成配置: $CONFIG"
else
  log "沿用已有配置: $CONFIG"
fi

start_service() {
  if [ "$USE_SERVICE" = "1" ] && [ "$(id -u)" = "0" ] && command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    cat > /etc/systemd/system/ghaccel233.service <<EOF
[Unit]
Description=ghaccel233 GitHub intranet accelerator
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN_PATH -config $CONFIG
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now ghaccel233
    log "systemd 服务 ghaccel233 已启动并设为开机自启"
  else
    if [ -f "$INSTALL_DIR/ghaccel233.pid" ] && kill -0 "$(cat "$INSTALL_DIR/ghaccel233.pid")" 2>/dev/null; then
      kill "$(cat "$INSTALL_DIR/ghaccel233.pid")" || true
      sleep 1
    fi
    nohup "$BIN_PATH" -config "$CONFIG" >> "$INSTALL_DIR/ghaccel233.log" 2>&1 &
    echo $! > "$INSTALL_DIR/ghaccel233.pid"
    log "后台启动完成，日志: $INSTALL_DIR/ghaccel233.log"
  fi
}

start_service
sleep 1
if curl -fsS -m 5 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
  log "健康检查通过 ✔"
else
  die "健康检查失败，请查看日志"
fi

cat <<EOF

部署完成！
  服务地址:   http://<本机IP>:$PORT
  productId:  $PRODUCT_ID
  配置文件:   $CONFIG

使用示例（把 GitHub 地址拼到 /$PRODUCT_ID/ 后面）:
  git clone http://<本机IP>:$PORT/$PRODUCT_ID/github.com/owner/repo.git
  curl -O http://<本机IP>:$PORT/$PRODUCT_ID/raw.githubusercontent.com/owner/repo/main/README.md

给 AI agent 接入: 参考仓库 skill/ 目录（install.sh 一键安装提示词）。
EOF