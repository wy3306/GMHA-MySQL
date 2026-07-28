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

payload_index="$stage/package-payloads.txt"
package_list="$stage/dependency-packages.txt"
: > "$payload_index"
find "$stage/toolkit/packages" -type f \
  \( -name '*.deb' -o -name '*.rpm' -o -name '*.apk' -o -name '*.pkg.tar.zst' -o -name '*.pkg.tar.xz' \) \
  -print > "$package_list"
while IFS= read -r package; do
  [ -s "$package" ] || { echo "empty dependency package: $package" >&2; exit 1; }
  case "$package" in
    *.deb)
      command -v dpkg-deb >/dev/null 2>&1 || {
        echo "dpkg-deb is required to validate Debian/Ubuntu dependency payloads" >&2
        exit 1
      }
      dpkg-deb -c "$package" >> "$payload_index"
      ;;
    *.rpm)
      command -v rpm >/dev/null 2>&1 || {
        echo "rpm is required to validate RPM dependency payloads" >&2
        exit 1
      }
      rpm -qpl "$package" >> "$payload_index"
      ;;
    *.apk|*.pkg.tar.zst|*.pkg.tar.xz)
      tar -tf "$package" >> "$payload_index"
      ;;
  esac
done < "$package_list"

[ -s "$payload_index" ] || {
  echo "dependency directory contains no readable native package payloads: $dependency_dir" >&2
  exit 1
}
missing_modules=""

check_module_payload() {
  module_path=$1
  module_name=$2
  if find "$stage/toolkit/vendor/perl5" "$stage/toolkit/lib/perl5" "$stage/toolkit/lib" \
    -type f -path "*/$module_path" -print 2>/dev/null | grep -q .; then
    return
  fi
  if grep -E "/$module_path([[:space:]]*)$" "$payload_index" >/dev/null 2>&1; then
    return
  fi
  missing_modules="$missing_modules $module_name"
}

check_module_payload 'DBI.pm' 'DBI'
check_module_payload 'DBD/mysql.pm' 'DBD::mysql'
check_module_payload 'IO/Socket/SSL.pm' 'IO::Socket::SSL'
check_module_payload 'Term/ReadKey.pm' 'Term::ReadKey'

if [ -n "$missing_modules" ]; then
  echo "offline PT bundle is missing payloads for Perl modules:$missing_modules" >&2
  echo "add matching native packages and all of their dependencies under: $dependency_dir" >&2
  echo "or bundle the modules under toolkit/vendor/perl5 in the source archive" >&2
  exit 1
fi

tar -czf "$output_archive" -C "$stage" toolkit
echo "offline PT bundle created: $output_archive"
