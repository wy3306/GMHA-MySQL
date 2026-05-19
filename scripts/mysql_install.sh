set -e

MYSQL_VERSION="${MYSQL_VERSION:-__MYSQL_VERSION__}"
REQUIRED_GLIBC="${REQUIRED_GLIBC:-__REQUIRED_GLIBC__}"
INSTALL_ROOT="${INSTALL_ROOT:-__INSTALL_ROOT__}"
BASE_DIR="${BASE_DIR:-__BASE_DIR__}"
DATA_ROOT="${DATA_ROOT:-__DATA_ROOT__}"
MYSQL_PORT="${MYSQL_PORT:-__MYSQL_PORT__}"
MYSQL_USER="${MYSQL_USER:-__MYSQL_USER__}"
PACKAGE_PATH="${PACKAGE_PATH:-__PACKAGE_PATH__}"
PACKAGE_URL="${PACKAGE_URL:-__PACKAGE_URL__}"
ROOT_PASSWORD="${ROOT_PASSWORD:-__ROOT_PASSWORD__}"
SERVER_ID="${SERVER_ID:-__SERVER_ID__}"
DISABLE_FIREWALL="${DISABLE_FIREWALL:-__DISABLE_FIREWALL__}"
DISABLE_SELINUX="${DISABLE_SELINUX:-__DISABLE_SELINUX__}"
PACKAGE_DIR_NAME="${PACKAGE_DIR_NAME:-__PACKAGE_DIR_NAME__}"
TARGET_OS="${TARGET_OS:-}"
CURRENT_OS=""
CURRENT_GLIBC=""
ARCH=""

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "mysql install requires root" >&2
    exit 1
  fi
}

detect_host_info() {
  ARCH="$(uname -m)"
  if [ -f /etc/os-release ]; then
    . /etc/os-release
    CURRENT_OS="${PRETTY_NAME:-$NAME}"
    TARGET_OS="${TARGET_OS:-${ID:-unknown}}"
    OS_FAMILY="${ID_LIKE:-${ID:-}}"
  else
    CURRENT_OS="unknown"
    TARGET_OS="${TARGET_OS:-unknown}"
    OS_FAMILY=""
  fi
  CURRENT_GLIBC="$(ldd --version 2>/dev/null | head -n1 | grep -Eo '[0-9]+\.[0-9]+' | tail -n1)"
  if [ -z "$CURRENT_GLIBC" ]; then
    echo "unable to detect glibc version" >&2
    exit 1
  fi
  if [ -z "$REQUIRED_GLIBC" ]; then
    REQUIRED_GLIBC="$CURRENT_GLIBC"
  fi
}

prepare_runtime_defaults() {
  if [ -z "$PACKAGE_DIR_NAME" ]; then
    PACKAGE_DIR_NAME="$(basename "$PACKAGE_PATH")"
    PACKAGE_DIR_NAME="${PACKAGE_DIR_NAME%%.tar.xz}"
  fi
}

disable_platform_guards() {
  if [ "$DISABLE_FIREWALL" = "1" ]; then
    systemctl stop firewalld >/dev/null 2>&1 || true
    systemctl disable firewalld >/dev/null 2>&1 || true
  fi
  if [ "$DISABLE_SELINUX" = "1" ]; then
    setenforce 0 >/dev/null 2>&1 || true
    if [ -f /etc/selinux/config ]; then
      sed -i 's/^SELINUX=.*/SELINUX=disabled/' /etc/selinux/config
    fi
  fi
}

install_dependencies() {
  if printf '%s' "$OS_FAMILY $TARGET_OS" | grep -Eqi 'debian|ubuntu'; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y libaio1 libnuma1 xz-utils curl
    return
  fi
  if printf '%s' "$OS_FAMILY $TARGET_OS" | grep -Eqi 'rhel|fedora|centos|rocky|almalinux'; then
    if rpm -qa | grep -qi '^mariadb'; then
      rpm -qa | grep mariadb | xargs -r yum remove -y || true
    fi
    if command -v dnf >/dev/null 2>&1; then
      dnf install -y libaio libaio-devel numactl-libs xz curl
    else
      yum install -y libaio libaio-devel numactl-libs xz curl
    fi
    return
  fi
  echo "unsupported linux: $CURRENT_OS" >&2
  exit 1
}

fetch_package() {
  if [ -f "$PACKAGE_PATH" ]; then
    return
  fi
  if [ -n "$PACKAGE_URL" ]; then
    mkdir -p "$(dirname "$PACKAGE_PATH")"
    curl -L -o "$PACKAGE_PATH" "$PACKAGE_URL"
    return
  fi
  echo "mysql package not found: $PACKAGE_PATH" >&2
  exit 1
}

ensure_mysql_user() {
  id -u "$MYSQL_USER" >/dev/null 2>&1 || useradd "$MYSQL_USER" -M -s /sbin/nologin
}

prepare_directories() {
  mkdir -p "$DATA_ROOT" "$DATA_ROOT/binlog" "$DATA_ROOT/data" "$DATA_ROOT/redo/$MYSQL_PORT" "$DATA_ROOT/undo/$MYSQL_PORT" "$DATA_ROOT/tmp"
  mkdir -p "$INSTALL_ROOT"
}

extract_mysql_package() {
  if [ ! -d "$INSTALL_ROOT/$PACKAGE_DIR_NAME" ]; then
    tar -xf "$PACKAGE_PATH" -C "$INSTALL_ROOT"
  fi
  ln -sfn "$INSTALL_ROOT/$PACKAGE_DIR_NAME" "$BASE_DIR"
  chown -R "$MYSQL_USER:$MYSQL_USER" "$DATA_ROOT" "$INSTALL_ROOT/$PACKAGE_DIR_NAME"
}

write_mycnf() {
__WRITE_MYCNF__
}

write_service_file() {
__WRITE_SERVICE_FILE__
}

update_profile_path() {
  grep -q "$BASE_DIR/bin" /etc/profile || echo 'export PATH="$PATH:'"$BASE_DIR"'/bin"' >> /etc/profile
}

initialize_mysql() {
  if [ ! -d "$DATA_ROOT/data/mysql" ]; then
    "$BASE_DIR/bin/mysqld" --initialize-insecure --user="$MYSQL_USER" --datadir="$DATA_ROOT/data" --basedir="$BASE_DIR"
  fi
}

start_mysql_service() {
  systemctl daemon-reload
  systemctl enable mysql
  systemctl restart mysql
}

set_root_password() {
  if [ -n "$ROOT_PASSWORD" ]; then
    "$BASE_DIR/bin/mysql" --socket="$DATA_ROOT/data/mysql.sock" -uroot -e __ALTER_ROOT_SQL__
  fi
}

verify_mysql() {
  "$BASE_DIR/bin/mysqladmin" --socket="$DATA_ROOT/data/mysql.sock" ping
}

main() {
  require_root
  detect_host_info
  prepare_runtime_defaults
  disable_platform_guards
  install_dependencies
  fetch_package
  ensure_mysql_user
  prepare_directories
  extract_mysql_package
  write_mycnf
  write_service_file
  update_profile_path
  initialize_mysql
  start_mysql_service
  set_root_password
  verify_mysql
}

main "$@"
