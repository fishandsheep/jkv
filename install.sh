#!/bin/sh
set -eu

JKV_DIR=${JKV_DIR:-"$HOME/.jkv"}
JKV_REPO=${JKV_REPO:-fishandsheep/jkv}
BIN_DIR="$JKV_DIR/bin"
MANAGED_BEGIN='# >>> jkv managed >>>'
MANAGED_END='# <<< jkv managed <<<'
modify_profile=true
action=install
purge=false
assume_yes=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-modify-profile) modify_profile=false ;;
    --uninstall) action=uninstall ;;
    --purge) action=uninstall; purge=true ;;
    --yes) assume_yes=true ;;
    *) echo "未知安装器选项: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ "${JKV_MODIFY_PROFILE:-1}" = 0 ]; then
  modify_profile=false
fi

shell_name=$(basename "${SHELL:-sh}")
case "$shell_name" in
  zsh) rc="$HOME/.zshrc" ;;
  bash) rc="$HOME/.bashrc" ;;
  fish) rc="$HOME/.config/fish/config.fish" ;;
  *) rc="$HOME/.profile"; shell_name=bash ;;
esac

remove_managed_block() {
  [ -f "$rc" ] || return 0
  tmp=$(mktemp "${rc}.jkv.XXXXXX")
  awk -v begin="$MANAGED_BEGIN" -v end="$MANAGED_END" '
    $0 == begin { managed=1; next }
    $0 == end { managed=0; next }
    managed != 1 && index($0, "# jkv init") == 0 { print }
  ' "$rc" > "$tmp"
  mv -f "$tmp" "$rc"
}

if [ "$action" = uninstall ]; then
  remove_managed_block
  rm -f "$BIN_DIR/jkv"
  if [ "$purge" = true ]; then
    case "$JKV_DIR" in
      ""|"/"|"."|"$HOME")
        echo "拒绝清理危险目录: $JKV_DIR" >&2
        exit 2
        ;;
    esac
    if [ "$assume_yes" != true ]; then
      if [ ! -t 0 ]; then
        echo "--purge 在非交互环境必须同时使用 --yes" >&2
        exit 2
      fi
      printf '将永久删除 %s，继续? [y/N] ' "$JKV_DIR"
      read -r answer
      case "$answer" in y|Y|yes|YES) ;; *) echo "已取消"; exit 0 ;; esac
    fi
    rm -rf "$JKV_DIR"
    echo "jkv 已彻底卸载: $JKV_DIR（不可恢复）"
  else
    echo "jkv 已卸载；已安装工具和配置保留在: $JKV_DIR"
  fi
  exit 0
fi

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

  asset="jkv-$os-$arch"
  tmp=$(mktemp "$BIN_DIR/.jkv.XXXXXX")
  sum_file="$tmp.sha256"
  trap 'rm -f "$tmp" "$sum_file"' EXIT INT TERM

  embedded_base='__JKV_CN_DOWNLOAD_BASE__'
  case "$embedded_base" in __JKV_*) embedded_base= ;; esac
  primary_base=${JKV_DOWNLOAD_BASE:-"$embedded_base"}
  if [ -n "${JKV_VERSION:-}" ]; then
    github_base="https://github.com/$JKV_REPO/releases/download/$JKV_VERSION"
  else
    github_base="https://github.com/$JKV_REPO/releases/latest/download"
  fi
  fallback_base=${JKV_FALLBACK_BASE:-"$github_base"}
  downloaded=false
  previous_base=

  for base in "$primary_base" "$fallback_base"; do
    base=${base%/}
    [ -n "$base" ] || continue
    [ "$base" != "$previous_base" ] || continue
    previous_base=$base
    url="$base/$asset"
    echo "下载 $asset: $base"
    if ! curl -fL --retry 3 --connect-timeout 10 -o "$tmp" "$url"; then
      echo "下载失败，尝试后备地址..." >&2
      continue
    fi
    if ! curl -fL --retry 3 --connect-timeout 10 -o "$sum_file" "$url.sha256"; then
      echo "校验文件下载失败，尝试后备地址..." >&2
      continue
    fi
    expected=$(awk '{print $1}' "$sum_file")
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$tmp" | awk '{print $1}')
    else
      actual=$(shasum -a 256 "$tmp" | awk '{print $1}')
    fi
    if [ "$expected" != "$actual" ]; then
      echo "SHA-256 校验失败；拒绝尝试其他来源" >&2
      exit 1
    fi
    downloaded=true
    break
  done
  if [ "$downloaded" != true ]; then
    echo "所有 jkv 下载地址均不可用" >&2
    exit 1
  fi
  chmod 755 "$tmp"
  mv -f "$tmp" "$BIN_DIR/jkv"
fi
chmod 755 "$BIN_DIR/jkv"

if [ "$modify_profile" = true ]; then
  mkdir -p "$(dirname "$rc")"
  remove_managed_block
  {
    printf '\n%s\n' "$MANAGED_BEGIN"
    if [ "$shell_name" = fish ]; then
      escaped_dir=$(printf '%s' "$JKV_DIR" | sed "s/'/\\\\'/g")
      printf "set -gx JKV_DIR '%s'\n" "$escaped_dir"
      printf 'fish_add_path --prepend "$JKV_DIR/bin"\n'
      printf 'jkv init fish | source\n'
    else
      escaped_dir=$(printf '%s' "$JKV_DIR" | sed "s/'/'\\\\''/g")
      printf "export JKV_DIR='%s'\n" "$escaped_dir"
      printf 'export PATH="$JKV_DIR/bin:$PATH"\n'
      printf 'eval "$(jkv init %s)"\n' "$shell_name"
    fi
    printf '%s\n' "$MANAGED_END"
  } >> "$rc"
  echo "已更新 shell 配置: $rc"
fi

echo "jkv 已安装: $BIN_DIR/jkv"
if [ "$shell_name" = fish ]; then
  echo "重新打开终端，或运行: set -gx JKV_DIR '$JKV_DIR'; fish_add_path '$BIN_DIR'; jkv init fish | source"
else
  echo "重新打开终端，或运行:"
  echo "  export JKV_DIR=\"$JKV_DIR\"; export PATH=\"$BIN_DIR:\$PATH\"; eval \"\$(jkv init $shell_name)\""
fi
