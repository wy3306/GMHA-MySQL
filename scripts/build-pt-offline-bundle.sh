#!/bin/sh
set -eu

usage() {
  echo "usage: $0 <percona-toolkit.tar.gz> <dependency-package-dir> <output.tar.gz>" >&2
  echo "dependency directory may contain .deb, .rpm, .apk, or .pkg.tar.zst files" >&2
  exit 2
}

[ "$#" -eq 3 ] || usage
toolkit_archive=$1
dependency_dir=$2
output_archive=$3

[ -f "$toolkit_archive" ] || { echo "toolkit archive not found: $toolkit_archive" >&2; exit 1; }
[ -d "$dependency_dir" ] || { echo "dependency directory not found: $dependency_dir" >&2; exit 1; }

stage=$(mktemp -d "${TMPDIR:-/tmp}/gmha-pt-bundle.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM

mkdir -p "$stage/toolkit" "$stage/toolkit/packages"
tar -xzf "$toolkit_archive" -C "$stage/toolkit" --strip-components=1
cp -a "$dependency_dir"/. "$stage/toolkit/packages"/

test -f "$stage/toolkit/bin/pt-table-sync" || {
  echo "invalid Percona Toolkit source archive: bin/pt-table-sync is missing" >&2
  exit 1
}
test -f "$stage/toolkit/bin/pt-archiver" || {
  echo "invalid Percona Toolkit source archive: bin/pt-archiver is missing" >&2
  exit 1
}

tar -czf "$output_archive" -C "$stage" toolkit
echo "offline PT bundle created: $output_archive"
