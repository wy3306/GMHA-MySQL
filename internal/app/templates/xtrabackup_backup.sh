#!/usr/bin/env bash
set -Eeuo pipefail

# GMHA XtraBackup 通用模板：调度由 Manager 负责，本脚本只执行一次带重试的备份任务。
TARGET_DIR=""; BACKUP_TYPE="full"; INCREMENTAL_BASEDIR=""; DISK_USAGE_THRESHOLD="95"
REPLICATION_LAG_WAIT="30"; PORT="3306"; SOCKET=""; MYSQL_USER="backup"; PASSWORD_B64=""
RETRY_COUNT="5"; RETRY_INTERVAL="60"; INCLUDE_BINLOG="false"; BINLOG_DIR=""; XTRABACKUP_BIN="xtrabackup"
DEFAULTS_FILE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-dir) TARGET_DIR="$2"; shift 2;;
    --backup-type) BACKUP_TYPE="$2"; shift 2;;
    --incremental-basedir) INCREMENTAL_BASEDIR="$2"; shift 2;;
    --disk-usage-threshold) DISK_USAGE_THRESHOLD="$2"; shift 2;;
    --replication-lag-wait) REPLICATION_LAG_WAIT="$2"; shift 2;;
    --port) PORT="$2"; shift 2;;
    --socket) SOCKET="$2"; shift 2;;
    --user) MYSQL_USER="$2"; shift 2;;
    --password-base64) PASSWORD_B64="$2"; shift 2;;
    --retry-count) RETRY_COUNT="$2"; shift 2;;
    --retry-interval) RETRY_INTERVAL="$2"; shift 2;;
    --include-binlog) INCLUDE_BINLOG="$2"; shift 2;;
    --binlog-dir) BINLOG_DIR="$2"; shift 2;;
    --defaults-file) DEFAULTS_FILE="$2"; shift 2;;
    --xtrabackup) XTRABACKUP_BIN="$2"; shift 2;;
    *) echo "[gmha-backup][ERROR] unknown argument: $1" >&2; exit 2;;
  esac
done

[[ -n "$TARGET_DIR" && "$TARGET_DIR" = /* && "$TARGET_DIR" != "/" ]] || { echo "[gmha-backup][ERROR] safe absolute --target-dir is required" >&2; exit 2; }
[[ "$BACKUP_TYPE" == "full" || "$BACKUP_TYPE" == "incremental" ]] || { echo "[gmha-backup][ERROR] backup type must be full or incremental" >&2; exit 2; }
[[ -z "$DEFAULTS_FILE" || -f "$DEFAULTS_FILE" ]] || { echo "[gmha-backup][ERROR] MySQL defaults file does not exist: $DEFAULTS_FILE" >&2; exit 2; }
if [[ "$BACKUP_TYPE" == "incremental" ]]; then
  [[ -f "$INCREMENTAL_BASEDIR/.gmha-backup-complete" ]] || { echo "[gmha-backup][ERROR] incremental base backup is incomplete: $INCREMENTAL_BASEDIR" >&2; exit 2; }
fi
command -v "$XTRABACKUP_BIN" >/dev/null 2>&1 || { echo "[gmha-backup][ERROR] xtrabackup is not installed: $XTRABACKUP_BIN" >&2; exit 127; }
command -v mysql >/dev/null 2>&1 || { echo "[gmha-backup][ERROR] mysql client is required for replication lag precheck" >&2; exit 127; }
mkdir -p "$(dirname "$TARGET_DIR")"

# 磁盘使用率达到阈值时直接失败，不进入重试，避免重试继续耗尽磁盘。
used_percent="$(df -P "$(dirname "$TARGET_DIR")" | awk 'NR==2 {gsub(/%/,"",$5); print $5}')"
[[ "$used_percent" =~ ^[0-9]+$ ]] || { echo "[gmha-backup][ERROR] unable to read backup disk usage" >&2; exit 1; }
echo "[gmha-backup][INFO] disk precheck: used=${used_percent}%, threshold=${DISK_USAGE_THRESHOLD}%"
if (( used_percent >= DISK_USAGE_THRESHOLD )); then
  echo "[gmha-backup][ERROR] backup disk usage ${used_percent}% reached threshold ${DISK_USAGE_THRESHOLD}%; backup rejected" >&2
  exit 1
fi

LOCK_FILE="$(dirname "$TARGET_DIR")/.gmha-xtrabackup-${PORT}.lock"
exec 9>"$LOCK_FILE"; flock -n 9 || { echo "[gmha-backup][ERROR] another backup for port ${PORT} is running" >&2; exit 75; }
MYSQL_PWD="$(printf '%s' "$PASSWORD_B64" | base64 -d)"
AUTH_FILE="$(mktemp "/tmp/gmha-xtrabackup-auth-${PORT}.XXXXXX.cnf")"
chmod 600 "$AUTH_FILE"
escaped_user="${MYSQL_USER//\\/\\\\}"; escaped_user="${escaped_user//\"/\\\"}"
escaped_password="${MYSQL_PWD//\\/\\\\}"; escaped_password="${escaped_password//\"/\\\"}"
{
  [[ -n "$DEFAULTS_FILE" ]] && printf '!include %s\n' "$DEFAULTS_FILE"
  printf '[client]\nuser="%s"\npassword="%s"\n' "$escaped_user" "$escaped_password"
} > "$AUTH_FILE"
trap 'rm -f "$AUTH_FILE"; unset MYSQL_PWD' EXIT

mysql_args=("--defaults-file=$AUTH_FILE" --batch --skip-column-names "--port=$PORT")
if [[ -n "$SOCKET" ]]; then mysql_args+=("--socket=$SOCKET"); else mysql_args+=(--host=127.0.0.1); fi

# XtraBackup releases are tied to a MySQL release series. Refuse a mismatched
# binary before deleting/recreating the target directory so a wrong offline
# package cannot turn a scheduled backup into an unusable artifact.
mysql_version="$(mysql "${mysql_args[@]}" -e 'SELECT VERSION()' | head -n1 | tr -d '[:space:]')"
[[ "$mysql_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || { echo "[gmha-backup][ERROR] unable to determine MySQL server version: $mysql_version" >&2; exit 1; }
mysql_series="$(printf '%s' "$mysql_version" | awk -F. '{print $1"."$2}')"
case "$mysql_series" in
  5.7) required_xtrabackup_series="2.4";;
  8.*|9.*) required_xtrabackup_series="$mysql_series";;
  *) echo "[gmha-backup][ERROR] unsupported MySQL series: $mysql_series" >&2; exit 1;;
esac
xtrabackup_version_output="$("$XTRABACKUP_BIN" --version 2>&1 || true)"
xtrabackup_version="$(printf '%s\n' "$xtrabackup_version_output" | sed -nE 's/.*xtrabackup version ([0-9]+\.[0-9]+\.[0-9]+[^ ]*).*/\1/p' | head -n1)"
escaped_required_series="${required_xtrabackup_series//./\\.}"
printf '%s\n' "$xtrabackup_version_output" | grep -Eq "xtrabackup version ${escaped_required_series}([.-]|[[:space:]]|$)" || {
  echo "[gmha-backup][ERROR] installed XtraBackup does not match MySQL $mysql_version; required XtraBackup $required_xtrabackup_series.x" >&2
  exit 1
}
echo "[gmha-backup][INFO] compatibility precheck: MySQL ${mysql_version}, XtraBackup ${xtrabackup_version:-unknown}"

replication_lag() {
  local status lag
  status="$(mysql "${mysql_args[@]}" -e 'SHOW REPLICA STATUS\G' 2>/dev/null || mysql "${mysql_args[@]}" -e 'SHOW SLAVE STATUS\G' 2>/dev/null || true)"
  [[ -z "$status" ]] && { echo "primary"; return 0; }
  lag="$(printf '%s\n' "$status" | awk -F': ' '/Seconds_Behind_(Source|Master):/{print $2; exit}' | tr -d '[:space:]')"
  echo "${lag:-unknown}"
}

wait_replication_zero() {
  local elapsed=0 lag
  while (( elapsed <= REPLICATION_LAG_WAIT )); do
    lag="$(replication_lag)"
    if [[ "$lag" == "primary" ]]; then echo "[gmha-backup][INFO] selected instance is primary/standalone; replica lag check skipped"; return 0; fi
    if [[ "$lag" == "0" ]]; then echo "[gmha-backup][INFO] replication lag reached 0 seconds"; return 0; fi
    echo "[gmha-backup][WARN] replication lag=${lag}; waiting ${elapsed}/${REPLICATION_LAG_WAIT}s"
    sleep 2; elapsed=$((elapsed + 2))
  done
  echo "[gmha-backup][ERROR] replication lag did not reach 0 within ${REPLICATION_LAG_WAIT} seconds" >&2
  return 1
}

attempt=0
while (( attempt <= RETRY_COUNT )); do
  echo "[gmha-backup][INFO] attempt $((attempt + 1))/$((RETRY_COUNT + 1)); type=${BACKUP_TYPE}; target=${TARGET_DIR}"
  if wait_replication_zero; then
    rm -rf "$TARGET_DIR"; mkdir -p "$TARGET_DIR"
    # Defaults-file options must be the first XtraBackup argument. A temporary
    # 0600 file avoids exposing the decoded password in process arguments.
    xb_args=("--defaults-file=$AUTH_FILE" --backup "--target-dir=$TARGET_DIR" "--port=$PORT")
    [[ -n "$SOCKET" ]] && xb_args+=("--socket=$SOCKET")
    [[ "$BACKUP_TYPE" == "incremental" ]] && xb_args+=("--incremental-basedir=$INCREMENTAL_BASEDIR")
    if "$XTRABACKUP_BIN" "${xb_args[@]}"; then break; fi
    echo "[gmha-backup][ERROR] xtrabackup command failed on attempt $((attempt + 1))" >&2
  fi
  if (( attempt >= RETRY_COUNT )); then echo "[gmha-backup][ERROR] $((RETRY_COUNT + 1)) attempts exhausted; today's backup is marked failed" >&2; exit 1; fi
  attempt=$((attempt + 1)); echo "[gmha-backup][WARN] retrying in ${RETRY_INTERVAL}s"; sleep "$RETRY_INTERVAL"
done

if [[ "$INCLUDE_BINLOG" == "true" ]]; then
  [[ -n "$BINLOG_DIR" && -d "$BINLOG_DIR" ]] || { echo "[gmha-backup][ERROR] binlog directory does not exist: $BINLOG_DIR" >&2; exit 1; }
  mkdir -p "$TARGET_DIR/gmha-binlog"
  if command -v rsync >/dev/null 2>&1; then rsync -a "$BINLOG_DIR/" "$TARGET_DIR/gmha-binlog/"; else cp -a "$BINLOG_DIR/." "$TARGET_DIR/gmha-binlog/"; fi
  echo "[gmha-backup][INFO] binlog directory copied"
fi
printf 'created_at=%s\nport=%s\nbackup_type=%s\nbase_dir=%s\ninclude_binlog=%s\nmysql_version=%s\nxtrabackup_version=%s\nxtrabackup_series=%s\n' "$(date -u +%FT%TZ)" "$PORT" "$BACKUP_TYPE" "$INCREMENTAL_BASEDIR" "$INCLUDE_BINLOG" "$mysql_version" "$xtrabackup_version" "$required_xtrabackup_series" > "$TARGET_DIR/gmha-backup.meta"
touch "$TARGET_DIR/.gmha-backup-complete"
echo "[gmha-backup][SUCCESS] backup completed: $TARGET_DIR"
