#!/bin/sh
set -eu

JKV_DIR=${JKV_DIR:-"$HOME/.jkv"}
JKV_REPO=${JKV_REPO:-fishandsheep/jkv}
BIN_DIR="$JKV_DIR/bin"
mkdir -p "$BIN_DIR"

source_root=
case "$0" in
  */install.sh|install.sh) source_root=$(CDPATH= cd "$(dirname "$0")" && pwd) ;;
esac

if [ -z "${JKV_DOWNLOAD_BASE:-}" ] && [ -n "$source_root" ] &&
   [ -f "$source_root/go.mod" ] && [ -d "$source_root/cmd/jkv" ] &&
   command -v go >/dev/null 2>&1; then
  echo "从本地源码构建 jkv..."
  (cd "$source_root" && go build -trimpath -ldflags "-s -w" -o "$BIN_DIR/jkv" ./cmd/jkv)
else
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "不支持架构: $arch" >&2; exit 1 ;;
  esac
  case "$os" in linux|darwin) ;; *) echo "不支持系统: $os" >&2; exit 1 ;; esac

  download_base=${JKV_DOWNLOAD_BASE:-"https://github.com/$JKV_REPO/releases/latest/download"}
  download_base=${download_base%/}
  asset="jkv-$os-$arch"
  url="$download_base/$asset"
  tmp=$(mktemp "$BIN_DIR/.jkv.XXXXXX")
  sum_file="$tmp.sha256"
  trap 'rm -f "$tmp" "$sum_file"' EXIT INT TERM

  echo "下载 $asset..."
  curl -fL --retry 3 -o "$tmp" "$url"
  curl -fL --retry 3 -o "$sum_file" "$url.sha256"
  expected=$(awk '{print $1}' "$sum_file")
  if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp" | awk '{print $1}')
  else actual=$(shasum -a 256 "$tmp" | awk '{print $1}'); fi
  [ "$expected" = "$actual" ] || { echo "SHA-256 校验失败" >&2; exit 1; }
  chmod 755 "$tmp"
  mv -f "$tmp" "$BIN_DIR/jkv"
fi
chmod 755 "$BIN_DIR/jkv"

shell_name=$(basename "${SHELL:-sh}")
case "$shell_name" in
  zsh) rc="$HOME/.zshrc" ;;
  bash) rc="$HOME/.bashrc" ;;
  *) rc="$HOME/.profile"; shell_name=bash ;;
esac
marker='# jkv init'
line='export JKV_DIR="$HOME/.jkv"; export PATH="$JKV_DIR/bin:$PATH"; eval "$(jkv init '"$shell_name"')" # jkv init'
if ! grep -F "$marker" "$rc" >/dev/null 2>&1; then
  printf '\n%s\n' "$line" >> "$rc"
fi

echo "jkv 已安装: $BIN_DIR/jkv"
echo "重新打开终端，或运行:"
echo "  export JKV_DIR=\"$JKV_DIR\"; export PATH=\"$BIN_DIR:\$PATH\"; eval \"\$(jkv init $shell_name)\""
