#!/usr/bin/env sh
set -eu

PACKAGE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

exec "$PACKAGE_DIR/gmha-web" \
  --listen "${GMHA_LAUNCHER_LISTEN:-0.0.0.0:8079}" \
  --manager-url "${GMHA_MANAGER_URL:-auto}" \
  "$@"
