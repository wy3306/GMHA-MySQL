package dynamic

import (
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

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gmha/internal/agent/mysqlcheck"
	dyndomain "gmha/internal/domain/dynamic"
)

// MySQLCollector 是 MySQL 动态指标采集器，支持采集 MySQL 进程状态、端口监听、连接性、磁盘使用率等指标。
type MySQLCollector struct {
	configPath string
	name       string
}

// NewMySQLCollector 创建一个新的 MySQL 动态指标采集器实例。
func NewMySQLCollector(configPath, name string) *MySQLCollector {
	return &MySQLCollector{configPath: configPath, name: name}
}

// Name 返回采集器名称。
func (c *MySQLCollector) Name() string { return c.name }

// Collect 执行 MySQL 指标采集，支持多实例并行采集。
func (c *MySQLCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	started := time.Now()
	cfg, _, err := mysqlcheck.LoadConfig(c.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = mysqlcheck.Config{Instances: []mysqlcheck.InstanceConfig{instanceFromSpec(spec)}}
		} else {
			return metricError(spec, err, time.Since(started).Milliseconds())
		}
	}
	if len(cfg.Instances) == 0 {
		cfg.Instances = []mysqlcheck.InstanceConfig{instanceFromSpec(spec)}
	}
	values := make([]map[string]any, 0, len(cfg.Instances))
	success := true
	for _, instance := range cfg.Instances {
		item, ok := c.collectOne(ctx, spec, normalizeMySQLInstance(instance))
		if !ok {
			success = false
		}
		values = append(values, item)
	}
	result := metricOK(spec, "mysql", dyndomain.ValueTypeArray, values, started)
	result.Success = success
	if !success {
		result.Error = "one or more mysql instances failed"
	}
	return result
}

func (c *MySQLCollector) collectOne(ctx context.Context, spec dyndomain.CollectTaskSpec, instance mysqlcheck.InstanceConfig) (map[string]any, bool) {
	base := map[string]any{"port": instance.Port, "socket": instance.Socket}
	switch c.name {
	case "mysql_process_alive":
		ok := mysqldProcessAlive()
		base["ok"] = ok
		return base, ok
	case "mysql_port_listening":
		ok := tcpListening(instance.Port)
		base["ok"] = ok
		return base, ok
	case "mysql_socket_ok":
		ok := socketOK(instance.Socket)
		base["ok"] = ok
		return base, ok
	case "mysql_connectivity":
		tcpOK := mysqlPing(ctx, instance, false) == nil
		socketOK := false
		if instance.Socket != "" {
			socketOK = mysqlPing(ctx, instance, true) == nil
		}
		base["tcp_ok"] = tcpOK
		base["socket_ok"] = socketOK
		base["final_ok"] = tcpOK || socketOK
		return base, tcpOK || socketOK
	case "mysql_data_disk_usage":
		return diskUsageValue(base, instance.DataDir)
	case "mysql_binlog_disk_usage":
		return diskUsageValue(base, instance.BinlogDir)
	case "mysql_redo_disk_usage":
		return diskUsageValue(base, instance.RedoDir)
	case "mysql_tmp_disk_usage":
		return diskUsageValue(base, instance.TmpDir)
	case "mysql_undo_disk_usage":
		path := instance.UndoDir
		if path == "" {
			path = spec.Params["undog_dir"]
		}
		return diskUsageValue(base, path)
	default:
		base["error"] = "unsupported mysql collector"
		return base, false
	}
}

func instanceFromSpec(spec dyndomain.CollectTaskSpec) mysqlcheck.InstanceConfig {
	port, _ := strconv.Atoi(spec.Params["port"])
	undoDir := spec.Params["undo_dir"]
	if undoDir == "" {
		undoDir = spec.Params["undog_dir"]
	}
	return normalizeMySQLInstance(mysqlcheck.InstanceConfig{
		Port:      port,
		Socket:    spec.Params["socket"],
		Username:  spec.Params["username"],
		Password:  spec.Params["password"],
		DataDir:   spec.Params["data_dir"],
		BinlogDir: spec.Params["binlog_dir"],
		RedoDir:   spec.Params["redo_dir"],
		TmpDir:    spec.Params["tmp_dir"],
		UndoDir:   undoDir,
	})
}

func normalizeMySQLInstance(instance mysqlcheck.InstanceConfig) mysqlcheck.InstanceConfig {
	if instance.Port <= 0 {
		instance.Port = 3306
	}
	if instance.DataDir == "" {
		instance.DataDir = fmt.Sprintf("/data/%d/data", instance.Port)
	}
	if instance.BinlogDir == "" {
		instance.BinlogDir = fmt.Sprintf("/data/%d/binlog", instance.Port)
	}
	if instance.RedoDir == "" {
		instance.RedoDir = fmt.Sprintf("/data/%d/redo", instance.Port)
	}
	if instance.TmpDir == "" {
		instance.TmpDir = fmt.Sprintf("/data/%d/tmp", instance.Port)
	}
	if instance.UndoDir == "" {
		instance.UndoDir = fmt.Sprintf("/data/%d/undo", instance.Port)
	}
	if instance.Socket == "" {
		instance.Socket = fmt.Sprintf("/data/%d/data/mysql.sock", instance.Port)
	}
	return instance
}

func mysqldProcessAlive() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(data)) == "mysqld" {
			return true
		}
	}
	return false
}

func tcpListening(port int) bool {
	if port <= 0 {
		port = 3306
	}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			v, _ := strconv.ParseInt(portHex, 16, 32)
			if int(v) == port {
				return true
			}
		}
	}
	return false
}

func socketOK(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func mysqlPing(ctx context.Context, instance mysqlcheck.InstanceConfig, socket bool) error {
	cfg := mysqlDriver.NewConfig()
	cfg.User = instance.Username
	cfg.Passwd = instance.Password
	cfg.Timeout = time.Second
	if socket {
		cfg.Net = "unix"
		cfg.Addr = instance.Socket
	} else {
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(instance.Port))
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return db.PingContext(pingCtx)
}

func diskUsageValue(base map[string]any, path string) (map[string]any, bool) {
	base["path"] = path
	if path == "" {
		base["error"] = "path is empty"
		return base, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		base["error"] = err.Error()
		return base, false
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	if total == 0 {
		base["error"] = "disk total is zero"
		return base, false
	}
	used := total - free
	base["total_bytes"] = total
	base["used_bytes"] = used
	base["available_bytes"] = free
	base["used_percent"] = round2(100 * float64(total-free) / float64(total))
	return base, true
}
