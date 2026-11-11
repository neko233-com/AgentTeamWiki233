#!/usr/bin/env bash
# ghaccel233 skill 安装（Linux / macOS）
# 用法:
#   ./install.sh --server http://192.168.1.100:8233 --product-id my-product [--token xxx] [--target all]
# target: claude | codex | generic | all（默认 all）
#   claude  → ~/.claude/skills/ghaccel233/SKILL.md
#   codex   → ~/.codex/AGENTS.md（标记块幂等更新）
#   generic → ~/.ghaccel233/PROMPT.md（dsh / WorkBuddy / 豆包工作 / MiMo 等手动粘贴）
set -euo pipefail

SERVER="" PRODUCT_ID="" TOKEN="" TARGET="all"
while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2 ;;
    --product-id) PRODUCT_ID="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --target) TARGET="$2"; shift 2 ;;
    -h|--help) grep '^#' "$0" | head -9; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done
[ -n "$SERVER" ] && [ -n "$PRODUCT_ID" ] || { echo "必须提供 --server 与 --product-id" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
log() { printf '\033[32m[skill]\033[0m %s\n' "$*"; }

render() { # render <src> <dst>
  mkdir -p "$(dirname "$2")"
  sed -e "s|{{SERVER}}|$SERVER|g" -e "s|{{PRODUCT_ID}}|$PRODUCT_ID|g" -e "s|{{TOKEN}}|$TOKEN|g" "$1" > "$2"
}

install_claude() {
  render "$SCRIPT_DIR/SKILL.md" "$HOME/.claude/skills/ghaccel233/SKILL.md"
  log "Claude Code skill → ~/.claude/skills/ghaccel233/SKILL.md"
}

install_codex() {
  local dst="$HOME/.codex/AGENTS.md" rendered tmp
  rendered="$(mktemp)"
  tmp="$(mktemp)"
  render "$SCRIPT_DIR/PROMPT.md" "$rendered"
  { echo "<!-- ghaccel233:start -->"; cat "$rendered"; echo "<!-- ghaccel233:end -->"; } > "$tmp"
  mkdir -p "$HOME/.codex"
  touch "$dst"
  if grep -q "ghaccel233:start" "$dst"; then
    awk -v blockfile="$tmp" '
      BEGIN { while ((getline line < blockfile) > 0) block = block line "\n"; close(blockfile) }
      /<!-- ghaccel233:start -->/ { printf "%s", block; skip=1; next }
      /<!-- ghaccel233:end -->/ { skip=0; next }
      !skip { print }
    ' "$dst" > "$dst.new" && mv "$dst.new" "$dst"
  else
    { echo ""; cat "$tmp"; } >> "$dst"
  fi
  rm -f "$tmp" "$rendered"
  log "Codex 全局规则 → ~/.codex/AGENTS.md"
}

install_generic() {
  render "$SCRIPT_DIR/PROMPT.md" "$HOME/.ghaccel233/PROMPT.md"
  log "通用提示词 → ~/.ghaccel233/PROMPT.md"
  log "dsh / WorkBuddy / 豆包工作 / MiMo 等：把该文件内容粘贴进对应 agent 的系统提示词/项目规则即可"
}

case "$TARGET" in
  claude) install_claude ;;
  codex) install_codex ;;
  generic) install_generic ;;
  all) install_claude; install_codex; install_generic ;;
  *) echo "未知 target: $TARGET" >&2; exit 1 ;;
esac

if curl -fsS -m 3 "$SERVER/healthz" >/dev/null 2>&1; then
  log "服务器健康检查通过 ✔ ($SERVER)"
else
  log "警告: $SERVER 当前不可达，agent 将自动回退公共镜像"
fi