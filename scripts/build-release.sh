#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-V0.2.2}
EMBED_VERSION=$(printf '%s' "$VERSION" | sed 's/^v/V/')
NAME="gmha-${VERSION}-linux-amd64"
PACKAGE="$ROOT/dist/$NAME"
ARCHIVE="$ROOT/dist/$NAME.tar.gz"
MANAGER_PACKAGE="$ROOT/dist/gmha-manager-${EMBED_VERSION}-linux-amd64.bin"
AGENT_PACKAGE="$ROOT/dist/gmha-agent-${EMBED_VERSION}-linux-amd64.bin"
AGENT_ARM64_PACKAGE="$ROOT/dist/gmha-agent-${EMBED_VERSION}-linux-arm64.bin"
BUNDLED_MANAGER_NAME="gmha-manager-${EMBED_VERSION}-linux-amd64.bin"
BUNDLED_AGENT_NAME="gmha-agent-${EMBED_VERSION}-linux-amd64.bin"
BUNDLED_AGENT_ARM64_NAME="gmha-agent-${EMBED_VERSION}-linux-arm64.bin"

checksum_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

cd "$ROOT/internal/interface/http/frontend"
npm run build

cd "$ROOT"
rm -rf "$PACKAGE" "$ARCHIVE" "$ARCHIVE.sha256" "$MANAGER_PACKAGE" "$AGENT_PACKAGE" "$AGENT_ARM64_PACKAGE"
mkdir -p "$PACKAGE/bin" "$PACKAGE/data" "$PACKAGE/logs" "$PACKAGE/scripts" "$PACKAGE/docs" \
  "$PACKAGE/software/gmha-manager" "$PACKAGE/software/gmha-agent"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X gmha/internal/buildinfo.Version=$EMBED_VERSION" -o "$PACKAGE/gmha" ./cmd/gmha
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$PACKAGE/gmha-web" ./cmd/gmha-web
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X gmha/internal/buildinfo.Version=$EMBED_VERSION" -o "$PACKAGE/bin/agentd-linux-amd64" ./cmd/agent
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w -X gmha/internal/buildinfo.Version=$EMBED_VERSION" -o "$PACKAGE/bin/agentd-linux-arm64" ./cmd/agent
cp "$PACKAGE/bin/agentd-linux-amd64" "$PACKAGE/bin/agentd"

cp "$ROOT/packaging/start-web.sh" "$PACKAGE/start-web.sh"
cp "$ROOT/scripts/build-pt-offline-bundle.sh" "$PACKAGE/scripts/build-pt-offline-bundle.sh"
cp "$ROOT/scripts/build-flamegraph-offline-bundle.sh" "$PACKAGE/scripts/build-flamegraph-offline-bundle.sh"
cp "$ROOT/packaging/flamegraph-install-offline.sh" "$PACKAGE/scripts/flamegraph-install-offline.sh"
cp "$ROOT/packaging/README-linux.md" "$PACKAGE/README.md"
cp "$ROOT/docs/linux-compatibility.md" "$PACKAGE/docs/linux-compatibility.md"
chmod +x "$PACKAGE/start-web.sh" "$PACKAGE/scripts/build-pt-offline-bundle.sh" "$PACKAGE/scripts/build-flamegraph-offline-bundle.sh" "$PACKAGE/scripts/flamegraph-install-offline.sh" "$PACKAGE/gmha" "$PACKAGE/gmha-web" "$PACKAGE/bin/agentd"
cp "$PACKAGE/gmha" "$PACKAGE/software/gmha-manager/$BUNDLED_MANAGER_NAME"
cp "$PACKAGE/bin/agentd" "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_NAME"
cp "$PACKAGE/bin/agentd-linux-arm64" "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_ARM64_NAME"
chmod +x "$PACKAGE/software/gmha-manager/$BUNDLED_MANAGER_NAME" \
  "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_NAME" \
  "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_ARM64_NAME"

MANAGER_SHA256=$(checksum_file "$PACKAGE/software/gmha-manager/$BUNDLED_MANAGER_NAME")
AGENT_SHA256=$(checksum_file "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_NAME")
AGENT_ARM64_SHA256=$(checksum_file "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_ARM64_NAME")
cat > "$PACKAGE/software/.gmha-package-index.json" <<EOF
{
  "items": {
    "gmha-manager/$BUNDLED_MANAGER_NAME": {
      "arch": "x86_64",
      "version": "$EMBED_VERSION",
      "sha256": "$MANAGER_SHA256",
      "description": "GMHA $EMBED_VERSION 发行包默认内置的 Manager 当前版本制品"
    },
    "gmha-agent/$BUNDLED_AGENT_NAME": {
      "arch": "x86_64",
      "version": "$EMBED_VERSION",
      "sha256": "$AGENT_SHA256",
      "description": "GMHA $EMBED_VERSION 发行包默认内置的 Agent x86_64 当前版本制品"
    },
    "gmha-agent/$BUNDLED_AGENT_ARM64_NAME": {
      "arch": "aarch64",
      "version": "$EMBED_VERSION",
      "sha256": "$AGENT_ARM64_SHA256",
      "description": "GMHA $EMBED_VERSION 发行包默认内置的 Agent ARM64 当前版本制品"
    }
  }
}
EOF

cp "$PACKAGE/software/gmha-manager/$BUNDLED_MANAGER_NAME" "$MANAGER_PACKAGE"
cp "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_NAME" "$AGENT_PACKAGE"
cp "$PACKAGE/software/gmha-agent/$BUNDLED_AGENT_ARM64_NAME" "$AGENT_ARM64_PACKAGE"
touch "$PACKAGE/data/.keep" "$PACKAGE/logs/.keep"

tar -C "$ROOT/dist" -czf "$ARCHIVE" "$NAME"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$ROOT/dist" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
  (cd "$ROOT/dist" && sha256sum "$(basename "$MANAGER_PACKAGE")" > "$(basename "$MANAGER_PACKAGE").sha256")
  (cd "$ROOT/dist" && sha256sum "$(basename "$AGENT_PACKAGE")" > "$(basename "$AGENT_PACKAGE").sha256")
  (cd "$ROOT/dist" && sha256sum "$(basename "$AGENT_ARM64_PACKAGE")" > "$(basename "$AGENT_ARM64_PACKAGE").sha256")
else
  (cd "$ROOT/dist" && shasum -a 256 "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
  (cd "$ROOT/dist" && shasum -a 256 "$(basename "$MANAGER_PACKAGE")" > "$(basename "$MANAGER_PACKAGE").sha256")
  (cd "$ROOT/dist" && shasum -a 256 "$(basename "$AGENT_PACKAGE")" > "$(basename "$AGENT_PACKAGE").sha256")
  (cd "$ROOT/dist" && shasum -a 256 "$(basename "$AGENT_ARM64_PACKAGE")" > "$(basename "$AGENT_ARM64_PACKAGE").sha256")
fi

echo "$ARCHIVE"
echo "$ARCHIVE.sha256"
echo "$MANAGER_PACKAGE"
echo "$AGENT_PACKAGE"
echo "$AGENT_ARM64_PACKAGE"
