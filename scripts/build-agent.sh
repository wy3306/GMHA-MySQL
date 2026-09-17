#!/usr/bin/env sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
# Uses the Manager's saved package-store path unless --storage is supplied.
exec go run ./cmd/build-agent "$@"
