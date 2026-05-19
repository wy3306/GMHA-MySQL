// Package mysqlcheck 提供 MySQL 心跳检查功能，通过定期更新心跳表来监控 MySQL 实例的可用性。
package mysqlcheck

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	hbdomain "gmha/internal/domain/heartbeat"
)

// DefaultConfigFile 是 MySQL 心跳配置文件的默认名称。
const DefaultConfigFile = "mysql-heartbeat.json"

// Config 是 MySQL 心跳配置结构体，包含需要监控的 MySQL 实例列表。
type Config struct {
	Instances []InstanceConfig `json:"instances"`
}

// InstanceConfig 是单个 MySQL 实例的连接和路径配置。
type InstanceConfig struct {
	Port        int    `json:"port"`
	Socket      string `json:"socket"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Database    string `json:"database"`
	SystemdUnit string `json:"systemd_unit"`
	DataDir     string `json:"data_dir,omitempty"`
	BinlogDir   string `json:"binlog_dir,omitempty"`
	RedoDir     string `json:"redo_dir,omitempty"`
	TmpDir      string `json:"tmp_dir,omitempty"`
	UndoDir     string `json:"undo_dir,omitempty"`
}

// Checker 是 MySQL 心跳检查器，管理数据库连接池并定期执行心跳检查，支持自动恢复。
type Checker struct {
	configPath   string
	mu           sync.Mutex
	configHash   string
	clients      map[string]*sql.DB
	ensured      map[string]bool
	lastSuccess  map[string]time.Time
	firstFailure map[string]time.Time
	lastRecovery map[string]time.Time
}

// NewChecker 创建一个新的 MySQL 心跳检查器实例。
func NewChecker(configPath string) *Checker {
	return &Checker{
		configPath:   strings.TrimSpace(configPath),
		clients:      make(map[string]*sql.DB),
		ensured:      make(map[string]bool),
		lastSuccess:  make(map[string]time.Time),
		firstFailure: make(map[string]time.Time),
		lastRecovery: make(map[string]time.Time),
	}
}

// Check 对所有配置的 MySQL 实例执行心跳检查，返回每个实例的健康状态。
func (c *Checker) Check(ctx context.Context) []hbdomain.HealthCheck {
	now := time.Now().UTC()
	cfg, hash, err := LoadConfig(c.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.closeAll()
			return nil
		}
		return []hbdomain.HealthCheck{{
			Name:      "mysql.heartbeat.config",
			Status:    hbdomain.CheckFail,
			Detail:    err.Error(),
			CheckedAt: now,
		}}
	}
	c.mu.Lock()
	if hash != c.configHash {
		c.closeAllLocked()
		c.configHash = hash
	}
	c.mu.Unlock()

	checks := make([]hbdomain.HealthCheck, 0, len(cfg.Instances))
	seen := make(map[string]bool, len(cfg.Instances))
	for _, instance := range cfg.Instances {
		instance = normalizeInstance(instance)
		key := instanceKey(instance)
		seen[key] = true
		checks = append(checks, c.checkInstance(ctx, instance, key, now))
	}
	c.closeStale(seen)
	return checks
}

func (c *Checker) checkInstance(ctx context.Context, instance InstanceConfig, key string, now time.Time) hbdomain.HealthCheck {
	if instance.Username == "" || instance.Password == "" {
		return hbdomain.HealthCheck{Name: "mysql.heartbeat." + instanceName(instance), Status: hbdomain.CheckWarn, Detail: "mha account is not configured", CheckedAt: now}
	}
	db, err := c.db(ctx, instance, key)
	if err != nil {
		return c.failCheck(ctx, instance, key, now, err.Error())
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(checkCtx, "select 1 from dual").Scan(&one); err != nil {
		c.reset(key)
		return c.failCheck(ctx, instance, key, now, "select 1 failed: "+err.Error())
	}
	if one != 1 {
		return c.failCheck(ctx, instance, key, now, "select 1 returned unexpected value")
	}
	if _, err := db.ExecContext(checkCtx, "update gmha.heartbeat set ts = current_timestamp where id = 1"); err != nil {
		c.reset(key)
		return c.failCheck(ctx, instance, key, now, "heartbeat update failed: "+err.Error())
	}
	c.markSuccess(key, now)
	return hbdomain.HealthCheck{Name: "mysql.heartbeat." + instanceName(instance), Status: hbdomain.CheckOK, Detail: "mha heartbeat ok", CheckedAt: now}
}

func (c *Checker) failCheck(ctx context.Context, instance InstanceConfig, key string, now time.Time, detail string) hbdomain.HealthCheck {
	if recoveryDetail := c.maybeRecover(ctx, instance, key, now); recoveryDetail != "" {
		detail += "; " + recoveryDetail
	}
	return hbdomain.HealthCheck{Name: "mysql.heartbeat." + instanceName(instance), Status: hbdomain.CheckFail, Detail: detail, CheckedAt: now}
}

func (c *Checker) maybeRecover(ctx context.Context, instance InstanceConfig, key string, now time.Time) string {
	c.mu.Lock()
	if c.firstFailure[key].IsZero() {
		c.firstFailure[key] = now
	}
	firstFailure := c.firstFailure[key]
	lastRecovery := c.lastRecovery[key]
	c.mu.Unlock()

	if now.Sub(firstFailure) < 30*time.Second {
		return ""
	}
	if !lastRecovery.IsZero() && now.Sub(lastRecovery) < 30*time.Second {
		return "mysql recovery recently attempted"
	}
	c.mu.Lock()
	c.lastRecovery[key] = now
	c.mu.Unlock()

	if err := startMySQL(ctx, instance.SystemdUnit); err != nil {
		return "systemctl start failed: " + err.Error()
	}
	return "systemctl start attempted"
}

func (c *Checker) db(ctx context.Context, instance InstanceConfig, key string) (*sql.DB, error) {
	c.mu.Lock()
	db := c.clients[key]
	if db == nil {
		var err error
		db, err = openDB(instance)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.clients[key] = db
	}
	ensured := c.ensured[key]
	c.mu.Unlock()

	if !ensured {
		ensureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ensureHeartbeatTable(ensureCtx, db)
		cancel()
		if err != nil {
			c.reset(key)
			return nil, err
		}
		c.mu.Lock()
		c.ensured[key] = true
		c.mu.Unlock()
	}
	return db, nil
}

func openDB(instance InstanceConfig) (*sql.DB, error) {
	cfg := mysqlDriver.NewConfig()
	cfg.User = instance.Username
	cfg.Passwd = instance.Password
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	if strings.TrimSpace(instance.Socket) != "" {
		cfg.Net = "unix"
		cfg.Addr = instance.Socket
	} else {
		cfg.Net = "tcp"
		cfg.Addr = "127.0.0.1:" + strconv.Itoa(instance.Port)
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	return db, nil
}

func ensureHeartbeatTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "create database if not exists gmha"); err != nil {
		return fmt.Errorf("ensure gmha schema failed: %w", err)
	}
	if _, err := db.ExecContext(ctx, "create table if not exists gmha.heartbeat (id bigint primary key, ts timestamp not null default current_timestamp on update current_timestamp) engine=InnoDB"); err != nil {
		return fmt.Errorf("ensure heartbeat table failed: %w", err)
	}
	if _, err := db.ExecContext(ctx, "insert ignore into gmha.heartbeat (id, ts) values (1, current_timestamp)"); err != nil {
		return fmt.Errorf("ensure heartbeat row failed: %w", err)
	}
	return nil
}

// LoadConfig 从指定路径加载 MySQL 心跳配置文件，返回配置内容和配置文件的 SHA256 哈希值。
func LoadConfig(path string) (Config, string, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, "", os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, "", err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", err
	}
	sum := sha256.Sum256(data)
	return cfg, hex.EncodeToString(sum[:]), nil
}

// UpsertInstance 向配置文件中插入或更新一个 MySQL 实例配置。
func UpsertInstance(path string, instance InstanceConfig) error {
	cfg, _, err := LoadConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	instance = normalizeInstance(instance)
	if instance.Port <= 0 && instance.Socket == "" {
		return errors.New("mysql heartbeat instance requires port or socket")
	}
	key := instanceKey(instance)
	replaced := false
	for i := range cfg.Instances {
		if instanceKey(normalizeInstance(cfg.Instances[i])) == key {
			cfg.Instances[i] = instance
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Instances = append(cfg.Instances, instance)
	}
	return writeConfig(path, cfg)
}

// EnsureInstance 确保指定 MySQL 实例的心跳表已创建。
func EnsureInstance(ctx context.Context, instance InstanceConfig) error {
	instance = normalizeInstance(instance)
	db, err := openDB(instance)
	if err != nil {
		return err
	}
	defer db.Close()
	ensureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return ensureHeartbeatTable(ensureCtx, db)
}

// RemoveInstance 从配置文件中删除指定的 MySQL 实例配置。
func RemoveInstance(path string, port int, socket string) error {
	cfg, _, err := LoadConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	target := instanceKey(normalizeInstance(InstanceConfig{Port: port, Socket: socket}))
	kept := cfg.Instances[:0]
	for _, item := range cfg.Instances {
		if instanceKey(normalizeInstance(item)) != target {
			kept = append(kept, item)
		}
	}
	cfg.Instances = kept
	if len(cfg.Instances) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeConfig(path, cfg)
}

func writeConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func normalizeInstance(instance InstanceConfig) InstanceConfig {
	instance.Username = strings.TrimSpace(instance.Username)
	instance.Socket = strings.TrimSpace(instance.Socket)
	instance.SystemdUnit = strings.TrimSpace(instance.SystemdUnit)
	if instance.Database == "" {
		instance.Database = "gmha"
	}
	if instance.SystemdUnit == "" {
		instance.SystemdUnit = "mysqld"
	}
	if instance.Port <= 0 {
		instance.Port = 3306
	}
	return instance
}

func instanceKey(instance InstanceConfig) string {
	if instance.Socket != "" {
		return "socket:" + instance.Socket
	}
	return "port:" + strconv.Itoa(instance.Port)
}

func instanceName(instance InstanceConfig) string {
	if instance.Port > 0 {
		return strconv.Itoa(instance.Port)
	}
	return strings.TrimPrefix(strings.ReplaceAll(instance.Socket, "/", "_"), "_")
}

func startMySQL(ctx context.Context, unit string) error {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		unit = "mysqld"
	}
	if strings.ContainsAny(unit, `/\ ;|&$<>(){}[]*?'"`+"\n\t") {
		return fmt.Errorf("invalid systemd unit: %s", unit)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "systemctl", "start", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Checker) markSuccess(key string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSuccess[key] = at
	delete(c.firstFailure, key)
}

func (c *Checker) reset(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if db := c.clients[key]; db != nil {
		_ = db.Close()
	}
	delete(c.clients, key)
	delete(c.ensured, key)
}

func (c *Checker) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeAllLocked()
}

func (c *Checker) closeAllLocked() {
	for key, db := range c.clients {
		_ = db.Close()
		delete(c.clients, key)
	}
	c.ensured = make(map[string]bool)
	c.lastSuccess = make(map[string]time.Time)
	c.firstFailure = make(map[string]time.Time)
	c.lastRecovery = make(map[string]time.Time)
}

func (c *Checker) closeStale(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, db := range c.clients {
		if seen[key] {
			continue
		}
		_ = db.Close()
		delete(c.clients, key)
		delete(c.ensured, key)
		delete(c.lastSuccess, key)
		delete(c.firstFailure, key)
		delete(c.lastRecovery, key)
	}
}
