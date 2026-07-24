#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-V0.0.3}
ARCH=${2:-amd64}
PERF_PACKAGES=${3:-}
OUTPUT=${4:-"$ROOT/dist/gmha-flamegraph-${VERSION}-linux-${ARCH}-offline.tar.gz"}

case "$ARCH" in
  amd64|arm64|386|ppc64le|s390x|riscv64) ;;
  *) echo "不支持的 GOARCH：$ARCH" >&2; exit 1 ;;
esac

TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/gmha-flamegraph.XXXXXX")
cleanup() { rm -rf "$TEMP_DIR"; }
trap cleanup EXIT INT TERM

BUNDLE="$TEMP_DIR/gmha-flamegraph-${VERSION}-linux-${ARCH}"
mkdir -p "$BUNDLE/bin" "$BUNDLE/packages"
if [ -d "$ROOT/cmd/agent" ]; then
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
    -ldflags="-s -w -X gmha/internal/buildinfo.Version=$VERSION" \
    -o "$BUNDLE/bin/agentd" "$ROOT/cmd/agent"
elif [ "$ARCH" = "amd64" ] && [ -x "$ROOT/bin/agentd" ]; then
  cp "$ROOT/bin/agentd" "$BUNDLE/bin/agentd"
else
  echo "当前发布包只能复用其 amd64 Agent；构建其他架构请在 GMHA 源码目录运行。" >&2
  exit 1
fi

INSTALLER="$ROOT/packaging/flamegraph-install-offline.sh"
if [ ! -f "$INSTALLER" ]; then
  INSTALLER="$ROOT/scripts/flamegraph-install-offline.sh"
fi
cp "$INSTALLER" "$BUNDLE/install.sh"
chmod 0755 "$BUNDLE/install.sh" "$BUNDLE/bin/agentd"

if [ -n "$PERF_PACKAGES" ]; then
  if [ ! -d "$PERF_PACKAGES" ]; then
    echo "perf 依赖目录不存在：$PERF_PACKAGES" >&2
    exit 1
  fi
  cp -R "$PERF_PACKAGES"/. "$BUNDLE/packages/"
fi

cat > "$BUNDLE/README.txt" <<EOF
GMHA Linux 火焰图离线包

1. bin/agentd 是支持 flamegraph 任务的新 Agent，可在 Manager 的版本升级页面上传并分发。
2. 在目标机执行 sudo ./install.sh，可离线安装包内 perf；PID/进程模式没有 perf 时会自动使用 /proc。
3. perf 与 Linux 内核工具通常需要发行版、版本、架构和内核版本匹配，请为不同目标分别制作离线包。
EOF

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$BUNDLE" && find . -type f ! -name SHA256SUMS -exec sha256sum {} \; | sort > SHA256SUMS)
else
  (cd "$BUNDLE" && find . -type f ! -name SHA256SUMS -exec shasum -a 256 {} \; | sort > SHA256SUMS)
fi

mkdir -p "$(dirname "$OUTPUT")"
tar -C "$TEMP_DIR" -czf "$OUTPUT" "$(basename "$BUNDLE")"
echo "$OUTPUT"
