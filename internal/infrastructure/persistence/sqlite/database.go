package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Dialect 表示 Manager 元数据存储所使用的 SQL 方言。
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
)

// DB 为既有仓储提供统一的数据库访问入口，并在调用处转换方言差异。
// 业务仓储只维护一份，SQLite、MySQL 和 PostgreSQL 使用相同的数据模型。
type DB struct {
	db      *sql.DB
	dialect Dialect
}

func NewDB(db *sql.DB, dialect Dialect) *DB {
	return &DB{db: db, dialect: dialect}
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	if d.dialect == DialectMySQL && len(args) == 0 {
		return d.execMySQLMigration(query)
	}
	return d.db.Exec(d.sql(query), args...)
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, d.sql(query), args...)
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, d.sql(query), args...)
}

func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.db.Query(d.sql(query), args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, d.sql(query), args...)
}

func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := d.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, db: d}, nil
}

// Tx 是带有同一方言转换规则的事务包装器。
type Tx struct {
	tx *sql.Tx
	db *DB
}

func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.db.sql(query), args...)
}
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.db.sql(query), args...)
}
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.db.sql(query), args...)
}
func (t *Tx) Commit() error   { return t.tx.Commit() }
func (t *Tx) Rollback() error { return t.tx.Rollback() }

var excludedColumn = regexp.MustCompile(`(?i)excluded\.([a-z_][a-z0-9_]*)`)
var conflictUpdate = regexp.MustCompile(`(?is)on\s+conflict\s*\([^)]*\)\s*do\s+update\s+set`)
var conflictNothing = regexp.MustCompile(`(?is)on\s+conflict\s*\([^)]*\)\s*do\s+nothing`)
var mysqlIndexIfNotExists = regexp.MustCompile(`(?i)create\s+(unique\s+)?index\s+if\s+not\s+exists`)
var mysqlKeyTextColumn = regexp.MustCompile(`(?i)\b(id|role|name|cluster_id|failover_id|run_id|machine_id|agent_id|fingerprint|kind|ip|parent_task_id|task_id|created_at|current_state|collected_at|status|metric|last_seen_at|delivered_at|next_run_at|cluster_name|vip_address|interface_name)\s+text\b`)
var mysqlTextNotNullDefault = regexp.MustCompile(`(?i)\btext\s+not\s+null\s+default\s+'(?:''|[^'])*'`)
var mysqlTextDefault = regexp.MustCompile(`(?i)\btext\s+default\s+'(?:''|[^'])*'`)
var mysqlCastInteger = regexp.MustCompile(`(?i)\bas\s+integer\s*\)`)
var mysqlReservedIdentifier = regexp.MustCompile(`(?i)\b(last_value)\b`)

func (d *DB) sql(query string) string {
	if d.dialect == DialectSQLite {
		return query
	}
	query = strings.ReplaceAll(query, "integer primary key autoincrement", d.autoIncrement())
	if d.dialect == DialectMySQL {
		query = mysqlIndexIfNotExists.ReplaceAllString(query, "create ${1}index")
		// MySQL does not allow TEXT to use CURRENT_TIMESTAMP as a default.
		query = strings.ReplaceAll(query, "text default CURRENT_TIMESTAMP", "varchar(64) not null default ''")
		query = mysqlKeyTextColumn.ReplaceAllString(query, "${1} varchar(191)")
		if conflictNothing.MatchString(query) {
			query = conflictNothing.ReplaceAllString(query, "")
			query = strings.Replace(query, "insert into", "insert ignore into", 1)
		}
		query = conflictUpdate.ReplaceAllString(query, "on duplicate key update")
		query = excludedColumn.ReplaceAllString(query, "values($1)")
		// MySQL 不允许 TEXT 使用 CURRENT_TIMESTAMP 默认值；这些字段仅记录审计时间，
		// 实际写入时由应用层覆盖，因此以空字符串作为建表默认值。
		// MySQL 5.7 forbids defaults on TEXT/BLOB. Large payload columns keep a
		// LONGTEXT type and are always populated explicitly by repositories.
		query = mysqlTextNotNullDefault.ReplaceAllString(query, "longtext not null")
		query = mysqlTextDefault.ReplaceAllString(query, "longtext")
		query = mysqlCastInteger.ReplaceAllString(query, "as signed)")
		// LAST_VALUE is a window-function keyword in current MySQL releases.
		// The alert state schema predates that reservation, so quote the existing
		// column consistently in migrations and CRUD without changing its API.
		query = mysqlReservedIdentifier.ReplaceAllString(query, "`$1`")
		return query
	}
	return bindPostgres(query)
}

func (d *DB) autoIncrement() string {
	if d.dialect == DialectMySQL {
		return "bigint auto_increment primary key"
	}
	return "bigserial primary key"
}

// go-sql-driver/mysql 默认禁止一条请求执行多条 SQL，而现有迁移会在一个
// Exec 中创建表和索引。仅迁移阶段（无参数）按分号拆分，避免要求用户在 DSN
// 中额外开启 multiStatements=true。
func (d *DB) execMySQLMigration(query string) (sql.Result, error) {
	var last sql.Result
	for _, statement := range strings.Split(d.sql(query), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		statement = mysqlMigrationStatement(statement)
		result, err := d.db.Exec(statement)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate key name") {
				continue // MySQL 没有通用的 CREATE INDEX IF NOT EXISTS。
			}
			return nil, err
		}
		last = result
	}
	return last, nil
}

func mysqlMigrationStatement(statement string) string {
	if strings.HasPrefix(strings.ToLower(statement), "create table ") && !strings.Contains(strings.ToLower(statement), "character set") {
		return statement + " default character set utf8mb4 collate utf8mb4_unicode_ci"
	}
	return statement
}

// bindPostgres 将仓储统一使用的 ? 参数占位符转换为 PostgreSQL 的 $n 格式。
func bindPostgres(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 16)
	index := 1
	inQuote := false
	for _, ch := range query {
		if ch == '\'' {
			inQuote = !inQuote
		}
		if ch == '?' && !inQuote {
			_, _ = fmt.Fprintf(&b, "$%d", index)
			index++
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}
