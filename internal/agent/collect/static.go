package collect

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	collectdomain "gmha/internal/collect"
	taskdomain "gmha/internal/domain/task"
)

// StaticCollector 是静态信息采集器，负责采集主机和 MySQL 的静态配置信息。
type StaticCollector struct {
	installDir string
}

// NewStaticCollector 创建一个新的静态信息采集器实例，installDir 为 GMHA 安装目录。
func NewStaticCollector(installDir string) *StaticCollector {
	return &StaticCollector{installDir: installDir}
}

// Collect 执行静态信息采集，包括主机硬件信息和 MySQL 实例配置信息。
func (c *StaticCollector) Collect(ctx context.Context, req taskdomain.CollectStaticInfoSpec) (*collectdomain.StaticInfo, error) {
	machine, err := NewMachineCollector().Collect(ctx)
	if err != nil {
		return nil, err
	}
	mysqlInfo := c.collectMySQL(ctx, req.MySQL)
	now := time.Now().UTC()
	return &collectdomain.StaticInfo{
		Host: collectdomain.HostStaticInfo{
			Arch:           machine.Arch,
			IPs:            machine.IPs,
			Interfaces:     machine.Interfaces,
			GlibcVersion:   machine.GlibcVersion,
			MemoryGB:       machine.MemoryGB,
			CPUCores:       machine.CPUCores,
			OS:             machine.OS,
			SwapEnabled:    machine.SwapEnabled,
			NTPEnabled:     machine.NTPEnabled,
			SELinux:        machine.SELinux,
			Firewall:       machine.Firewall,
			MySQLInstalled: mysqlInfo.Installed,
			GMHAInstalled:  c.detectGMHAInstalled(),
		},
		MySQL:           mysqlInfo,
		AgentTimeUnixMS: now.UnixMilli(),
		CollectedAt:     now,
	}, nil
}

func (c *StaticCollector) detectGMHAInstalled() bool {
	if c.installDir != "" {
		if _, err := os.Stat(filepath.Join(c.installDir, "agentd")); err == nil {
			return true
		}
	}
	if _, err := os.Stat("/etc/systemd/system/gmha-agent.service"); err == nil {
		return true
	}
	return false
}

func (c *StaticCollector) collectMySQL(ctx context.Context, cfg taskdomain.MySQLStaticCollectSpec) collectdomain.MySQLStaticInfo {
	info := collectdomain.MySQLStaticInfo{Installed: detectMySQLInstalled()}
	if !cfg.Enabled {
		return info
	}
	if strings.TrimSpace(cfg.Username) == "" {
		cfg.Username = "monitor"
	}
	if strings.TrimSpace(cfg.Password) == "" {
		cfg.Password = "3306niubi"
	}
	if cfg.Port <= 0 {
		cfg.Port = 3306
	}
	if strings.TrimSpace(cfg.Socket) == "" {
		cfg.Socket = "/data/3306/data/mysql.sock"
	}
	db, err := openMonitorDB(cfg)
	if err != nil {
		info.Error = "monitor 连接配置失败: " + err.Error()
		return info
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		info.Error = "monitor 连接失败: " + err.Error()
		return info
	}
	vars, err := queryMySQLVariables(ctx, db)
	if err != nil {
		info.Error = "采集 MySQL 变量失败: " + err.Error()
		return info
	}
	info.CollectOK = true
	info.Installed = true
	info.ServerID = atoi(vars["server_id"])
	info.BaseDir = vars["basedir"]
	info.Version = vars["version"]
	info.Port = atoi(vars["port"])
	info.ConfigFile = deriveMyCnfPath(info.Socket, cfg.Port)
	info.SlowLog = vars["slow_query_log_file"]
	info.ErrorLog = vars["log_error"]
	info.Socket = vars["socket"]
	info.DataDir = vars["datadir"]
	info.UndoDir = vars["innodb_undo_directory"]
	info.RedoDir = vars["innodb_log_group_home_dir"]
	info.BinlogDir = filepath.Dir(vars["log_bin_basename"])
	info.TmpDir = vars["tmpdir"]
	return info
}

func detectMySQLInstalled() bool {
	checks := [][]string{
		{"systemctl", "is-active", "mysqld"},
		{"/usr/local/mysql/bin/mysql", "-V"},
	}
	for _, item := range checks {
		out, err := runCommand(context.Background(), item[0], item[1:]...)
		if err == nil && strings.TrimSpace(out) != "" {
			return true
		}
	}
	return false
}

func openMonitorDB(cfg taskdomain.MySQLStaticCollectSpec) (*sql.DB, error) {
	mysqlCfg := mysqlDriver.NewConfig()
	mysqlCfg.User = cfg.Username
	mysqlCfg.Passwd = cfg.Password
	mysqlCfg.Timeout = 5 * time.Second
	mysqlCfg.ReadTimeout = 5 * time.Second
	mysqlCfg.WriteTimeout = 5 * time.Second
	if socket := strings.TrimSpace(cfg.Socket); socket != "" && socketExists(socket) {
		mysqlCfg.Net = "unix"
		mysqlCfg.Addr = socket
	} else {
		mysqlCfg.Net = "tcp"
		host := strings.TrimSpace(cfg.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		mysqlCfg.Addr = host + ":" + strconv.Itoa(cfg.Port)
	}
	return sql.Open("mysql", mysqlCfg.FormatDSN())
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func queryMySQLVariables(ctx context.Context, db *sql.DB) (map[string]string, error) {
	names := []string{
		"server_id", "basedir", "version", "port", "pid_file", "slow_query_log_file",
		"log_error", "socket", "datadir", "innodb_undo_directory",
		"innodb_log_group_home_dir", "log_bin_basename", "tmpdir",
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		var value string
		rowCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := db.QueryRowContext(rowCtx, "select @@"+name).Scan(&value)
		cancel()
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func atoi(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func deriveMyCnfPath(socket string, port int) string {
	socket = strings.TrimSpace(socket)
	if socket != "" {
		dataDir := filepath.Dir(socket)
		instanceDir := filepath.Dir(dataDir)
		if instanceDir != "." && instanceDir != "/" {
			return filepath.Join(instanceDir, "my.cnf")
		}
	}
	if port <= 0 {
		port = 3306
	}
	return filepath.Join("/data", strconv.Itoa(port), "my.cnf")
}
