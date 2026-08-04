package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	sqliteinfra "gmha/internal/infrastructure/persistence/sqlite"
)

var (
	ErrWALCleanupInProgress  = errors.New("WAL 清理正在执行，请勿重复提交")
	ErrWALCleanupUnsupported = errors.New("当前元数据库不支持 WAL 清理")
)

// DatabaseWALStatus describes the live Manager metadata database and its
// SQLite sidecar files. Byte counts are physical file sizes on the Manager
// host, not estimates from SQLite page counts.
type DatabaseWALStatus struct {
	Supported     bool      `json:"supported"`
	Reason        string    `json:"reason,omitempty"`
	Driver        string    `json:"driver"`
	JournalMode   string    `json:"journal_mode,omitempty"`
	DatabasePath  string    `json:"database_path,omitempty"`
	DatabaseBytes int64     `json:"database_bytes"`
	WALBytes      int64     `json:"wal_bytes"`
	SHMBytes      int64     `json:"shm_bytes"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DatabaseWALCleanupResult records the checkpoint result and the observable
// disk-space change. Completed is false when SQLite reports a busy reader.
type DatabaseWALCleanupResult struct {
	Completed          bool              `json:"completed"`
	Busy               int               `json:"busy"`
	LogFrames          int               `json:"log_frames"`
	CheckpointedFrames int               `json:"checkpointed_frames"`
	ReleasedBytes      int64             `json:"released_bytes"`
	Before             DatabaseWALStatus `json:"before"`
	After              DatabaseWALStatus `json:"after"`
	Message            string            `json:"message"`
}

// DatabaseMaintenanceService performs maintenance against the exact database
// handle used by the running Manager. This avoids deleting SQLite sidecar
// files behind an active connection.
type DatabaseMaintenanceService struct {
	db       *sql.DB
	dialect  sqliteinfra.Dialect
	driver   string
	filePath string
	cleaning atomic.Bool
}

func NewDatabaseMaintenanceService(db *sql.DB, dialect sqliteinfra.Dialect, cfg Config) *DatabaseMaintenanceService {
	driver := strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver))
	if driver == "" {
		driver = string(sqliteinfra.DialectSQLite)
	}
	if driver == "sqlite3" {
		driver = string(sqliteinfra.DialectSQLite)
	}
	filePath := ""
	if dialect == sqliteinfra.DialectSQLite {
		filePath = sqliteMaintenanceFilePath(cfg)
	}
	return &DatabaseMaintenanceService{
		db:       db,
		dialect:  dialect,
		driver:   driver,
		filePath: filePath,
	}
}

func (s *DatabaseMaintenanceService) WALStatus(ctx context.Context) (DatabaseWALStatus, error) {
	status := DatabaseWALStatus{
		Driver:       s.driver,
		DatabasePath: s.filePath,
		UpdatedAt:    time.Now().UTC(),
	}
	if s.dialect != sqliteinfra.DialectSQLite {
		status.Reason = "当前元数据库不是 SQLite，无 WAL 文件需要清理"
		return status, nil
	}
	if s.db == nil {
		return status, errors.New("数据库连接未初始化")
	}
	if s.filePath == "" {
		status.Reason = "当前 SQLite 使用内存或不可定位的数据源，无本地 WAL 文件可清理"
		return status, nil
	}
	if err := s.db.QueryRowContext(ctx, `pragma journal_mode`).Scan(&status.JournalMode); err != nil {
		return status, fmt.Errorf("读取 SQLite journal_mode 失败: %w", err)
	}
	status.JournalMode = strings.ToLower(strings.TrimSpace(status.JournalMode))
	if status.JournalMode != "wal" {
		status.Reason = fmt.Sprintf("当前 SQLite 日志模式为 %s，无 WAL 文件需要清理", status.JournalMode)
	} else {
		status.Supported = true
	}

	var err error
	if status.DatabaseBytes, err = maintenanceFileSize(s.filePath); err != nil {
		return status, fmt.Errorf("读取 SQLite 主库大小失败: %w", err)
	}
	if status.WALBytes, err = maintenanceFileSize(s.filePath + "-wal"); err != nil {
		return status, fmt.Errorf("读取 SQLite WAL 大小失败: %w", err)
	}
	if status.SHMBytes, err = maintenanceFileSize(s.filePath + "-shm"); err != nil {
		return status, fmt.Errorf("读取 SQLite SHM 大小失败: %w", err)
	}
	return status, nil
}

func (s *DatabaseMaintenanceService) CleanupWAL(ctx context.Context) (DatabaseWALCleanupResult, error) {
	if !s.cleaning.CompareAndSwap(false, true) {
		return DatabaseWALCleanupResult{}, ErrWALCleanupInProgress
	}
	defer s.cleaning.Store(false)

	before, err := s.WALStatus(ctx)
	if err != nil {
		return DatabaseWALCleanupResult{}, err
	}
	if !before.Supported {
		return DatabaseWALCleanupResult{}, fmt.Errorf("%w: %s", ErrWALCleanupUnsupported, before.Reason)
	}

	result := DatabaseWALCleanupResult{Before: before}
	if err := s.db.QueryRowContext(ctx, `pragma wal_checkpoint(truncate)`).Scan(
		&result.Busy,
		&result.LogFrames,
		&result.CheckpointedFrames,
	); err != nil {
		return DatabaseWALCleanupResult{}, fmt.Errorf("执行 SQLite WAL checkpoint 失败: %w", err)
	}
	after, err := s.WALStatus(ctx)
	if err != nil {
		return DatabaseWALCleanupResult{}, err
	}
	result.After = after
	result.ReleasedBytes = before.WALBytes - after.WALBytes
	if result.ReleasedBytes < 0 {
		result.ReleasedBytes = 0
	}
	result.Completed = result.Busy == 0
	if result.Completed {
		result.Message = "SQLite WAL checkpoint 已完成，日志文件已尝试截断"
	} else {
		result.Message = "WAL 清理未完成：存在活跃读取事务，请稍后重试"
	}
	return result, nil
}

func sqliteMaintenanceFilePath(cfg Config) string {
	dsn := strings.TrimSpace(cfg.DatabaseDSN)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.DBPath)
	}
	if dsn == "" {
		dsn = "./data/manager.db"
	}
	lower := strings.ToLower(dsn)
	if dsn == ":memory:" || strings.Contains(lower, "mode=memory") {
		return ""
	}
	path := dsn
	if strings.HasPrefix(strings.ToLower(path), "file:") {
		path = path[len("file:"):]
	}
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func maintenanceFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
