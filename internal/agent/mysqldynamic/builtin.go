package mysqldynamic

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

// BuiltinCollector 是 MySQL 内置指标采集器，通过 SQL 查询收集 MySQL 状态变量和系统变量，
// 支持 30+ 种预定义指标，包括连接性、复制状态、缓冲池命中率等。
type BuiltinCollector struct {
	name string
}

// NewBuiltinCollector 创建一个指定名称的内置 MySQL 指标采集器实例。
func NewBuiltinCollector(name string) *BuiltinCollector {
	return &BuiltinCollector{name: name}
}

func (c *BuiltinCollector) Name() string {
	return c.name
}

func (c *BuiltinCollector) Category() string {
	return "mysql"
}

// Collect 执行指标采集，根据指标名称分发到对应的采集逻辑。
func (c *BuiltinCollector) Collect(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	started := time.Now()
	value, err := c.collect(ctx, env, spec)
	if err != nil {
		return metricError(spec, err, time.Since(started).Milliseconds())
	}
	return metricOK(spec, spec.Category, valueTypeFor(value), value, started)
}

func (c *BuiltinCollector) collect(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec) (any, error) {
	if env == nil {
		return nil, errors.New("mysql collect env is nil")
	}
	switch spec.Name {
	case "mysql_process_alive":
		return processAlive(ctx), nil
	case "mysql_port_listening":
		return tcpListening(ctx, env.Connect.Host, env.port(), env.timeout()), nil
	case "mysql_socket_ok":
		if strings.TrimSpace(env.Connect.Socket) == "" {
			return false, nil
		}
		_, err := os.Stat(env.Connect.Socket)
		return err == nil, nil
	case "mysql_probe", "mysql_connectivity":
		return collectConnectivity(ctx, env)
	case "mysql_uptime":
		return collectStatus(ctx, env, "Uptime")
	case "mysql_read_only":
		return collectVariableBool(ctx, env, "read_only")
	case "mysql_super_read_only":
		return collectVariableBool(ctx, env, "super_read_only")
	case "mysql_threads_running":
		return collectStatus(ctx, env, "Threads_running")
	case "mysql_tps":
		// TPS includes both committed and rolled-back transactions. The Manager
		// derives the per-second rate from this monotonic cumulative counter.
		return collectStatusSum(ctx, env, "Com_commit", "Com_rollback")
	case "mysql_replication_basic_status", "mysql_replication_thread_status":
		return collectReplicationBasic(ctx, env)
	case "mysql_replica_io_thread":
		return collectReplicaField(ctx, env, spec, "Replica_IO_Running", "Slave_IO_Running")
	case "mysql_replica_sql_thread":
		return collectReplicaField(ctx, env, spec, "Replica_SQL_Running", "Slave_SQL_Running")
	case "mysql_replication_lag", "mysql_seconds_behind_master":
		return collectReplicaField(ctx, env, spec, "Seconds_Behind_Source", "Seconds_Behind_Master")
	case "mysql_role":
		return collectRole(ctx, env)
	case "mysql_semisync_status":
		return collectSemisync(ctx, env)
	case "mysql_connection_usage_percent":
		return collectRatio(ctx, env, statusValue("Threads_connected"), variableValue("max_connections"))
	case "mysql_table_scan_ratio":
		return collectRatio(ctx, env, statusValue("Handler_read_rnd_next"), statusSum("Handler_read_first", "Handler_read_key", "Handler_read_next", "Handler_read_prev", "Handler_read_rnd", "Handler_read_rnd_next"))
	case "mysql_join_full_scan_ratio":
		return collectRatio(ctx, env, statusValue("Select_full_join"), statusSum("Select_full_join", "Select_range_check"))
	case "mysql_tmp_disk_table_ratio", "mysql_internal_tmp_disk_table_ratio", "mysql_memory_tmp_to_disk_ratio":
		return collectRatio(ctx, env, statusValue("Created_tmp_disk_tables"), statusValue("Created_tmp_tables"))
	case "mysql_memory_modules":
		return collectMemoryModules(ctx, env)
	case "mysql_binlog_cache_disk_ratio":
		return collectRatio(ctx, env, statusValue("Binlog_cache_disk_use"), statusValue("Binlog_cache_use"))
	case "mysql_buffer_pool_usage_percent":
		return collectRatio(ctx, env, statusDiff("Innodb_buffer_pool_pages_total", "Innodb_buffer_pool_pages_free"), statusValue("Innodb_buffer_pool_pages_total"))
	case "mysql_buffer_pool_dirty_ratio":
		return collectRatio(ctx, env, statusValue("Innodb_buffer_pool_pages_dirty"), statusValue("Innodb_buffer_pool_pages_total"))
	case "mysql_buffer_pool_hit_ratio":
		return collectBufferPoolHitRatio(ctx, env)
	case "mysql_open_files_usage_percent":
		return collectRatio(ctx, env, statusValue("Open_files"), variableValue("open_files_limit"))
	case "mysql_table_cache_usage_percent":
		return collectRatio(ctx, env, statusValue("Open_tables"), variableValue("table_open_cache"))
	case "mysql_table_cache_overflow_ratio":
		return collectRatio(ctx, env, statusValue("Opened_tables"), statusSum("Open_tables", "Opened_tables"))
	case "mysql_thread_cache_hit_ratio":
		return collectThreadCacheHitRatio(ctx, env)
	case "mysql_history_list_length", "mysql_purge_backlog_length":
		return collectQuery(ctx, env, "select coalesce(max(COUNT),0) from information_schema.innodb_metrics where name = 'trx_rseg_history_len'")
	case "mysql_slowest_sql_seconds":
		return collectQuery(ctx, env, "select coalesce(max(MAX_TIMER_WAIT),0)/1000000000000 from performance_schema.events_statements_summary_by_digest")
	case "mysql_slow_sql_total_seconds":
		return collectQuery(ctx, env, "select coalesce(sum(SUM_TIMER_WAIT),0)/1000000000000 from performance_schema.events_statements_summary_by_digest")
	case "mysql_slow_sql_topn_trend":
		return collectQuery(ctx, env, "select least(count(*),10) from performance_schema.events_statements_summary_by_digest where DIGEST is not null")
	case "mysql_slow_sql_hot_db_stats":
		return collectQuery(ctx, env, "select coalesce(SCHEMA_NAME,'') as schema_name, sum(COUNT_STAR) as executions, sum(SUM_TIMER_WAIT)/1000000000000 as total_seconds from performance_schema.events_statements_summary_by_digest where DIGEST is not null group by SCHEMA_NAME order by total_seconds desc limit 10")
	case "mysql_slow_sql_hot_table_stats":
		return collectQuery(ctx, env, "select OBJECT_SCHEMA, OBJECT_NAME, COUNT_READ+COUNT_WRITE as operations from performance_schema.table_io_waits_summary_by_table order by operations desc limit 10")
	case "mysql_blocking_chain_length":
		return collectQuery(ctx, env, "select count(*) from information_schema.innodb_lock_waits")
	case "mysql_max_blocking_seconds":
		return collectQuery(ctx, env, "select coalesce(max(timestampdiff(second,trx_wait_started,now())),0) from information_schema.innodb_trx where trx_wait_started is not null")
	case "mysql_join_buffer_pressure":
		return collectRatio(ctx, env, statusValue("Select_full_join"), statusValue("Questions"))
	case "mysql_sort_buffer_pressure":
		return collectRatio(ctx, env, statusValue("Sort_merge_passes"), statusSum("Sort_rows", "Sort_scan", "Sort_range"))
	case "mysql_read_buffer_pressure":
		return collectRatio(ctx, env, statusValue("Handler_read_rnd_next"), statusSum("Handler_read_first", "Handler_read_key", "Handler_read_next", "Handler_read_prev", "Handler_read_rnd", "Handler_read_rnd_next"))
	case "mysql_read_rnd_buffer_pressure":
		return collectRatio(ctx, env, statusValue("Handler_read_rnd"), statusSum("Handler_read_first", "Handler_read_key", "Handler_read_next", "Handler_read_prev", "Handler_read_rnd", "Handler_read_rnd_next"))
	case "mysql_table_definition_cache_usage":
		return collectRatio(ctx, env, statusValue("Open_table_definitions"), variableValue("table_definition_cache"))
	case "mysql_checkpoint_pressure":
		return collectRatio(ctx, env, statusValue("Innodb_buffer_pool_pages_dirty"), statusValue("Innodb_buffer_pool_pages_total"))
	case "mysql_background_flush_pressure":
		return collectRatio(ctx, env, statusValue("Innodb_buffer_pool_wait_free"), statusSum("Innodb_buffer_pool_pages_flushed", "Innodb_buffer_pool_wait_free"))
	case "mysql_relay_log_backlog":
		return collectReplicaPositionDelta(ctx, env, "Read_Source_Log_Pos", "Read_Master_Log_Pos", "Exec_Source_Log_Pos", "Exec_Master_Log_Pos")
	case "mysql_relay_log_growth_rate":
		return collectReplicaPosition(ctx, env, "Relay_Log_Pos")
	case "mysql_relay_log_replay_rate":
		return collectReplicaPosition(ctx, env, "Exec_Source_Log_Pos", "Exec_Master_Log_Pos")
	case "mysql_master_binlog_rate":
		return collectMasterBinlogPosition(ctx, env)
	case "mysql_replica_catchup_status":
		return collectReplicaCaughtUp(ctx, env)
	case "mysql_replication_lag_change_rate":
		return collectReplicaField(ctx, env, spec, "Seconds_Behind_Source", "Seconds_Behind_Master")
	case "mysql_replication_error_count":
		return collectReplicationErrorCount(ctx, env)
	case "mysql_gtid_sync_status":
		return collectGTIDSyncStatus(ctx, env)
	case "mysql_master_role_change":
		return collectRole(ctx, env)
	case "mysql_last_replication_error_time":
		return collectLastReplicationErrorTime(ctx, env)
	case "mysql_undo_growth_rate":
		return collectPathUsedBytes(env.Static.UndoDir)
	case "mysql_tmp_dir_space_change":
		return collectPathUsedBytes(env.Static.TmpDir)
	case "mysql_error_log_size", "mysql_error_log_growth", "mysql_error_log_growth_per_min",
		"mysql_recent_error_count", "mysql_recent_warning_count", "mysql_error_code_stats",
		"mysql_oom_keyword_count", "mysql_crash_recovery_keyword_count",
		"mysql_replication_error_keyword_count", "mysql_disk_full_keyword_count",
		"mysql_table_corruption_keyword_count", "mysql_permission_failed_keyword_count":
		return collectErrorLogMetric(ctx, env, spec.Name)
	case "mysql_slow_query_log_growth":
		return collectLogFileSize(ctx, env, "slow_query_log_file")
	case "mysql_data_disk_usage":
		return collectPathDiskUsage(env.Static.DataDir)
	case "mysql_binlog_disk_usage":
		return collectPathDiskUsage(env.Static.BinlogDir)
	case "mysql_redo_disk_usage":
		return collectPathDiskUsage(env.Static.RedoDir)
	case "mysql_tmp_disk_usage":
		return collectPathDiskUsage(env.Static.TmpDir)
	case "mysql_undo_disk_usage":
		return collectPathDiskUsage(env.Static.UndoDir)
	}
	if statusName := strings.TrimSpace(spec.Params["status"]); statusName != "" {
		if noDBCredential(env) {
			return skippedValue("mysql credential not configured"), nil
		}
		return collectStatus(ctx, env, statusName)
	}
	if variableName := strings.TrimSpace(spec.Params["variable"]); variableName != "" {
		if noDBCredential(env) {
			return skippedValue("mysql credential not configured"), nil
		}
		return collectVariable(ctx, env, variableName)
	}
	if query := strings.TrimSpace(spec.Params["query"]); query != "" {
		if noDBCredential(env) {
			return skippedValue("mysql credential not configured"), nil
		}
		return collectQuery(ctx, env, query)
	}
	if replicaField := strings.TrimSpace(spec.Params["replica_field"]); replicaField != "" {
		if noDBCredential(env) {
			return skippedValue("mysql credential not configured"), nil
		}
		return collectReplicaField(ctx, env, spec, replicaField, spec.Params["slave_field"])
	}
	return skippedValue("collector not implemented yet"), nil
}

func collectMemoryModules(ctx context.Context, env *CollectEnv) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	rows, err := env.QueryRows(ctx, `
		select EVENT_NAME as event_name,
			CURRENT_COUNT_USED as current_count_used,
			CURRENT_NUMBER_OF_BYTES_USED as current_bytes,
			HIGH_COUNT_USED as high_count_used,
			HIGH_NUMBER_OF_BYTES_USED as high_bytes,
			COUNT_ALLOC as alloc_count,
			COUNT_FREE as free_count,
			SUM_NUMBER_OF_BYTES_ALLOC as total_allocated_bytes,
			SUM_NUMBER_OF_BYTES_FREE as total_freed_bytes
		from performance_schema.memory_summary_global_by_event_name
		where CURRENT_NUMBER_OF_BYTES_USED <> 0
		order by CURRENT_NUMBER_OF_BYTES_USED desc
	`)
	if err != nil {
		return skippedValue("performance_schema memory instrumentation unavailable: " + err.Error()), nil
	}
	return normalizeMemoryModuleRows(rows), nil
}

func normalizeMemoryModuleRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		eventName := strings.TrimSpace(toString(firstMapValue(row, "event_name", "EVENT_NAME")))
		if eventName == "" {
			continue
		}
		parts := strings.Split(eventName, "/")
		group := "other"
		module := eventName
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			group = strings.TrimSpace(parts[1])
		}
		if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) != "" {
			module = strings.TrimSpace(parts[len(parts)-1])
		}
		out = append(out, map[string]any{
			"event_name":            eventName,
			"group":                 group,
			"module":                module,
			"current_count_used":    firstMapValue(row, "current_count_used", "CURRENT_COUNT_USED"),
			"current_bytes":         firstMapValue(row, "current_bytes", "CURRENT_NUMBER_OF_BYTES_USED"),
			"high_count_used":       firstMapValue(row, "high_count_used", "HIGH_COUNT_USED"),
			"high_bytes":            firstMapValue(row, "high_bytes", "HIGH_NUMBER_OF_BYTES_USED"),
			"alloc_count":           firstMapValue(row, "alloc_count", "COUNT_ALLOC"),
			"free_count":            firstMapValue(row, "free_count", "COUNT_FREE"),
			"total_allocated_bytes": firstMapValue(row, "total_allocated_bytes", "SUM_NUMBER_OF_BYTES_ALLOC"),
			"total_freed_bytes":     firstMapValue(row, "total_freed_bytes", "SUM_NUMBER_OF_BYTES_FREE"),
		})
	}
	return out
}

func firstMapValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			return value
		}
	}
	return float64(0)
}

func collectReplicaPosition(ctx context.Context, env *CollectEnv, fields ...string) (float64, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return 0, err
	}
	for _, field := range fields {
		if value, ok := toFloat(parseNumberOrString(status[field])); ok {
			return value, nil
		}
	}
	return 0, nil
}

func collectReplicaPositionDelta(ctx context.Context, env *CollectEnv, readSource, readMaster, execSource, execMaster string) (float64, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return 0, err
	}
	read, _ := toFloat(parseNumberOrString(firstNonEmpty(status[readSource], status[readMaster])))
	executed, _ := toFloat(parseNumberOrString(firstNonEmpty(status[execSource], status[execMaster])))
	if read < executed {
		return 0, nil
	}
	return read - executed, nil
}

func collectMasterBinlogPosition(ctx context.Context, env *CollectEnv) (any, error) {
	rows, err := env.QueryRows(ctx, "show master status")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return float64(0), nil
	}
	for _, key := range []string{"Position", "position"} {
		if value, ok := toFloat(rows[0][key]); ok {
			return value, nil
		}
	}
	return float64(0), nil
}

func collectReplicaCaughtUp(ctx context.Context, env *CollectEnv) (bool, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return false, err
	}
	if len(status) == 0 {
		return true, nil
	}
	lag, _ := toFloat(parseNumberOrString(firstNonEmpty(status["Seconds_Behind_Source"], status["Seconds_Behind_Master"])))
	io := firstNonEmpty(status["Replica_IO_Running"], status["Slave_IO_Running"])
	sqlThread := firstNonEmpty(status["Replica_SQL_Running"], status["Slave_SQL_Running"])
	return lag == 0 && strings.EqualFold(io, "yes") && strings.EqualFold(sqlThread, "yes"), nil
}

func collectReplicationErrorCount(ctx context.Context, env *CollectEnv) (float64, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return 0, err
	}
	count := float64(0)
	if strings.TrimSpace(firstNonEmpty(status["Last_IO_Error"], status["Last_IO_Errno"])) != "" && firstNonEmpty(status["Last_IO_Errno"]) != "0" {
		count++
	}
	if strings.TrimSpace(firstNonEmpty(status["Last_SQL_Error"], status["Last_SQL_Errno"])) != "" && firstNonEmpty(status["Last_SQL_Errno"]) != "0" {
		count++
	}
	return count, nil
}

func collectGTIDSyncStatus(ctx context.Context, env *CollectEnv) (bool, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return false, err
	}
	if len(status) == 0 {
		return true, nil
	}
	autoPosition := firstNonEmpty(status["Auto_Position"])
	if autoPosition == "0" || strings.EqualFold(autoPosition, "no") {
		return false, nil
	}
	return collectReplicaCaughtUp(ctx, env)
}

func collectLastReplicationErrorTime(ctx context.Context, env *CollectEnv) (string, error) {
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return "", err
	}
	return firstNonEmpty(status["Last_SQL_Error_Timestamp"], status["Last_IO_Error_Timestamp"]), nil
}

func collectPathUsedBytes(path string) (float64, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	return float64(total - available), nil
}

func collectLogFileSize(ctx context.Context, env *CollectEnv, variable string) (float64, error) {
	pathValue, err := collectVariable(ctx, env, variable)
	if err != nil {
		return 0, err
	}
	path := strings.TrimSpace(toString(pathValue))
	if path == "" {
		return 0, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(env.Static.DataDir, path)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return float64(info.Size()), nil
}

func collectErrorLogMetric(ctx context.Context, env *CollectEnv, metric string) (any, error) {
	pathValue, err := collectVariable(ctx, env, "log_error")
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(toString(pathValue))
	if path == "" || strings.EqualFold(path, "stderr") {
		return skippedValue("mysql error log is not a readable file"), nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(env.Static.DataDir, path)
	}
	if metric == "mysql_error_log_size" || metric == "mysql_error_log_growth" || metric == "mysql_error_log_growth_per_min" {
		info, statErr := os.Stat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return float64(0), nil
		}
		if statErr != nil {
			return nil, statErr
		}
		return float64(info.Size()), nil
	}
	return scanErrorLog(ctx, path, metric)
}

func scanErrorLog(ctx context.Context, path, metric string) (any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const maxScan = int64(32 << 20)
	if info.Size() > maxScan {
		_, _ = file.Seek(info.Size()-maxScan, 0)
	}
	count := 0
	codes := map[string]int{}
	cutoff := time.Now().UTC().Add(-5 * time.Minute)
	var entryAt time.Time
	keywords := map[string][]string{
		"mysql_oom_keyword_count":               {"out of memory", "oom"},
		"mysql_crash_recovery_keyword_count":    {"crash recovery", "starting crash recovery"},
		"mysql_replication_error_keyword_count": {"replication", "slave sql", "replica sql"},
		"mysql_disk_full_keyword_count":         {"disk full", "no space left"},
		"mysql_table_corruption_keyword_count":  {"corrupt", "crashed table"},
		"mysql_permission_failed_keyword_count": {"access denied", "permission denied"},
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		rawLine := scanner.Text()
		if parsed, ok := mysqlLogTimestamp(rawLine); ok {
			entryAt = parsed
		}
		if entryAt.IsZero() || entryAt.Before(cutoff) {
			continue
		}
		line := strings.ToLower(rawLine)
		switch metric {
		case "mysql_recent_error_count":
			if strings.Contains(line, "[error]") {
				count++
			}
		case "mysql_recent_warning_count":
			if strings.Contains(line, "[warning]") {
				count++
			}
		case "mysql_error_code_stats":
			for _, field := range strings.Fields(rawLine) {
				trimmed := strings.Trim(field, "[](),:")
				if strings.HasPrefix(trimmed, "MY-") {
					codes[trimmed]++
				}
			}
		default:
			for _, keyword := range keywords[metric] {
				if strings.Contains(line, keyword) {
					count++
					break
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if metric == "mysql_error_code_stats" {
		return codes, nil
	}
	return float64(count), nil
}

func mysqlLogTimestamp(line string) (time.Time, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}, false
	}
	text := strings.TrimSpace(fields[0])
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999Z07:00"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), true
		}
	}
	if len(fields) > 1 {
		if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", fields[0]+" "+fields[1], time.Local); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(fmt.Sprint(value), `"`))
}

func collectStatusSum(ctx context.Context, env *CollectEnv, names ...string) (float64, error) {
	var total float64
	for _, name := range names {
		value, err := collectStatus(ctx, env, name)
		if err != nil {
			return 0, err
		}
		number, ok := toFloat(value)
		if !ok {
			return 0, nil
		}
		total += number
	}
	return total, nil
}

func collectPathDiskUsage(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]any{"path": "", "available": false}, nil
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	available := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return map[string]any{"path": path, "available": false}, nil
	}
	used := total - available
	return map[string]any{
		"path": path, "available": true, "total_bytes": total, "used_bytes": used,
		"available_bytes": available, "used_percent": float64(used) * 100 / float64(total),
	}, nil
}

func collectConnectivity(ctx context.Context, env *CollectEnv) (map[string]any, error) {
	tcpOK := tcpListening(ctx, env.Connect.Host, env.port(), env.timeout())
	socketOK := false
	if strings.TrimSpace(env.Connect.Socket) != "" {
		if _, err := os.Stat(env.Connect.Socket); err == nil {
			socketOK = true
		}
	}
	tcpDBOK := pingDB(ctx, env, false)
	socketDBOK := false
	if socketOK {
		socketDBOK = pingDB(ctx, env, true)
	}
	finalOK := tcpDBOK || socketDBOK
	return map[string]any{
		"tcp_port_ok":  tcpOK,
		"socket_ok":    socketOK,
		"tcp_db_ok":    tcpDBOK,
		"socket_db_ok": socketDBOK,
		"final_ok":     finalOK,
	}, nil
}

func pingDB(ctx context.Context, env *CollectEnv, useSocket bool) bool {
	if noDBCredential(env) {
		return false
	}
	db, err := env.OpenDB(useSocket)
	if err != nil {
		return false
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, env.timeout())
	defer cancel()
	return db.PingContext(ctx) == nil
}

func collectStatus(ctx context.Context, env *CollectEnv, name string) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	v, err := env.QueryGlobalStatus(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return skippedValue("mysql status not found: " + name), nil
		}
		return nil, err
	}
	return parseNumberOrString(v), nil
}

func collectVariable(ctx context.Context, env *CollectEnv, name string) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	v, err := env.QueryGlobalVariable(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return skippedValue("mysql variable not found: " + name), nil
		}
		return nil, err
	}
	return parseNumberOrString(v), nil
}

func collectVariableBool(ctx context.Context, env *CollectEnv, name string) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	v, err := env.QueryGlobalVariable(ctx, name)
	if err != nil {
		return false, err
	}
	return boolFromONOFF(v), nil
}

func collectReplicationBasic(ctx context.Context, env *CollectEnv) (map[string]any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return nil, err
	}
	ioRunning := firstNonEmpty(status["Replica_IO_Running"], status["Slave_IO_Running"])
	sqlRunning := firstNonEmpty(status["Replica_SQL_Running"], status["Slave_SQL_Running"])
	lag := firstNonEmpty(status["Seconds_Behind_Source"], status["Seconds_Behind_Master"])
	lastErr := firstNonEmpty(status["Last_IO_Error"], status["Last_SQL_Error"], status["Last_Error"])
	return map[string]any{
		"io_running":     ioRunning,
		"sql_running":    sqlRunning,
		"lag_seconds":    parseNumberOrString(lag),
		"last_error":     lastErr,
		"replica_status": status,
	}, nil
}

func collectReplicaField(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec, replicaField, slaveField string) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	status, err := env.QueryReplicaStatus(ctx)
	if err != nil {
		return nil, err
	}
	if replicaField == "" {
		replicaField = spec.Params["replica_field"]
	}
	if slaveField == "" {
		slaveField = spec.Params["slave_field"]
	}
	return parseNumberOrString(firstNonEmpty(status[replicaField], status[slaveField])), nil
}

func collectRole(ctx context.Context, env *CollectEnv) (string, error) {
	if noDBCredential(env) {
		return "unknown", nil
	}
	status, err := env.QueryReplicaStatus(ctx)
	if err == nil && len(status) > 0 {
		return "replica", nil
	}
	readOnly, err := env.QueryGlobalVariable(ctx, "read_only")
	if err != nil {
		return "", err
	}
	if boolFromONOFF(readOnly) {
		return "replica_or_readonly", nil
	}
	return "primary", nil
}

func collectSemisync(ctx context.Context, env *CollectEnv) (map[string]any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	names := []string{
		"Rpl_semi_sync_master_status",
		"Rpl_semi_sync_source_status",
		"Rpl_semi_sync_slave_status",
		"Rpl_semi_sync_replica_status",
	}
	out := map[string]any{}
	for _, name := range names {
		v, err := env.QueryGlobalStatus(ctx, name)
		if err == nil {
			out[name] = parseNumberOrString(v)
		}
	}
	return out, nil
}

func collectQuery(ctx context.Context, env *CollectEnv, query string) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	rows, err := env.QueryRows(ctx, query)
	if err != nil {
		if strings.Contains(strings.ToLower(query), "information_schema.innodb_lock_waits") {
			fallbackRows, fallbackErr := env.QueryRows(ctx, "select count(*) as blocked_sessions from information_schema.innodb_trx where trx_state = 'LOCK WAIT'")
			if fallbackErr == nil {
				rows = fallbackRows
				err = nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 1 && len(rows[0]) == 1 {
		for _, v := range rows[0] {
			return v, nil
		}
	}
	return rows, nil
}

type numericSource func(context.Context, *CollectEnv) (any, error)

func statusValue(name string) numericSource {
	return func(ctx context.Context, env *CollectEnv) (any, error) {
		return collectStatus(ctx, env, name)
	}
}

func variableValue(name string) numericSource {
	return func(ctx context.Context, env *CollectEnv) (any, error) {
		return collectVariable(ctx, env, name)
	}
}

func statusSum(names ...string) numericSource {
	return func(ctx context.Context, env *CollectEnv) (any, error) {
		var sum float64
		for _, name := range names {
			v, err := collectStatus(ctx, env, name)
			if err != nil {
				return nil, err
			}
			f, _ := toFloat(v)
			sum += f
		}
		return sum, nil
	}
}

func statusDiff(totalName, freeName string) numericSource {
	return func(ctx context.Context, env *CollectEnv) (any, error) {
		total, err := collectStatus(ctx, env, totalName)
		if err != nil {
			return nil, err
		}
		free, err := collectStatus(ctx, env, freeName)
		if err != nil {
			return nil, err
		}
		totalF, _ := toFloat(total)
		freeF, _ := toFloat(free)
		return totalF - freeF, nil
	}
}

func collectRatio(ctx context.Context, env *CollectEnv, numerator, denominator numericSource) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	n, err := numerator(ctx, env)
	if err != nil {
		return nil, err
	}
	d, err := denominator(ctx, env)
	if err != nil {
		return nil, err
	}
	return ratio(n, d), nil
}

func collectBufferPoolHitRatio(ctx context.Context, env *CollectEnv) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	physical, err := collectStatus(ctx, env, "Innodb_buffer_pool_reads")
	if err != nil {
		return nil, err
	}
	logical, err := collectStatus(ctx, env, "Innodb_buffer_pool_read_requests")
	if err != nil {
		return nil, err
	}
	p, _ := toFloat(physical)
	l, _ := toFloat(logical)
	if l == 0 {
		return 0, nil
	}
	return (1 - p/l) * 100, nil
}

func collectThreadCacheHitRatio(ctx context.Context, env *CollectEnv) (any, error) {
	if noDBCredential(env) {
		return skippedValue("mysql credential not configured"), nil
	}
	created, err := collectStatus(ctx, env, "Threads_created")
	if err != nil {
		return nil, err
	}
	connections, err := collectStatus(ctx, env, "Connections")
	if err != nil {
		return nil, err
	}
	c, _ := toFloat(created)
	total, _ := toFloat(connections)
	if total == 0 {
		return 0, nil
	}
	return (1 - c/total) * 100, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func noDBCredential(env *CollectEnv) bool {
	return env == nil || strings.TrimSpace(env.Connect.Username) == ""
}

func skippedValue(reason string) map[string]any {
	return map[string]any{"skipped": true, "reason": reason}
}

func localPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
