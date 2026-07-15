#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-v0.1.0}
NAME="gmha-${VERSION}-linux-amd64"
PACKAGE="$ROOT/dist/$NAME"
ARCHIVE="$ROOT/dist/$NAME.tar.gz"

cd "$ROOT/internal/interface/http/frontend"
npm run build

cd "$ROOT"
rm -rf "$PACKAGE" "$ARCHIVE" "$ARCHIVE.sha256"
mkdir -p "$PACKAGE/bin" "$PACKAGE/data" "$PACKAGE/logs"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$PACKAGE/gmha" ./cmd/gmha
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$PACKAGE/gmha-web" ./cmd/gmha-web
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$PACKAGE/bin/agentd" ./cmd/agent

cp "$ROOT/packaging/start-web.sh" "$PACKAGE/start-web.sh"
cp "$ROOT/packaging/README-linux.md" "$PACKAGE/README.md"
chmod +x "$PACKAGE/start-web.sh" "$PACKAGE/gmha" "$PACKAGE/gmha-web" "$PACKAGE/bin/agentd"
touch "$PACKAGE/data/.keep" "$PACKAGE/logs/.keep"

tar -C "$ROOT/dist" -czf "$ARCHIVE" "$NAME"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$ROOT/dist" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
else
  (cd "$ROOT/dist" && shasum -a 256 "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
fi

echo "$ARCHIVE"
echo "$ARCHIVE.sha256"
