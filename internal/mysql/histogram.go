package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	mysqlDriver "github.com/go-sql-driver/mysql"
	sqldomain "gmha/internal/domain/sqldiagnostic"
)

const (
	DefaultHistogramBuckets = 100
	MaxHistogramBuckets     = 1024
)

var ErrHistogramVersionUnsupported = errors.New("mysql version does not support histogram management")

// HistogramCatalog is the metadata required by the instance-management UI.
// Filters are progressive: schemas are always returned, tables require a
// schema, and columns require both a schema and table.
type HistogramCatalog struct {
	Instance       sqldomain.Instance `json:"instance"`
	ServerVersion  string             `json:"server_version"`
	VersionComment string             `json:"version_comment"`
	Supported      bool               `json:"supported"`
	Schemas        []string           `json:"schemas"`
	Tables         []HistogramTable   `json:"tables"`
	Columns        []HistogramColumn  `json:"columns"`
	Histograms     []Histogram        `json:"histograms"`
}

type HistogramTable struct {
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	EstimatedRows uint64 `json:"estimated_rows"`
}

type HistogramColumn struct {
	Name               string `json:"name"`
	DataType           string `json:"data_type"`
	ColumnType         string `json:"column_type"`
	Nullable           bool   `json:"nullable"`
	Indexed            bool   `json:"indexed"`
	SingleColumnUnique bool   `json:"single_column_unique"`
	Eligible           bool   `json:"eligible"`
	IneligibleReason   string `json:"ineligible_reason,omitempty"`
	HasHistogram       bool   `json:"has_histogram"`
}

type Histogram struct {
	Schema           string          `json:"schema"`
	Table            string          `json:"table"`
	Column           string          `json:"column"`
	HistogramType    string          `json:"histogram_type"`
	DataType         string          `json:"data_type"`
	BucketCount      int             `json:"bucket_count"`
	SpecifiedBuckets int             `json:"specified_buckets"`
	NullValues       float64         `json:"null_values"`
	SamplingRate     float64         `json:"sampling_rate"`
	LastUpdated      string          `json:"last_updated"`
	Raw              json.RawMessage `json:"raw"`
}

type HistogramOperationResult struct {
	Action        string             `json:"action"`
	Instance      sqldomain.Instance `json:"instance"`
	ServerVersion string             `json:"server_version"`
	Schema        string             `json:"schema"`
	Table         string             `json:"table"`
	Columns       []string           `json:"columns"`
	Buckets       int                `json:"buckets,omitempty"`
	Messages      []HistogramMessage `json:"messages"`
}

type HistogramMessage struct {
	Table   string `json:"table"`
	Op      string `json:"op"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// HistogramManager is implemented by HistogramClient and kept at the package
// boundary so application-service version gates can be tested without a live
// MySQL server.
type HistogramManager interface {
	Inspect(context.Context, sqldomain.Instance, DiagnosticCredential, string, string) (HistogramCatalog, error)
	Update(context.Context, sqldomain.Instance, DiagnosticCredential, string, string, []string, int) (HistogramOperationResult, error)
	Drop(context.Context, sqldomain.Instance, DiagnosticCredential, string, string, []string) (HistogramOperationResult, error)
}

type HistogramClient struct {
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

func (c HistogramClient) Inspect(ctx context.Context, instance sqldomain.Instance, credential DiagnosticCredential, schema, table string) (HistogramCatalog, error) {
	if table != "" && schema == "" {
		return HistogramCatalog{}, errors.New("table requires schema")
	}
	if schema != "" {
		if err := validateHistogramIdentifier("schema", schema); err != nil {
			return HistogramCatalog{}, err
		}
	}
	if table != "" {
		if err := validateHistogramIdentifier("table", table); err != nil {
			return HistogramCatalog{}, err
		}
	}
	db, err := c.open(instance, credential)
	if err != nil {
		return HistogramCatalog{}, err
	}
	defer db.Close()
	version, comment, err := c.serverVersion(ctx, db)
	if err != nil {
		return HistogramCatalog{}, err
	}
	if !SupportsHistogramForVersion(version) {
		return HistogramCatalog{}, fmt.Errorf("%w: MySQL %s 不支持直方图管理；该功能仅支持 MySQL 8.0 及以上版本，不兼容 MySQL 5.7", ErrHistogramVersionUnsupported, version)
	}
	result := HistogramCatalog{
		Instance: instance, ServerVersion: version, VersionComment: comment, Supported: true,
		Schemas: []string{}, Tables: []HistogramTable{}, Columns: []HistogramColumn{}, Histograms: []Histogram{},
	}
	if result.Schemas, err = c.schemas(ctx, db); err != nil {
		return HistogramCatalog{}, err
	}
	if schema != "" {
		if result.Tables, err = c.tables(ctx, db, schema); err != nil {
			return HistogramCatalog{}, err
		}
	}
	if table != "" {
		if result.Columns, err = c.columns(ctx, db, schema, table); err != nil {
			return HistogramCatalog{}, err
		}
	}
	if result.Histograms, err = c.histograms(ctx, db, schema, table); err != nil {
		return HistogramCatalog{}, err
	}
	byColumn := make(map[string]bool, len(result.Histograms))
	for _, item := range result.Histograms {
		if item.Schema == schema && item.Table == table {
			byColumn[item.Column] = true
		}
	}
	for index := range result.Columns {
		result.Columns[index].HasHistogram = byColumn[result.Columns[index].Name]
	}
	return result, nil
}

func (c HistogramClient) Update(ctx context.Context, instance sqldomain.Instance, credential DiagnosticCredential, schema, table string, columns []string, buckets int) (HistogramOperationResult, error) {
	if buckets < 1 || buckets > MaxHistogramBuckets {
		return HistogramOperationResult{}, fmt.Errorf("buckets must be between 1 and %d", MaxHistogramBuckets)
	}
	return c.manage(ctx, instance, credential, "update", schema, table, columns, buckets)
}

func (c HistogramClient) Drop(ctx context.Context, instance sqldomain.Instance, credential DiagnosticCredential, schema, table string, columns []string) (HistogramOperationResult, error) {
	return c.manage(ctx, instance, credential, "drop", schema, table, columns, 0)
}

func (c HistogramClient) manage(ctx context.Context, instance sqldomain.Instance, credential DiagnosticCredential, action, schema, table string, columns []string, buckets int) (HistogramOperationResult, error) {
	quotedSchema, quotedTable, quotedColumns, err := histogramIdentifiers(schema, table, columns)
	if err != nil {
		return HistogramOperationResult{}, err
	}
	db, err := c.open(instance, credential)
	if err != nil {
		return HistogramOperationResult{}, err
	}
	defer db.Close()
	version, _, err := c.serverVersion(ctx, db)
	if err != nil {
		return HistogramOperationResult{}, err
	}
	if !SupportsHistogramForVersion(version) {
		return HistogramOperationResult{}, fmt.Errorf("%w: MySQL %s 不支持直方图管理；该功能仅支持 MySQL 8.0 及以上版本，不兼容 MySQL 5.7", ErrHistogramVersionUnsupported, version)
	}
	statement := "ANALYZE NO_WRITE_TO_BINLOG TABLE " + quotedSchema + "." + quotedTable
	if action == "update" {
		statement += " UPDATE HISTOGRAM ON " + strings.Join(quotedColumns, ", ") + fmt.Sprintf(" WITH %d BUCKETS", buckets)
	} else {
		statement += " DROP HISTOGRAM ON " + strings.Join(quotedColumns, ", ")
	}
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, statement)
	if err != nil {
		return HistogramOperationResult{}, err
	}
	defer rows.Close()
	result := HistogramOperationResult{
		Action: action, Instance: instance, ServerVersion: version, Schema: schema,
		Table: table, Columns: append([]string(nil), columns...), Buckets: buckets, Messages: []HistogramMessage{},
	}
	for rows.Next() {
		var item HistogramMessage
		if err := rows.Scan(&item.Table, &item.Op, &item.Type, &item.Message); err != nil {
			return HistogramOperationResult{}, err
		}
		result.Messages = append(result.Messages, item)
	}
	if err := rows.Err(); err != nil {
		return HistogramOperationResult{}, err
	}
	for _, item := range result.Messages {
		if strings.EqualFold(item.Type, "error") {
			return result, errors.New(item.Message)
		}
	}
	return result, nil
}

func (c HistogramClient) open(instance sqldomain.Instance, credential DiagnosticCredential) (*sql.DB, error) {
	if strings.TrimSpace(instance.MachineIP) == "" || instance.Port <= 0 {
		return nil, errors.New("invalid mysql histogram endpoint")
	}
	if strings.TrimSpace(credential.Username) == "" || credential.Password == "" {
		return nil, errors.New("mysql histogram credential is not configured")
	}
	connectTimeout := c.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 3 * time.Second
	}
	queryTimeout := c.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = 2 * time.Minute
	}
	cfg := mysqlDriver.NewConfig()
	cfg.User, cfg.Passwd = credential.Username, credential.Password
	cfg.Net, cfg.Addr = "tcp", fmt.Sprintf("%s:%d", instance.MachineIP, instance.Port)
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = connectTimeout, queryTimeout, queryTimeout
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	return db, nil
}

func (c HistogramClient) serverVersion(ctx context.Context, db *sql.DB) (string, string, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	var version, comment string
	if err := db.QueryRowContext(queryCtx, `SELECT @@version, @@version_comment`).Scan(&version, &comment); err != nil {
		return "", "", err
	}
	return version, comment, nil
}

func (c HistogramClient) schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys')
		ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func (c HistogramClient) tables(ctx context.Context, db *sql.DB, schema string) ([]HistogramTable, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		SELECT table_name, COALESCE(engine,''), COALESCE(table_rows,0)
		FROM information_schema.tables
		WHERE table_schema=? AND table_type='BASE TABLE'
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]HistogramTable, 0)
	for rows.Next() {
		var item HistogramTable
		if err := rows.Scan(&item.Name, &item.Engine, &item.EstimatedRows); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (c HistogramClient) columns(ctx context.Context, db *sql.DB, schema, table string) ([]HistogramColumn, error) {
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `
		SELECT c.column_name, c.data_type, c.column_type, c.is_nullable,
			EXISTS(
				SELECT 1 FROM information_schema.statistics s
				WHERE s.table_schema=c.table_schema AND s.table_name=c.table_name
					AND s.column_name=c.column_name
			) AS indexed,
			EXISTS(
				SELECT 1 FROM information_schema.statistics s
				WHERE s.table_schema=c.table_schema AND s.table_name=c.table_name
					AND s.column_name=c.column_name AND s.non_unique=0
					AND (SELECT COUNT(*) FROM information_schema.statistics s2
						WHERE s2.table_schema=s.table_schema AND s2.table_name=s.table_name
							AND s2.index_name=s.index_name)=1
			) AS single_column_unique
		FROM information_schema.columns c
		JOIN information_schema.tables t
			ON t.table_schema=c.table_schema AND t.table_name=c.table_name AND t.table_type='BASE TABLE'
		WHERE c.table_schema=? AND c.table_name=?
		ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]HistogramColumn, 0)
	for rows.Next() {
		var item HistogramColumn
		var nullable string
		if err := rows.Scan(&item.Name, &item.DataType, &item.ColumnType, &nullable, &item.Indexed, &item.SingleColumnUnique); err != nil {
			return nil, err
		}
		item.Nullable = strings.EqualFold(nullable, "YES")
		item.Eligible, item.IneligibleReason = histogramColumnEligibility(item.DataType, item.SingleColumnUnique)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (c HistogramClient) histograms(ctx context.Context, db *sql.DB, schema, table string) ([]Histogram, error) {
	statement := `SELECT schema_name, table_name, column_name, CAST(histogram AS CHAR)
		FROM information_schema.column_statistics WHERE 1=1`
	var args []any
	if schema != "" {
		statement += " AND schema_name=?"
		args = append(args, schema)
	}
	if table != "" {
		statement += " AND table_name=?"
		args = append(args, table)
	}
	statement += " ORDER BY schema_name, table_name, column_name"
	queryCtx, cancel := c.queryContext(ctx)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Histogram, 0)
	for rows.Next() {
		var item Histogram
		var raw []byte
		if err := rows.Scan(&item.Schema, &item.Table, &item.Column, &raw); err != nil {
			return nil, err
		}
		item.Raw = append(json.RawMessage(nil), raw...)
		var payload struct {
			Buckets          []json.RawMessage `json:"buckets"`
			DataType         string            `json:"data-type"`
			NullValues       float64           `json:"null-values"`
			SamplingRate     float64           `json:"sampling-rate"`
			HistogramType    string            `json:"histogram-type"`
			SpecifiedBuckets int               `json:"number-of-buckets-specified"`
			LastUpdated      string            `json:"last-updated"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("decode histogram %s.%s.%s: %w", item.Schema, item.Table, item.Column, err)
		}
		item.BucketCount, item.DataType = len(payload.Buckets), payload.DataType
		item.NullValues, item.SamplingRate = payload.NullValues, payload.SamplingRate
		item.HistogramType, item.SpecifiedBuckets, item.LastUpdated = payload.HistogramType, payload.SpecifiedBuckets, payload.LastUpdated
		result = append(result, item)
	}
	return result, rows.Err()
}

func (c HistogramClient) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.QueryTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return context.WithTimeout(ctx, timeout)
}

func histogramColumnEligibility(dataType string, singleColumnUnique bool) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "json":
		return false, "JSON 列不支持直方图"
	case "geometry", "point", "linestring", "polygon", "multipoint", "multilinestring", "multipolygon", "geometrycollection":
		return false, "空间数据类型不支持直方图"
	}
	if singleColumnUnique {
		return false, "单列唯一索引已提供精确基数，无需直方图"
	}
	return true, ""
}

func histogramIdentifiers(schema, table string, columns []string) (string, string, []string, error) {
	if err := validateHistogramIdentifier("schema", schema); err != nil {
		return "", "", nil, err
	}
	if err := validateHistogramIdentifier("table", table); err != nil {
		return "", "", nil, err
	}
	if len(columns) == 0 {
		return "", "", nil, errors.New("at least one column is required")
	}
	if len(columns) > 64 {
		return "", "", nil, errors.New("at most 64 columns may be managed at once")
	}
	seen := make(map[string]bool, len(columns))
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		if err := validateHistogramIdentifier("column", column); err != nil {
			return "", "", nil, err
		}
		if seen[column] {
			return "", "", nil, fmt.Errorf("duplicate column %q", column)
		}
		seen[column] = true
		quoted = append(quoted, quoteHistogramIdentifier(column))
	}
	return quoteHistogramIdentifier(schema), quoteHistogramIdentifier(table), quoted, nil
}

func validateHistogramIdentifier(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", kind)
	}
	if utf8.RuneCountInString(value) > 64 {
		return fmt.Errorf("%s exceeds MySQL's 64-character identifier limit", kind)
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s contains an invalid NUL byte", kind)
	}
	return nil
}

func quoteHistogramIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
