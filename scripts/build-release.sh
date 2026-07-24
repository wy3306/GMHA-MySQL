#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-V0.0.3}
EMBED_VERSION=$(printf '%s' "$VERSION" | sed 's/^v/V/')
NAME="gmha-${VERSION}-linux-amd64"
PACKAGE="$ROOT/dist/$NAME"
ARCHIVE="$ROOT/dist/$NAME.tar.gz"
MANAGER_PACKAGE="$ROOT/dist/gmha-manager-${EMBED_VERSION}-linux-amd64.bin"
AGENT_PACKAGE="$ROOT/dist/gmha-agent-${EMBED_VERSION}-linux-amd64.bin"

cd "$ROOT/internal/interface/http/frontend"
npm run build

cd "$ROOT"
rm -rf "$PACKAGE" "$ARCHIVE" "$ARCHIVE.sha256" "$MANAGER_PACKAGE" "$AGENT_PACKAGE"
mkdir -p "$PACKAGE/bin" "$PACKAGE/data" "$PACKAGE/logs" "$PACKAGE/scripts"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X gmha/internal/buildinfo.Version=$EMBED_VERSION" -o "$PACKAGE/gmha" ./cmd/gmha
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$PACKAGE/gmha-web" ./cmd/gmha-web
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X gmha/internal/buildinfo.Version=$EMBED_VERSION" -o "$PACKAGE/bin/agentd" ./cmd/agent

cp "$ROOT/packaging/start-web.sh" "$PACKAGE/start-web.sh"
cp "$ROOT/scripts/build-pt-offline-bundle.sh" "$PACKAGE/scripts/build-pt-offline-bundle.sh"
cp "$ROOT/scripts/build-flamegraph-offline-bundle.sh" "$PACKAGE/scripts/build-flamegraph-offline-bundle.sh"
cp "$ROOT/packaging/flamegraph-install-offline.sh" "$PACKAGE/scripts/flamegraph-install-offline.sh"
cp "$ROOT/packaging/README-linux.md" "$PACKAGE/README.md"
chmod +x "$PACKAGE/start-web.sh" "$PACKAGE/scripts/build-pt-offline-bundle.sh" "$PACKAGE/scripts/build-flamegraph-offline-bundle.sh" "$PACKAGE/scripts/flamegraph-install-offline.sh" "$PACKAGE/gmha" "$PACKAGE/gmha-web" "$PACKAGE/bin/agentd"
cp "$PACKAGE/gmha" "$MANAGER_PACKAGE"
cp "$PACKAGE/bin/agentd" "$AGENT_PACKAGE"
touch "$PACKAGE/data/.keep" "$PACKAGE/logs/.keep"

tar -C "$ROOT/dist" -czf "$ARCHIVE" "$NAME"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$ROOT/dist" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
  (cd "$ROOT/dist" && sha256sum "$(basename "$MANAGER_PACKAGE")" > "$(basename "$MANAGER_PACKAGE").sha256")
  (cd "$ROOT/dist" && sha256sum "$(basename "$AGENT_PACKAGE")" > "$(basename "$AGENT_PACKAGE").sha256")
else
  (cd "$ROOT/dist" && shasum -a 256 "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
  (cd "$ROOT/dist" && shasum -a 256 "$(basename "$MANAGER_PACKAGE")" > "$(basename "$MANAGER_PACKAGE").sha256")
  (cd "$ROOT/dist" && shasum -a 256 "$(basename "$AGENT_PACKAGE")" > "$(basename "$AGENT_PACKAGE").sha256")
fi

echo "$ARCHIVE"
echo "$ARCHIVE.sha256"
echo "$MANAGER_PACKAGE"
echo "$AGENT_PACKAGE"
