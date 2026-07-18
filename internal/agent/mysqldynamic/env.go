package mysqldynamic

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gmha/internal/agent/mysqlcheck"
)

// MySQLConnectInfo 是 MySQL 连接信息，包含主机、端口、Socket、用户名、密码、数据库和超时时间。
type MySQLConnectInfo struct {
	Host     string
	Port     int
	Socket   string
	Username string
	Password string
	Database string
	Timeout  time.Duration
}

// StaticMySQLInfoRef 是 MySQL 实例的静态路径信息引用，包含端口、Socket、数据目录、Binlog 目录等。
type StaticMySQLInfoRef struct {
	Port      int
	Socket    string
	DataDir   string
	BinlogDir string
	RedoDir   string
	TmpDir    string
	UndoDir   string
}

// CommandResult 是 Shell 命令执行结果，包含标准输出、标准错误、退出码和错误信息。
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// CommandExecutor 定义命令执行器接口，用于在采集环境中执行 Shell 命令。
type CommandExecutor interface {
	Run(ctx context.Context, command string) CommandResult
}

// ShellCommandExecutor 是基于 /bin/bash 的命令执行器实现。
type ShellCommandExecutor struct{}

func (ShellCommandExecutor) Run(ctx context.Context, command string) CommandResult {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return CommandResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: exitCode,
		Err:      err,
	}
}

// CollectEnv 是 MySQL 动态指标采集环境，封装了连接信息、静态路径、日志器和命令执行器，
// 为采集器提供统一的数据库查询和命令执行能力。
type CollectEnv struct {
	Instance string
	Connect  MySQLConnectInfo
	Static   StaticMySQLInfoRef
	Logger   *log.Logger
	Executor CommandExecutor
}

// BuildCollectEnv 根据心跳配置文件路径构建单个 MySQL 采集环境，若配置了多个实例则返回第一个。
func BuildCollectEnv(configPath string) (*CollectEnv, error) {
	envs, err := BuildCollectEnvs(configPath)
	if err != nil {
		return nil, err
	}
	if len(envs) == 0 {
		return nil, errors.New("no mysql collect env configured")
	}
	return envs[0], nil
}

// BuildCollectEnvs 根据心跳配置文件路径构建所有 MySQL 实例的采集环境列表。
func BuildCollectEnvs(configPath string) ([]*CollectEnv, error) {
	cfg, _, err := loadMySQLHeartbeatConfig(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	instances := cfg.Instances
	if len(instances) == 0 {
		return []*CollectEnv{}, nil
	}
	out := make([]*CollectEnv, 0, len(instances))
	for _, instance := range instances {
		out = append(out, collectEnvFromInstance(instance))
	}
	return out, nil
}

func collectEnvFromInstance(instance mysqlcheck.InstanceConfig) *CollectEnv {
	if instance.Port <= 0 {
		instance.Port = 3306
	}
	if instance.Database == "" {
		instance.Database = "gmha"
	}
	return &CollectEnv{
		Instance: instanceName(instance),
		Connect: MySQLConnectInfo{
			Host:     "127.0.0.1",
			Port:     instance.Port,
			Socket:   strings.TrimSpace(instance.Socket),
			Username: strings.TrimSpace(instance.Username),
			Password: instance.Password,
			Database: instance.Database,
			Timeout:  1 * time.Second,
		},
		Static: StaticMySQLInfoRef{
			Port:      instance.Port,
			Socket:    strings.TrimSpace(instance.Socket),
			DataDir:   instance.DataDir,
			BinlogDir: instance.BinlogDir,
			RedoDir:   instance.RedoDir,
			TmpDir:    instance.TmpDir,
			UndoDir:   instance.UndoDir,
		},
		Logger:   log.Default(),
		Executor: ShellCommandExecutor{},
	}
}

func instanceName(instance mysqlcheck.InstanceConfig) string {
	if strings.TrimSpace(instance.Socket) != "" {
		return "socket:" + strings.TrimSpace(instance.Socket)
	}
	return "port:" + strconv.Itoa(instance.Port)
}

func loadMySQLHeartbeatConfig(configPath string) (mysqlcheck.Config, string, error) {
	seen := map[string]bool{}
	paths := []string{strings.TrimSpace(configPath)}
	if dir := strings.TrimSpace(os.Getenv("GMHA_AGENT_INSTALL_DIR")); dir != "" {
		paths = append(paths, filepath.Join(dir, mysqlcheck.DefaultConfigFile))
	}
	paths = append(paths,
		filepath.Join("/home/gmha/agent", mysqlcheck.DefaultConfigFile),
		filepath.Join("/root/gmha/agent", mysqlcheck.DefaultConfigFile),
	)
	var missing error = os.ErrNotExist
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		cfg, hash, err := mysqlcheck.LoadConfig(path)
		if err == nil {
			return cfg, hash, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return mysqlcheck.Config{}, "", err
		}
		missing = err
	}
	return mysqlcheck.Config{}, "", missing
}

// OpenDB 打开一个 MySQL 数据库连接，useSocket 为 true 时使用 Unix Socket 连接，否则使用 TCP 连接。
func (e *CollectEnv) OpenDB(useSocket bool) (*sql.DB, error) {
	if e == nil {
		return nil, errors.New("mysql collect env is nil")
	}
	if e.Connect.Username == "" {
		return nil, errors.New("mysql username is not configured")
	}
	cfg := mysqlDriver.NewConfig()
	cfg.User = e.Connect.Username
	cfg.Passwd = e.Connect.Password
	cfg.DBName = e.Connect.Database
	cfg.Timeout = e.timeout()
	cfg.ReadTimeout = e.timeout()
	cfg.WriteTimeout = e.timeout()
	if useSocket {
		if strings.TrimSpace(e.Connect.Socket) == "" {
			return nil, errors.New("mysql socket is not configured")
		}
		cfg.Net = "unix"
		cfg.Addr = e.Connect.Socket
	} else {
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(defaultString(e.Connect.Host, "127.0.0.1"), strconv.Itoa(e.port()))
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)
	return db, nil
}

// QueryGlobalStatus 查询 MySQL 全局状态变量的值。
func (e *CollectEnv) QueryGlobalStatus(ctx context.Context, name string) (string, error) {
	return QueryGlobalStatus(ctx, e, name)
}

// QueryGlobalVariable 查询 MySQL 全局系统变量的值。
func (e *CollectEnv) QueryGlobalVariable(ctx context.Context, name string) (string, error) {
	return QueryGlobalVariable(ctx, e, name)
}

// QueryReplicaStatus 查询 MySQL 复制状态，兼容 SHOW REPLICA STATUS 和 SHOW SLAVE STATUS 语法。
func (e *CollectEnv) QueryReplicaStatus(ctx context.Context) (map[string]string, error) {
	return QueryReplicaStatus(ctx, e)
}

// QueryScalar 执行 SQL 查询并返回单个标量值。
func (e *CollectEnv) QueryScalar(ctx context.Context, query string) (any, error) {
	db, err := e.OpenDB(false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	var v any
	if err := db.QueryRowContext(ctx, query).Scan(&v); err != nil {
		return nil, err
	}
	return normalizeSQLValue(v), nil
}

// QueryRows 执行 SQL 查询并返回多行结果，每行以列名为键的 map 表示。
func (e *CollectEnv) QueryRows(ctx context.Context, query string) ([]map[string]any, error) {
	db, err := e.OpenDB(false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		item := make(map[string]any, len(cols))
		for i, col := range cols {
			item[col] = normalizeSQLValue(values[i])
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// QueryGlobalStatus 是包级别的全局状态查询函数，通过 SHOW GLOBAL STATUS 获取指定状态变量的值。
func QueryGlobalStatus(ctx context.Context, env *CollectEnv, name string) (string, error) {
	return queryNameValue(ctx, env, "show global status like '"+escapeMySQLLikeValue(name)+"'")
}

// QueryGlobalVariable 是包级别的全局变量查询函数，通过 SHOW GLOBAL VARIABLES 获取指定系统变量的值。
func QueryGlobalVariable(ctx context.Context, env *CollectEnv, name string) (string, error) {
	return queryNameValue(ctx, env, "show global variables like '"+escapeMySQLLikeValue(name)+"'")
}

// QueryReplicaStatus 是包级别的复制状态查询函数，自动兼容新旧版本 MySQL 的复制状态语法。
func QueryReplicaStatus(ctx context.Context, env *CollectEnv) (map[string]string, error) {
	db, err := env.OpenDB(false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, env.timeout())
	defer cancel()
	out, err := queryReplicaStatusOnce(ctx, db, "show replica status")
	if err == nil {
		return out, nil
	}
	return queryReplicaStatusOnce(ctx, db, "show slave status")
}

func queryNameValue(ctx context.Context, env *CollectEnv, query string) (string, error) {
	db, err := env.OpenDB(false)
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, env.timeout())
	defer cancel()
	var key, value string
	if err := db.QueryRowContext(ctx, query).Scan(&key, &value); err != nil {
		return "", err
	}
	return value, nil
}

func escapeMySQLLikeValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `''`)
	return v
}

func queryReplicaStatusOnce(ctx context.Context, db *sql.DB, query string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return map[string]string{}, nil
	}
	values := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cols))
	for i, col := range cols {
		if values[i].Valid {
			out[col] = values[i].String
		}
	}
	return out, rows.Err()
}

func (e *CollectEnv) timeout() time.Duration {
	if e != nil && e.Connect.Timeout > 0 {
		return e.Connect.Timeout
	}
	return time.Second
}

func (e *CollectEnv) port() int {
	if e != nil && e.Connect.Port > 0 {
		return e.Connect.Port
	}
	return 3306
}

func normalizeSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return x
	}
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func boolFromONOFF(v string) bool {
	return strings.EqualFold(v, "ON") || strings.EqualFold(v, "YES") || v == "1" || strings.EqualFold(v, "true")
}

func tcpListening(ctx context.Context, host string, port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(defaultString(host, "127.0.0.1"), strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func processAlive(ctx context.Context) bool {
	entries, err := os.ReadDir("/proc")
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, err := strconv.Atoi(entry.Name()); err != nil {
				continue
			}
			data, err := os.ReadFile("/proc/" + entry.Name() + "/comm")
			if err == nil && strings.Contains(strings.ToLower(string(data)), "mysqld") {
				return true
			}
		}
	}
	cmdCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return exec.CommandContext(cmdCtx, "pgrep", "-x", "mysqld").Run() == nil
}

func parseNumberOrString(v string) any {
	if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		return f
	}
	return v
}

func ratio(numerator, denominator any) float64 {
	n, ok1 := toFloat(numerator)
	d, ok2 := toFloat(denominator)
	if !ok1 || !ok2 || d == 0 {
		return 0
	}
	return n / d * 100
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func unsupportedMetric(name string) error {
	return fmt.Errorf("mysql dynamic builtin %s is registered but has no query mapping yet", name)
}
