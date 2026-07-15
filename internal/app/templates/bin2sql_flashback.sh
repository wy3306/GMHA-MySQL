#!/usr/bin/env bash
set -Eeuo pipefail

PORT="3306"; SOCKET=""; DB_USER="root"; PASSWORD_B64=""; RESTORE_TIME=""; OUTPUT_DIR="/data/gmha/recovery"
DATABASE=""; TABLES=""; APPLY="false"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2;;
    --socket) SOCKET="$2"; shift 2;;
    --user) DB_USER="$2"; shift 2;;
    --password-base64) PASSWORD_B64="$2"; shift 2;;
    --restore-time) RESTORE_TIME="$2"; shift 2;;
    --output-dir) OUTPUT_DIR="$2"; shift 2;;
    --database) DATABASE="$2"; shift 2;;
    --tables) TABLES="$2"; shift 2;;
    --apply) APPLY="$2"; shift 2;;
    *) echo "[gmha-flashback][ERROR] unknown argument: $1" >&2; exit 2;;
  esac
done

[[ -n "$RESTORE_TIME" ]] || { echo "[gmha-flashback][ERROR] restore time is required" >&2; exit 2; }
[[ "$OUTPUT_DIR" = /* ]] || { echo "[gmha-flashback][ERROR] output directory must be absolute" >&2; exit 2; }
command -v mysql >/dev/null 2>&1 || { echo "[gmha-flashback][ERROR] mysql client is required" >&2; exit 127; }
if command -v bin2sql >/dev/null 2>&1; then BIN2SQL=(bin2sql)
elif command -v bin2sql.py >/dev/null 2>&1; then BIN2SQL=(python3 "$(command -v bin2sql.py)")
elif [[ -f /opt/bin2sql/bin2sql.py ]]; then BIN2SQL=(python3 /opt/bin2sql/bin2sql.py)
else echo "[gmha-flashback][ERROR] bin2sql is not installed" >&2; exit 127; fi

MYSQL_PWD="$(printf '%s' "$PASSWORD_B64" | base64 -d)"; export MYSQL_PWD
mysql_args=(--batch --skip-column-names "--user=$DB_USER" "--port=$PORT")
host_args=(-h 127.0.0.1 -P "$PORT" -u "$DB_USER" -p "$MYSQL_PWD")
if [[ -n "$SOCKET" ]]; then mysql_args+=("--socket=$SOCKET"); fi
format="$(mysql "${mysql_args[@]}" -e "select @@global.binlog_format")"
row_image="$(mysql "${mysql_args[@]}" -e "select @@global.binlog_row_image")"
[[ "$format" == "ROW" ]] || { echo "[gmha-flashback][ERROR] bin2sql flashback requires binlog_format=ROW; current=$format" >&2; exit 1; }
[[ "$row_image" == "FULL" ]] || { echo "[gmha-flashback][ERROR] bin2sql flashback requires binlog_row_image=FULL; current=$row_image" >&2; exit 1; }

mkdir -p "$OUTPUT_DIR"; stamp="$(date +%Y%m%d_%H%M%S)"; output="$OUTPUT_DIR/flashback_${PORT}_${stamp}.sql"
stop_time="$(date '+%Y-%m-%d %H:%M:%S')"
args=("${host_args[@]}" --start-datetime "$RESTORE_TIME" --stop-datetime "$stop_time" -B)
[[ -n "$DATABASE" ]] && args+=(-d "$DATABASE")
if [[ -n "$TABLES" ]]; then IFS=',' read -r -a table_items <<< "$TABLES"; args+=(-t "${table_items[@]}"); fi
echo "[gmha-flashback][WARN] generating reverse SQL with bin2sql for ${RESTORE_TIME} -> ${stop_time}"
"${BIN2SQL[@]}" "${args[@]}" > "$output"
[[ -s "$output" ]] || { echo "[gmha-flashback][ERROR] bin2sql generated an empty rollback file" >&2; exit 1; }
echo "[gmha-flashback][SUCCESS] rollback SQL generated: $output"
if [[ "$APPLY" == "true" ]]; then
  echo "[gmha-flashback][WARN] applying rollback SQL online; concurrent writes can cause conflicts"
  mysql "${mysql_args[@]}" < "$output"
  echo "[gmha-flashback][SUCCESS] data flashback SQL applied"
else
  echo "[gmha-flashback][INFO] preview mode: SQL was generated but not executed"
fi
unset MYSQL_PWD
