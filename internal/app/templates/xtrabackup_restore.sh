#!/usr/bin/env bash
set -Eeuo pipefail

FULL_DIR=""; INCREMENTAL_DIRS=(); DATA_DIR=""; MYSQL_OS_USER="mysql"; SYSTEMD_UNIT=""; XTRABACKUP_BIN="xtrabackup"
RECOVERY_MODE="physical"; RESTORE_TIME=""; BINLOG_DIR=""; PORT="3306"; SOCKET=""; DB_USER="root"; DB_PASSWORD_B64=""; REPAIR_REPLICATION="false"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --full-dir) FULL_DIR="$2"; shift 2;;
    --incremental-dir) INCREMENTAL_DIRS+=("$2"); shift 2;;
    --recovery-mode) RECOVERY_MODE="$2"; shift 2;;
    --restore-time) RESTORE_TIME="$2"; shift 2;;
    --binlog-dir) BINLOG_DIR="$2"; shift 2;;
    --port) PORT="$2"; shift 2;;
    --socket) SOCKET="$2"; shift 2;;
    --db-user) DB_USER="$2"; shift 2;;
    --db-password-base64) DB_PASSWORD_B64="$2"; shift 2;;
    --repair-replication) REPAIR_REPLICATION="$2"; shift 2;;
    --data-dir) DATA_DIR="$2"; shift 2;;
    --mysql-os-user) MYSQL_OS_USER="$2"; shift 2;;
    --systemd-unit) SYSTEMD_UNIT="$2"; shift 2;;
    --xtrabackup) XTRABACKUP_BIN="$2"; shift 2;;
    *) echo "[gmha-restore][ERROR] unknown argument: $1" >&2; exit 2;;
  esac
done

[[ -f "$FULL_DIR/.gmha-backup-complete" ]] || { echo "[gmha-restore][ERROR] full backup marker is missing" >&2; exit 2; }
for dir in "${INCREMENTAL_DIRS[@]}"; do [[ -f "$dir/.gmha-backup-complete" ]] || { echo "[gmha-restore][ERROR] incremental backup marker is missing: $dir" >&2; exit 2; }; done
[[ -n "$DATA_DIR" && "$DATA_DIR" = /* ]] || { echo "[gmha-restore][ERROR] valid absolute --data-dir is required" >&2; exit 2; }
[[ -n "$SYSTEMD_UNIT" ]] || { echo "[gmha-restore][ERROR] --systemd-unit is required" >&2; exit 2; }
command -v "$XTRABACKUP_BIN" >/dev/null 2>&1 || { echo "[gmha-restore][ERROR] xtrabackup is not installed" >&2; exit 127; }
[[ "$RECOVERY_MODE" == "physical" || "$RECOVERY_MODE" == "point_in_time" ]] || { echo "[gmha-restore][ERROR] invalid recovery mode" >&2; exit 2; }
if [[ "$RECOVERY_MODE" == "point_in_time" ]]; then
  [[ -n "$RESTORE_TIME" ]] || { echo "[gmha-restore][ERROR] restore time is required" >&2; exit 2; }
  [[ -d "$BINLOG_DIR" ]] || { echo "[gmha-restore][ERROR] binlog recovery directory does not exist: $BINLOG_DIR" >&2; exit 2; }
  command -v mysqlbinlog >/dev/null 2>&1 || { echo "[gmha-restore][ERROR] mysqlbinlog is required" >&2; exit 127; }
fi

stamp="$(date +%Y%m%d_%H%M%S)"; STAGING="$(dirname "$DATA_DIR")/.gmha_restore_${stamp}"
echo "[gmha-restore][INFO] copying full backup into restore staging: $STAGING"
mkdir -p "$STAGING"; trap 'rm -rf "$STAGING"' EXIT
if command -v rsync >/dev/null 2>&1; then rsync -a --exclude gmha-binlog "$FULL_DIR/" "$STAGING/"; else cp -a "$FULL_DIR/." "$STAGING/"; rm -rf "$STAGING/gmha-binlog"; fi

if (( ${#INCREMENTAL_DIRS[@]} == 0 )); then
  "$XTRABACKUP_BIN" --prepare "--target-dir=$STAGING"
else
  "$XTRABACKUP_BIN" --prepare --apply-log-only "--target-dir=$STAGING"
  last=$(( ${#INCREMENTAL_DIRS[@]} - 1 ))
  for i in "${!INCREMENTAL_DIRS[@]}"; do
    if (( i == last )); then "$XTRABACKUP_BIN" --prepare "--target-dir=$STAGING" "--incremental-dir=${INCREMENTAL_DIRS[$i]}"
    else "$XTRABACKUP_BIN" --prepare --apply-log-only "--target-dir=$STAGING" "--incremental-dir=${INCREMENTAL_DIRS[$i]}"; fi
  done
fi

echo "[gmha-restore][WARN] full physical restore will stop $SYSTEMD_UNIT; database access is now suspended"
systemctl stop "$SYSTEMD_UNIT"
if [[ -d "$DATA_DIR" ]]; then mv "$DATA_DIR" "${DATA_DIR}.before_restore_${stamp}"; fi
mkdir -p "$DATA_DIR"
if ! "$XTRABACKUP_BIN" --copy-back "--target-dir=$STAGING" "--datadir=$DATA_DIR"; then
  echo "[gmha-restore][ERROR] copy-back failed; rolling back original data directory" >&2
  rm -rf "$DATA_DIR"; [[ -d "${DATA_DIR}.before_restore_${stamp}" ]] && mv "${DATA_DIR}.before_restore_${stamp}" "$DATA_DIR"; exit 1
fi
chown -R "$MYSQL_OS_USER:$MYSQL_OS_USER" "$DATA_DIR"; systemctl start "$SYSTEMD_UNIT"

MYSQL_PWD="$(printf '%s' "$DB_PASSWORD_B64" | base64 -d)"; export MYSQL_PWD
mysql_args=(--batch "--user=$DB_USER" "--port=$PORT")
if [[ -n "$SOCKET" ]]; then mysql_args+=("--socket=$SOCKET"); else mysql_args+=(--host=127.0.0.1); fi
for _ in $(seq 1 30); do mysql "${mysql_args[@]}" -e 'select 1' >/dev/null 2>&1 && break; sleep 1; done
mysql "${mysql_args[@]}" -e 'select 1' >/dev/null 2>&1 || { echo "[gmha-restore][ERROR] MySQL did not become ready after restore" >&2; exit 1; }

if [[ "$RECOVERY_MODE" == "point_in_time" ]]; then
  read -r first_file first_pos _ < "$STAGING/xtrabackup_binlog_info" || { echo "[gmha-restore][ERROR] xtrabackup_binlog_info is missing" >&2; exit 1; }
  [[ -n "$first_file" && "$first_pos" =~ ^[0-9]+$ ]] || { echo "[gmha-restore][ERROR] invalid XtraBackup binlog coordinates" >&2; exit 1; }
  mapfile -t binlog_files < <(find "$BINLOG_DIR" -maxdepth 1 -type f -name '*.[0-9]*' | sort)
  first_path=""
  for file in "${binlog_files[@]}"; do [[ "$(basename "$file")" == "$first_file" ]] && first_path="$file"; done
  [[ -n "$first_path" ]] || { echo "[gmha-restore][ERROR] starting binlog $first_file not found in $BINLOG_DIR" >&2; exit 1; }
  echo "[gmha-restore][INFO] replaying binlog from ${first_file}:${first_pos} until ${RESTORE_TIME}"
  {
    mysqlbinlog "--start-position=$first_pos" "--stop-datetime=$RESTORE_TIME" "$first_path"
    passed=false
    for file in "${binlog_files[@]}"; do
      if [[ "$passed" == "true" ]]; then mysqlbinlog "--stop-datetime=$RESTORE_TIME" "$file"; fi
      [[ "$file" == "$first_path" ]] && passed=true
    done
  } | mysql "${mysql_args[@]}"
  echo "[gmha-restore][SUCCESS] point-in-time binlog replay completed"
fi

mysql "${mysql_args[@]}" -e 'START REPLICA' >/dev/null 2>&1 || mysql "${mysql_args[@]}" -e 'START SLAVE' >/dev/null 2>&1 || true
if [[ "$REPAIR_REPLICATION" == "true" ]]; then
  command -v pt-table-sync >/dev/null 2>&1 || { echo "[gmha-restore][ERROR] pt-table-sync is required for replication repair" >&2; exit 127; }
  echo "[gmha-restore][WARN] running pt-table-sync --sync-to-master to repair replica consistency"
  dsn="h=127.0.0.1,P=${PORT},u=${DB_USER},p=${MYSQL_PWD}"
  pt-table-sync --execute --sync-to-master "$dsn"
  echo "[gmha-restore][SUCCESS] pt-table-sync replication repair completed"
fi
unset MYSQL_PWD
echo "[gmha-restore][SUCCESS] restore completed; previous data retained at ${DATA_DIR}.before_restore_${stamp}"
