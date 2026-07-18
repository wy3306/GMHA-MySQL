package mysqldynamic

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
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
