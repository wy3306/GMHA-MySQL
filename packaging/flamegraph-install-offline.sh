#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PACKAGES="$ROOT/packages"
TOOLS_DIR=${GMHA_FLAMEGRAPH_TOOLS_DIR:-/opt/gmha-tools/flamegraph}

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行离线安装器。" >&2
  exit 1
fi

if [ -r /etc/os-release ]; then
  # /etc/os-release is supplied by the local Linux distribution.
  # shellcheck disable=SC1091
  . /etc/os-release
fi

family=unknown
identity=$(printf '%s %s' "${ID:-}" "${ID_LIKE:-}" | tr '[:upper:]' '[:lower:]')
case "$identity" in
  *debian*|*ubuntu*) family=deb ;;
  *rhel*|*fedora*|*centos*|*rocky*|*almalinux*|*suse*|*opensuse*) family=rpm ;;
  *alpine*) family=apk ;;
  *arch*) family=arch ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  i386|i486|i586|i686) arch=386 ;;
esac

mkdir -p "$TOOLS_DIR/bin"
if [ -x "$ROOT/bin/perf-$arch" ]; then
  cp "$ROOT/bin/perf-$arch" "$TOOLS_DIR/bin/perf"
  chmod 0755 "$TOOLS_DIR/bin/perf"
  echo "已安装离线独立 perf：$TOOLS_DIR/bin/perf"
fi

if command -v perf >/dev/null 2>&1 || [ -x "$TOOLS_DIR/bin/perf" ]; then
  echo "perf 已可用；GMHA Agent 的全系统和进程火焰图均可使用。"
  exit 0
fi

if [ ! -d "$PACKAGES" ]; then
  echo "包内没有 perf 依赖。PID/进程模式仍可使用 Agent 内置 /proc 兼容采样；全系统模式需要补充 perf。" >&2
  exit 0
fi
if ! find "$PACKAGES" -type f -print -quit | grep -q .; then
  echo "离线包未附带 perf 软件包。PID/进程模式可直接使用 Agent 内置 /proc 兼容采样。" >&2
  exit 0
fi

case "$family" in
  deb)
    set -- "$PACKAGES"/*.deb
    [ -e "$1" ] || { echo "没有适用于 Debian/Ubuntu 的 .deb 包。" >&2; exit 1; }
    dpkg -i "$@"
    ;;
  rpm)
    set -- "$PACKAGES"/*.rpm
    [ -e "$1" ] || { echo "没有适用于 RPM 系 Linux 的 .rpm 包。" >&2; exit 1; }
    rpm -Uvh --replacepkgs "$@"
    ;;
  apk)
    set -- "$PACKAGES"/*.apk
    [ -e "$1" ] || { echo "没有适用于 Alpine 的 .apk 包。" >&2; exit 1; }
    apk add --no-network --allow-untrusted "$@"
    ;;
  arch)
    set -- "$PACKAGES"/*.pkg.tar.*
    [ -e "$1" ] || { echo "没有适用于 Arch Linux 的离线包。" >&2; exit 1; }
    pacman -U --noconfirm "$@"
    ;;
  *)
    echo "无法识别发行版。可将静态 perf 放入 bin/perf-$arch，或手工安装 packages/ 中的软件包。" >&2
    exit 1
    ;;
esac

if command -v perf >/dev/null 2>&1; then
  echo "离线安装完成：$(perf --version 2>/dev/null || echo perf)"
else
  echo "软件包已安装，但 perf 仍不在 PATH 中；请检查内核工具包是否与当前内核匹配。" >&2
  exit 1
fi
