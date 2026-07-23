package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sqldomain "gmha/internal/domain/sqldiagnostic"
)

const MaxExplainSQLBytes = 64 * 1024

// ExecutionPlan is the ordered, tabular result returned by MySQL EXPLAIN.
// Columns is kept separately because JSON object key order is not stable.
type ExecutionPlan struct {
	Instance    sqldomain.Instance `json:"instance"`
	Database    string             `json:"database,omitempty"`
	SQL         string             `json:"sql"`
	Columns     []string           `json:"columns"`
	Rows        []map[string]any   `json:"rows"`
	GeneratedAt time.Time          `json:"generated_at"`
}

type ExecutionPlanExplainer interface {
	Explain(context.Context, sqldomain.Instance, DiagnosticCredential, string, string) (ExecutionPlan, error)
}

type ExecutionPlanClient struct {
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

func (c ExecutionPlanClient) Explain(ctx context.Context, instance sqldomain.Instance, credential DiagnosticCredential, database, statement string) (ExecutionPlan, error) {
	statement, err := NormalizeExplainStatement(statement)
	if err != nil {
		return ExecutionPlan{}, err
	}
	database = strings.TrimSpace(database)
	if database != "" && !safeMySQLIdentifier(database) {
		return ExecutionPlan{}, errors.New("数据库名称只能包含字母、数字、下划线、$ 或合法的 Unicode 字符")
	}

	client := DiagnosticClient{ConnectTimeout: c.ConnectTimeout, QueryTimeout: c.QueryTimeout}
	db, err := client.Open(instance, credential)
	if err != nil {
		return ExecutionPlan{}, err
	}
	defer db.Close()

	queryTimeout := c.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = 30 * time.Second
	}
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	conn, err := db.Conn(queryCtx)
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("open mysql explain connection: %w", err)
	}
	defer conn.Close()
	if database != "" {
		if _, err := conn.ExecContext(queryCtx, "USE "+quoteMySQLIdentifier(database)); err != nil {
			return ExecutionPlan{}, fmt.Errorf("select database %s: %w", database, err)
		}
	}
	rows, err := conn.QueryContext(queryCtx, "EXPLAIN "+statement)
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("explain sql: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("read explain columns: %w", err)
	}
	items, err := scanExecutionPlanRows(rows, columns)
	if err != nil {
		return ExecutionPlan{}, err
	}
	return ExecutionPlan{
		Instance: instance, Database: database, SQL: statement,
		Columns: columns, Rows: items, GeneratedAt: time.Now().UTC(),
	}, nil
}

func scanExecutionPlanRows(rows *sql.Rows, columns []string) ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan explain row: %w", err)
		}
		item := make(map[string]any, len(columns))
		for index, column := range columns {
			switch value := values[index].(type) {
			case []byte:
				item[column] = string(value)
			default:
				item[column] = value
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read explain rows: %w", err)
	}
	return items, nil
}

// NormalizeExplainStatement accepts one explainable statement and deliberately
// rejects EXPLAIN itself so callers can never request EXPLAIN ANALYZE.
func NormalizeExplainStatement(statement string) (string, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return "", errors.New("请输入需要分析的 SQL")
	}
	if len(statement) > MaxExplainSQLBytes {
		return "", fmt.Errorf("SQL 不能超过 %d KB", MaxExplainSQLBytes/1024)
	}
	if strings.HasSuffix(statement, ";") {
		statement = strings.TrimSpace(strings.TrimSuffix(statement, ";"))
	}
	if hasUnquotedSemicolon(statement) {
		return "", errors.New("一次只能分析一条 SQL")
	}
	keyword := strings.ToUpper(firstSQLKeyword(statement))
	switch keyword {
	case "SELECT", "WITH", "INSERT", "UPDATE", "DELETE", "REPLACE", "TABLE":
		return statement, nil
	case "EXPLAIN", "DESC", "DESCRIBE":
		return "", errors.New("请输入原始 SQL，系统会自动添加安全的 EXPLAIN")
	default:
		return "", errors.New("仅支持 SELECT、WITH、INSERT、UPDATE、DELETE、REPLACE 或 TABLE 语句的执行计划")
	}
}

func firstSQLKeyword(statement string) string {
	rest := strings.TrimSpace(statement)
	for {
		switch {
		case strings.HasPrefix(rest, "/*"):
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				return ""
			}
			rest = strings.TrimSpace(rest[end+4:])
		case strings.HasPrefix(rest, "--"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				return ""
			}
			rest = strings.TrimSpace(rest[end+1:])
		case strings.HasPrefix(rest, "#"):
			end := strings.IndexByte(rest, '\n')
			if end < 0 {
				return ""
			}
			rest = strings.TrimSpace(rest[end+1:])
		default:
			if index := strings.IndexAny(rest, " \t\r\n("); index >= 0 {
				return rest[:index]
			}
			return rest
		}
	}
}

func hasUnquotedSemicolon(statement string) bool {
	var quote byte
	lineComment, blockComment := false, false
	for index := 0; index < len(statement); index++ {
		current := statement[index]
		next := byte(0)
		if index+1 < len(statement) {
			next = statement[index+1]
		}
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && next == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if current == '\\' {
				index++
				continue
			}
			if current == quote {
				if next == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		switch {
		case current == '\'' || current == '"' || current == '`':
			quote = current
		case current == '-' && next == '-':
			lineComment = true
			index++
		case current == '#':
			lineComment = true
		case current == '/' && next == '*':
			blockComment = true
			index++
		case current == ';':
			return true
		}
	}
	return false
}

func safeMySQLIdentifier(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) {
		return false
	}
	for _, char := range value {
		if char == '_' || char == '$' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char > 127 {
			continue
		}
		return false
	}
	return true
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
