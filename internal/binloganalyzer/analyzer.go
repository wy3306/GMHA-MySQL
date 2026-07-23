// Package binloganalyzer analyzes MySQL row-based binary logs over the
// replication protocol. Its behavior is based on the public wy3306/bin2sql
// feature set, but it is integrated with GMHA's instance and credential model.
package binloganalyzer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	BigTransactionRows  = "rows"
	BigTransactionBytes = "bytes"

	maxStoredDMLEvents       = 20000
	maxStoredDDLEvents       = 5000
	maxStoredBigTransactions = 5000
)

var (
	errTimeOver  = errors.New("binlog timestamp is after requested end time")
	startFileRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
	ddlObjectRE  = regexp.MustCompile("(?i)^\\s*(?:CREATE|ALTER|DROP|TRUNCATE|RENAME)\\s+(?:ONLINE\\s+|TEMPORARY\\s+)?(?:TABLE|DATABASE|SCHEMA|INDEX|VIEW|TRIGGER|EVENT|PROCEDURE|FUNCTION)?\\s*(?:IF\\s+(?:NOT\\s+)?EXISTS\\s+)?((?:`[^`]+`|[A-Za-z0-9_$-]+)(?:\\s*\\.\\s*(?:`[^`]+`|[A-Za-z0-9_$-]+))?)")
	ddlStatement = map[string]bool{"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true}
)

// Config contains a single analysis request. Credentials are deliberately kept
// outside the HTTP-facing request models.
type Config struct {
	Host                 string
	Port                 int
	User                 string
	Password             string
	StartFile            string
	StartTime            time.Time
	EndTime              time.Time
	BigTxnMode           string
	BigTxnRowsThreshold  int
	BigTxnBytesThreshold uint64
}

type Progress struct {
	Phase           string    `json:"phase"`
	Message         string    `json:"message"`
	CurrentFile     string    `json:"current_file,omitempty"`
	FilesTotal      int       `json:"files_total"`
	FilesCompleted  int       `json:"files_completed"`
	EventsProcessed int64     `json:"events_processed"`
	Workers         int       `json:"workers"`
	LastEventTime   time.Time `json:"last_event_time,omitempty"`
}

type Summary struct {
	StartFile            string    `json:"start_file"`
	StartTime            time.Time `json:"start_time"`
	EndTime              time.Time `json:"end_time"`
	FilesAnalyzed        int       `json:"files_analyzed"`
	InsertRows           int       `json:"insert_rows"`
	UpdateRows           int       `json:"update_rows"`
	DeleteRows           int       `json:"delete_rows"`
	TotalRows            int       `json:"total_rows"`
	DMLEventCount        int       `json:"dml_event_count"`
	DDLCount             int       `json:"ddl_count"`
	BigTxnCount          int       `json:"big_txn_count"`
	BigTxnMode           string    `json:"big_txn_mode"`
	BigTxnRowsThreshold  int       `json:"big_txn_rows_threshold"`
	BigTxnBytesThreshold uint64    `json:"big_txn_bytes_threshold"`
	BucketSeconds        int       `json:"bucket_seconds"`
	DMLTruncated         bool      `json:"dml_truncated"`
	DDLTruncated         bool      `json:"ddl_truncated"`
}

type TimeBucket struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	InsertRows    int       `json:"insert_rows"`
	UpdateRows    int       `json:"update_rows"`
	DeleteRows    int       `json:"delete_rows"`
	TotalRows     int       `json:"total_rows"`
	DMLEventCount int       `json:"dml_event_count"`
	DDLCount      int       `json:"ddl_count"`
	BigTxnCount   int       `json:"big_txn_count"`
}

type DMLEvent struct {
	Time       time.Time `json:"time"`
	Schema     string    `json:"schema"`
	Table      string    `json:"table"`
	Type       string    `json:"type"`
	RowCount   int       `json:"row_count"`
	BinlogFile string    `json:"binlog_file"`
	GTID       string    `json:"gtid,omitempty"`
}

type DDLEvent struct {
	Time       time.Time `json:"time"`
	Schema     string    `json:"schema"`
	Object     string    `json:"object,omitempty"`
	Type       string    `json:"type"`
	Statement  string    `json:"statement"`
	BinlogFile string    `json:"binlog_file"`
	GTID       string    `json:"gtid,omitempty"`
}

type TableSummary struct {
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	InsertRows int    `json:"insert_rows"`
	UpdateRows int    `json:"update_rows"`
	DeleteRows int    `json:"delete_rows"`
	DDLCount   int    `json:"ddl_count"`
	TotalRows  int    `json:"total_rows"`
}

type BigTransaction struct {
	StartTime              time.Time  `json:"start_time"`
	EndTime                time.Time  `json:"end_time"`
	OriginalCommitTime     *time.Time `json:"original_commit_time,omitempty"`
	ImmediateCommitTime    *time.Time `json:"immediate_commit_time,omitempty"`
	ReplicationDelayMicros int64      `json:"replication_delay_micros,omitempty"`
	RowCount               int        `json:"row_count"`
	TransactionLength      uint64     `json:"transaction_length"`
	InsertRows             int        `json:"insert_rows"`
	UpdateRows             int        `json:"update_rows"`
	DeleteRows             int        `json:"delete_rows"`
	DDLCount               int        `json:"ddl_count"`
	BinlogFile             string     `json:"binlog_file"`
	GTID                   string     `json:"gtid,omitempty"`
	Tables                 []string   `json:"tables"`
}

type Result struct {
	Summary         Summary          `json:"summary"`
	Buckets         []TimeBucket     `json:"buckets"`
	DMLEvents       []DMLEvent       `json:"dml_events"`
	DDLEvents       []DDLEvent       `json:"ddl_events"`
	Tables          []TableSummary   `json:"tables"`
	BigTransactions []BigTransaction `json:"big_transactions"`
	AnalyzedFiles   []string         `json:"analyzed_files"`
}

type partialResult struct {
	result          Result
	tableStats      map[string]*TableSummary
	eventsProcessed int64
	lastEventTime   time.Time
}

type transactionState struct {
	active          bool
	start           time.Time
	originalCommit  time.Time
	immediateCommit time.Time
	gtid            string
	bytes           uint64
	rows            int
	inserts         int
	updates         int
	deletes         int
	ddl             int
	tables          map[string]struct{}
}

// Analyze streams and aggregates the selected binary logs.
func Analyze(ctx context.Context, cfg Config, emit func(Progress)) (*Result, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	cfg.BigTxnMode = normalizeMode(cfg.BigTxnMode)

	files, followRotates, err := resolveFiles(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("没有可分析的 Binlog 文件")
	}
	cfg.StartFile = files[0]

	bucketSize := chooseBucketSize(cfg.EndTime.Sub(cfg.StartTime))
	result := &Result{
		Summary: Summary{
			StartFile: files[0], StartTime: cfg.StartTime, EndTime: cfg.EndTime,
			BigTxnMode: cfg.BigTxnMode, BigTxnRowsThreshold: cfg.BigTxnRowsThreshold,
			BigTxnBytesThreshold: cfg.BigTxnBytesThreshold, BucketSeconds: int(bucketSize.Seconds()),
		},
		Buckets: makeBuckets(cfg.StartTime, cfg.EndTime, bucketSize),
	}

	workers := 1
	if !followRotates {
		workers = adaptiveWorkerCount(len(files))
	}
	progress := Progress{Phase: "preparing", Message: "正在定位 Binlog 时间范围", FilesTotal: len(files), Workers: workers}
	if emit != nil {
		emit(progress)
	}

	if followRotates {
		part, err := analyzeFile(ctx, cfg, files[0], true, bucketSize)
		if err != nil && !errors.Is(err, errTimeOver) {
			return nil, err
		}
		mergePartial(result, part)
		progress.FilesCompleted = 1
		progress.EventsProcessed = part.eventsProcessed
		progress.LastEventTime = part.lastEventTime
	} else {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		jobs := make(chan string)
		parts := make(chan struct {
			file string
			part partialResult
			err  error
		})
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for file := range jobs {
					part, analyzeErr := analyzeFile(ctx, cfg, file, false, bucketSize)
					select {
					case parts <- struct {
						file string
						part partialResult
						err  error
					}{file: file, part: part, err: analyzeErr}:
					case <-ctx.Done():
						return
					}
					if analyzeErr != nil && !errors.Is(analyzeErr, errTimeOver) {
						return
					}
				}
			}()
		}
		go func() {
			defer close(jobs)
			for _, file := range files {
				select {
				case jobs <- file:
				case <-ctx.Done():
					return
				}
			}
		}()
		go func() {
			wg.Wait()
			close(parts)
		}()

		var firstErr error
		for item := range parts {
			if item.err != nil && !errors.Is(item.err, errTimeOver) {
				if firstErr == nil {
					firstErr = fmt.Errorf("分析 %s 失败: %w", item.file, item.err)
					cancel()
				}
				continue
			}
			mergePartial(result, item.part)
			progress.FilesCompleted++
			progress.EventsProcessed += item.part.eventsProcessed
			if item.part.lastEventTime.After(progress.LastEventTime) {
				progress.LastEventTime = item.part.lastEventTime
			}
			progress.Phase = "running"
			progress.CurrentFile = item.file
			progress.Message = fmt.Sprintf("已完成 %d/%d 个 Binlog 文件", progress.FilesCompleted, len(files))
			if emit != nil {
				emit(progress)
			}
		}
		if firstErr != nil {
			return nil, firstErr
		}
	}

	finalize(result)
	progress.Phase = "completed"
	progress.Message = "分析完成"
	progress.CurrentFile = ""
	if emit != nil {
		emit(progress)
	}
	return result, nil
}

func ValidateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return errors.New("MySQL 地址不能为空")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("MySQL 端口必须在 1–65535 之间")
	}
	if strings.TrimSpace(cfg.User) == "" {
		return errors.New("MySQL 用户不能为空")
	}
	if cfg.StartTime.IsZero() || cfg.EndTime.IsZero() {
		return errors.New("开始时间和结束时间不能为空")
	}
	if !cfg.EndTime.After(cfg.StartTime) {
		return errors.New("结束时间必须晚于开始时间")
	}
	if cfg.EndTime.Sub(cfg.StartTime) > 7*24*time.Hour {
		return errors.New("单次分析时间范围不能超过 7 天")
	}
	if cfg.StartFile != "" && !startFileRE.MatchString(cfg.StartFile) {
		return errors.New("起始 Binlog 文件名格式不正确")
	}
	if normalizeMode(cfg.BigTxnMode) == BigTransactionRows {
		if cfg.BigTxnRowsThreshold < 0 {
			return errors.New("大事务行数阈值不能为负数")
		}
	} else if cfg.BigTxnBytesThreshold == 0 {
		return errors.New("按字节识别大事务时，字节阈值必须大于 0")
	}
	return nil
}

func resolveFiles(ctx context.Context, cfg Config) ([]string, bool, error) {
	files, err := listBinaryLogs(ctx, cfg)
	if err != nil {
		if cfg.StartFile != "" {
			return []string{cfg.StartFile}, true, nil
		}
		return nil, false, fmt.Errorf("读取 Binlog 文件列表失败: %w", err)
	}
	start := cfg.StartFile
	if start == "" {
		start, err = locateStartFile(ctx, cfg, files)
		if err != nil {
			return nil, false, err
		}
	}
	for index, file := range files {
		if file == start {
			return files[index:], false, nil
		}
	}
	return nil, false, fmt.Errorf("服务器上不存在起始 Binlog 文件 %q", start)
}

func listBinaryLogs(ctx context.Context, cfg Config) ([]string, error) {
	driverCfg := mysqldriver.NewConfig()
	driverCfg.User = cfg.User
	driverCfg.Passwd = cfg.Password
	driverCfg.Net = "tcp"
	driverCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	driverCfg.Timeout = 5 * time.Second
	driverCfg.ReadTimeout = 15 * time.Second
	db, err := sql.Open("mysql", driverCfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SHOW BINARY LOGS")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		columns, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		values := make([]sql.RawBytes, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		if len(values) > 0 {
			files = append(files, string(values[0]))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("实例没有可用的 Binlog 文件")
	}
	return files, nil
}

func locateStartFile(ctx context.Context, cfg Config, files []string) (string, error) {
	low, high, best := 0, len(files)-1, -1
	for low <= high {
		mid := low + (high-low)/2
		start, err := firstEventTime(ctx, cfg, files[mid])
		if err != nil {
			return "", fmt.Errorf("探测 %s 的起始时间失败: %w", files[mid], err)
		}
		if start.After(cfg.StartTime) {
			high = mid - 1
		} else {
			best = mid
			low = mid + 1
		}
	}
	if best < 0 {
		best = 0
	}
	return files[best], nil
}

func firstEventTime(ctx context.Context, cfg Config, file string) (time.Time, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	syncer := replication.NewBinlogSyncer(syncerConfig(cfg))
	defer syncer.Close()
	streamer, err := syncer.StartSync(mysql.Position{Name: file, Pos: 4})
	if err != nil {
		return time.Time{}, err
	}
	for {
		event, err := streamer.GetEvent(probeCtx)
		if err != nil {
			return time.Time{}, err
		}
		if event.Header.Timestamp > 0 {
			return eventTime(event.Header.Timestamp, cfg.StartTime.Location()), nil
		}
	}
}

func analyzeFile(ctx context.Context, cfg Config, file string, followRotates bool, bucketSize time.Duration) (partialResult, error) {
	part := newPartial(cfg, bucketSize)
	currentFile := file
	part.result.AnalyzedFiles = append(part.result.AnalyzedFiles, currentFile)
	position := uint32(4)
	tableMap := make(map[uint64][2]string)
	txn := transactionState{tables: make(map[string]struct{})}
	syncer := replication.NewBinlogSyncer(syncerConfig(cfg))
	defer syncer.Close()
	streamer, err := syncer.StartSync(mysql.Position{Name: currentFile, Pos: position})
	if err != nil {
		return part, fmt.Errorf("启动复制协议失败: %w", err)
	}

	for {
		event, err := streamer.GetEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return part, ctx.Err()
			}
			if time.Now().After(cfg.EndTime) && strings.Contains(strings.ToLower(err.Error()), "timeout") {
				finishTransaction(&part, cfg, &txn, part.lastEventTime, currentFile, bucketSize)
				return part, nil
			}
			return part, err
		}
		part.eventsProcessed++
		ts := eventTime(event.Header.Timestamp, cfg.StartTime.Location())
		if !ts.IsZero() {
			part.lastEventTime = ts
			if ts.After(cfg.EndTime) {
				finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
				return part, errTimeOver
			}
		}

		switch value := event.Event.(type) {
		case *replication.RotateEvent:
			next := string(value.NextLogName)
			if next == "" || next == currentFile {
				continue
			}
			if !followRotates {
				finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
				return part, nil
			}
			currentFile = next
			part.result.AnalyzedFiles = append(part.result.AnalyzedFiles, currentFile)
			tableMap = make(map[uint64][2]string)
		case *replication.GTIDEvent:
			finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
			startTransaction(&txn, ts)
			uuid := value.SID
			txn.gtid = fmt.Sprintf("%x-%x-%x-%x-%x:%d", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:], value.GNO)
			txn.bytes = value.TransactionLength
			txn.originalCommit = commitTimestamp(value.OriginalCommitTimestamp, cfg.StartTime.Location())
			txn.immediateCommit = commitTimestamp(value.ImmediateCommitTimestamp, cfg.StartTime.Location())
		case *replication.MariadbGTIDEvent:
			finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
			startTransaction(&txn, ts)
			txn.gtid = value.GTID.String()
		case *replication.TableMapEvent:
			tableMap[value.TableID] = [2]string{string(value.Schema), string(value.Table)}
		case *replication.RowsEvent:
			if ts.Before(cfg.StartTime) {
				continue
			}
			table, ok := tableMap[value.TableID]
			if !ok {
				continue
			}
			kind, ok := classifyDML(event.Header.EventType)
			if !ok {
				continue
			}
			rows := len(value.Rows)
			if kind == "UPDATE" {
				rows /= 2
			}
			if rows <= 0 {
				continue
			}
			if !txn.active {
				startTransaction(&txn, ts)
			}
			recordDML(&part, cfg, &txn, ts, currentFile, table[0], table[1], kind, rows, bucketSize)
		case *replication.QueryEvent:
			query := strings.TrimSpace(string(value.Query))
			upper := strings.ToUpper(query)
			switch upper {
			case "BEGIN":
				if !txn.active {
					startTransaction(&txn, ts)
				} else if txn.start.IsZero() {
					txn.start = ts
				}
			case "COMMIT", "ROLLBACK":
				finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
			default:
				if ts.Before(cfg.StartTime) {
					continue
				}
				if kind, ok := classifyDDL(query); ok {
					if !txn.active {
						startTransaction(&txn, ts)
					}
					recordDDL(&part, cfg, &txn, ts, currentFile, string(value.Schema), kind, query, bucketSize)
				}
			}
		case *replication.XIDEvent:
			finishTransaction(&part, cfg, &txn, ts, currentFile, bucketSize)
		}
	}
}

func syncerConfig(cfg Config) replication.BinlogSyncerConfig {
	return replication.BinlogSyncerConfig{
		ServerID: uint32(rand.Intn(900000) + 100000), Flavor: "mysql",
		Host: cfg.Host, Port: uint16(cfg.Port), User: cfg.User, Password: cfg.Password,
		ReadTimeout: 20 * time.Second, HeartbeatPeriod: 10 * time.Second,
		ParseTime: false, UseDecimal: false,
	}
}

func newPartial(cfg Config, bucketSize time.Duration) partialResult {
	return partialResult{
		result: Result{
			Summary: Summary{
				StartTime: cfg.StartTime, EndTime: cfg.EndTime, BigTxnMode: normalizeMode(cfg.BigTxnMode),
				BigTxnRowsThreshold: cfg.BigTxnRowsThreshold, BigTxnBytesThreshold: cfg.BigTxnBytesThreshold,
				BucketSeconds: int(bucketSize.Seconds()),
			},
			Buckets:       makeBuckets(cfg.StartTime, cfg.EndTime, bucketSize),
			AnalyzedFiles: []string{},
		},
		tableStats: make(map[string]*TableSummary),
	}
}

func startTransaction(txn *transactionState, start time.Time) {
	*txn = transactionState{active: true, start: start, tables: make(map[string]struct{})}
}

func finishTransaction(part *partialResult, cfg Config, txn *transactionState, end time.Time, file string, bucketSize time.Duration) {
	if !txn.active {
		return
	}
	if end.IsZero() {
		end = txn.start
	}
	if isBigTransaction(cfg, txn.rows, txn.bytes) && len(part.result.BigTransactions) < maxStoredBigTransactions {
		tables := make([]string, 0, len(txn.tables))
		for table := range txn.tables {
			tables = append(tables, table)
		}
		sort.Strings(tables)
		delayMicros := int64(0)
		if !txn.originalCommit.IsZero() && !txn.immediateCommit.Before(txn.originalCommit) {
			delayMicros = txn.immediateCommit.Sub(txn.originalCommit).Microseconds()
		}
		part.result.BigTransactions = append(part.result.BigTransactions, BigTransaction{
			StartTime: txn.start, EndTime: end,
			OriginalCommitTime: timePointer(txn.originalCommit), ImmediateCommitTime: timePointer(txn.immediateCommit),
			ReplicationDelayMicros: delayMicros, RowCount: txn.rows, TransactionLength: txn.bytes,
			InsertRows: txn.inserts, UpdateRows: txn.updates, DeleteRows: txn.deletes,
			DDLCount: txn.ddl, BinlogFile: file, GTID: txn.gtid, Tables: tables,
		})
		if index := bucketIndex(cfg.StartTime, end, bucketSize, len(part.result.Buckets)); index >= 0 {
			part.result.Buckets[index].BigTxnCount++
		}
	}
	startTransaction(txn, time.Time{})
	txn.active = false
}

func recordDML(part *partialResult, cfg Config, txn *transactionState, ts time.Time, file, schema, table, kind string, rows int, bucketSize time.Duration) {
	txn.rows += rows
	txn.tables[schema+"."+table] = struct{}{}
	switch kind {
	case "INSERT":
		txn.inserts += rows
	case "UPDATE":
		txn.updates += rows
	case "DELETE":
		txn.deletes += rows
	}
	summary := &part.result.Summary
	summary.TotalRows += rows
	summary.DMLEventCount++
	index := bucketIndex(cfg.StartTime, ts, bucketSize, len(part.result.Buckets))
	if index >= 0 {
		bucket := &part.result.Buckets[index]
		bucket.TotalRows += rows
		bucket.DMLEventCount++
		switch kind {
		case "INSERT":
			bucket.InsertRows += rows
		case "UPDATE":
			bucket.UpdateRows += rows
		case "DELETE":
			bucket.DeleteRows += rows
		}
	}
	key := schema + "." + table
	stat := part.tableStats[key]
	if stat == nil {
		stat = &TableSummary{Schema: schema, Table: table}
		part.tableStats[key] = stat
	}
	stat.TotalRows += rows
	switch kind {
	case "INSERT":
		summary.InsertRows += rows
		stat.InsertRows += rows
	case "UPDATE":
		summary.UpdateRows += rows
		stat.UpdateRows += rows
	case "DELETE":
		summary.DeleteRows += rows
		stat.DeleteRows += rows
	}
	if len(part.result.DMLEvents) < maxStoredDMLEvents {
		part.result.DMLEvents = append(part.result.DMLEvents, DMLEvent{Time: ts, Schema: schema, Table: table, Type: kind, RowCount: rows, BinlogFile: file, GTID: txn.gtid})
	} else {
		summary.DMLTruncated = true
	}
}

func recordDDL(part *partialResult, cfg Config, txn *transactionState, ts time.Time, file, schema, kind, statement string, bucketSize time.Duration) {
	object := ddlObject(statement)
	txn.ddl++
	if object != "" {
		txn.tables[schema+"."+object] = struct{}{}
	}
	part.result.Summary.DDLCount++
	if index := bucketIndex(cfg.StartTime, ts, bucketSize, len(part.result.Buckets)); index >= 0 {
		part.result.Buckets[index].DDLCount++
	}
	if object != "" {
		key := schema + "." + object
		stat := part.tableStats[key]
		if stat == nil {
			stat = &TableSummary{Schema: schema, Table: object}
			part.tableStats[key] = stat
		}
		stat.DDLCount++
	}
	if len(part.result.DDLEvents) < maxStoredDDLEvents {
		part.result.DDLEvents = append(part.result.DDLEvents, DDLEvent{
			Time: ts, Schema: schema, Object: object, Type: kind,
			Statement: compactSQL(statement, 2000), BinlogFile: file, GTID: txn.gtid,
		})
	} else {
		part.result.Summary.DDLTruncated = true
	}
}

func mergePartial(result *Result, part partialResult) {
	result.AnalyzedFiles = append(result.AnalyzedFiles, part.result.AnalyzedFiles...)
	result.Summary.InsertRows += part.result.Summary.InsertRows
	result.Summary.UpdateRows += part.result.Summary.UpdateRows
	result.Summary.DeleteRows += part.result.Summary.DeleteRows
	result.Summary.TotalRows += part.result.Summary.TotalRows
	result.Summary.DMLEventCount += part.result.Summary.DMLEventCount
	result.Summary.DDLCount += part.result.Summary.DDLCount
	result.Summary.DMLTruncated = result.Summary.DMLTruncated || part.result.Summary.DMLTruncated
	result.Summary.DDLTruncated = result.Summary.DDLTruncated || part.result.Summary.DDLTruncated
	for index := range result.Buckets {
		if index >= len(part.result.Buckets) {
			break
		}
		dst, src := &result.Buckets[index], part.result.Buckets[index]
		dst.InsertRows += src.InsertRows
		dst.UpdateRows += src.UpdateRows
		dst.DeleteRows += src.DeleteRows
		dst.TotalRows += src.TotalRows
		dst.DMLEventCount += src.DMLEventCount
		dst.DDLCount += src.DDLCount
		dst.BigTxnCount += src.BigTxnCount
	}
	appendLimitedDML(result, part.result.DMLEvents)
	appendLimitedDDL(result, part.result.DDLEvents)
	if remaining := maxStoredBigTransactions - len(result.BigTransactions); remaining > 0 {
		if len(part.result.BigTransactions) > remaining {
			part.result.BigTransactions = part.result.BigTransactions[:remaining]
		}
		result.BigTransactions = append(result.BigTransactions, part.result.BigTransactions...)
	}
	for key, source := range part.tableStats {
		target := findOrCreateTable(result, key, source.Schema, source.Table)
		target.InsertRows += source.InsertRows
		target.UpdateRows += source.UpdateRows
		target.DeleteRows += source.DeleteRows
		target.DDLCount += source.DDLCount
		target.TotalRows += source.TotalRows
	}
}

func appendLimitedDML(result *Result, items []DMLEvent) {
	remaining := maxStoredDMLEvents - len(result.DMLEvents)
	if remaining <= 0 {
		result.Summary.DMLTruncated = result.Summary.DMLTruncated || len(items) > 0
		return
	}
	if len(items) > remaining {
		items = items[:remaining]
		result.Summary.DMLTruncated = true
	}
	result.DMLEvents = append(result.DMLEvents, items...)
}

func appendLimitedDDL(result *Result, items []DDLEvent) {
	remaining := maxStoredDDLEvents - len(result.DDLEvents)
	if remaining <= 0 {
		result.Summary.DDLTruncated = result.Summary.DDLTruncated || len(items) > 0
		return
	}
	if len(items) > remaining {
		items = items[:remaining]
		result.Summary.DDLTruncated = true
	}
	result.DDLEvents = append(result.DDLEvents, items...)
}

func findOrCreateTable(result *Result, key, schema, table string) *TableSummary {
	for index := range result.Tables {
		if result.Tables[index].Schema+"."+result.Tables[index].Table == key {
			return &result.Tables[index]
		}
	}
	result.Tables = append(result.Tables, TableSummary{Schema: schema, Table: table})
	return &result.Tables[len(result.Tables)-1]
}

func finalize(result *Result) {
	result.AnalyzedFiles = uniqueSorted(result.AnalyzedFiles)
	result.Summary.FilesAnalyzed = len(result.AnalyzedFiles)
	result.Summary.BigTxnCount = len(result.BigTransactions)
	sort.Slice(result.Tables, func(i, j int) bool {
		if result.Tables[i].TotalRows == result.Tables[j].TotalRows {
			return result.Tables[i].DDLCount > result.Tables[j].DDLCount
		}
		return result.Tables[i].TotalRows > result.Tables[j].TotalRows
	})
	sort.Slice(result.DMLEvents, func(i, j int) bool { return result.DMLEvents[i].Time.Before(result.DMLEvents[j].Time) })
	sort.Slice(result.DDLEvents, func(i, j int) bool { return result.DDLEvents[i].Time.Before(result.DDLEvents[j].Time) })
	sort.Slice(result.BigTransactions, func(i, j int) bool {
		return result.BigTransactions[i].StartTime.Before(result.BigTransactions[j].StartTime)
	})
}

func adaptiveWorkerCount(fileCount int) int {
	if fileCount <= 1 {
		return 1
	}
	workers := runtime.NumCPU() / 4
	if workers < 2 {
		workers = 2
	}
	if workers > 12 {
		workers = 12
	}
	if load := currentLoadRatio(); load >= .7 {
		workers = 1
	} else if load >= .5 && workers > 2 {
		workers /= 2
	}
	if memoryUsage := currentMemoryRatio(); memoryUsage >= .8 {
		workers = 1
	}
	if workers > fileCount {
		workers = fileCount
	}
	return workers
}

func currentLoadRatio() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil || runtime.NumCPU() <= 0 {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	load, _ := strconv.ParseFloat(fields[0], 64)
	return load / float64(runtime.NumCPU())
}

func currentMemoryRatio() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, available float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, _ = strconv.ParseFloat(fields[1], 64)
		case "MemAvailable:":
			available, _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	if total <= 0 || available > total {
		return 0
	}
	return 1 - available/total
}

func chooseBucketSize(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return time.Minute
	case span <= 24*time.Hour:
		return 5 * time.Minute
	case span <= 72*time.Hour:
		return 15 * time.Minute
	case span <= 7*24*time.Hour:
		return time.Hour
	default:
		return 2 * time.Hour
	}
}

func makeBuckets(start, end time.Time, size time.Duration) []TimeBucket {
	var buckets []TimeBucket
	for cursor := start; cursor.Before(end); {
		next := cursor.Add(size)
		if next.After(end) {
			next = end
		}
		buckets = append(buckets, TimeBucket{Start: cursor, End: next})
		cursor = next
	}
	if len(buckets) == 0 {
		buckets = append(buckets, TimeBucket{Start: start, End: end})
	}
	return buckets
}

func bucketIndex(start, target time.Time, size time.Duration, total int) int {
	if target.Before(start) || total == 0 {
		return -1
	}
	index := int(target.Sub(start) / size)
	if index >= total {
		return total - 1
	}
	return index
}

func eventTime(timestamp uint32, location *time.Location) time.Time {
	if timestamp == 0 {
		return time.Time{}
	}
	if location == nil {
		location = time.Local
	}
	return time.Unix(int64(timestamp), 0).In(location)
}

func commitTimestamp(microseconds uint64, location *time.Location) time.Time {
	if microseconds == 0 {
		return time.Time{}
	}
	if location == nil {
		location = time.Local
	}
	seconds := int64(microseconds / 1_000_000)
	nanoseconds := int64(microseconds%1_000_000) * 1_000
	return time.Unix(seconds, nanoseconds).In(location)
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func normalizeMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), BigTransactionBytes) {
		return BigTransactionBytes
	}
	return BigTransactionRows
}

func isBigTransaction(cfg Config, rows int, bytes uint64) bool {
	if normalizeMode(cfg.BigTxnMode) == BigTransactionBytes {
		return cfg.BigTxnBytesThreshold > 0 && bytes >= cfg.BigTxnBytesThreshold
	}
	return cfg.BigTxnRowsThreshold > 0 && rows >= cfg.BigTxnRowsThreshold
}

func classifyDML(eventType replication.EventType) (string, bool) {
	switch eventType {
	case replication.WRITE_ROWS_EVENTv0, replication.WRITE_ROWS_EVENTv1, replication.WRITE_ROWS_EVENTv2:
		return "INSERT", true
	case replication.UPDATE_ROWS_EVENTv0, replication.UPDATE_ROWS_EVENTv1, replication.UPDATE_ROWS_EVENTv2:
		return "UPDATE", true
	case replication.DELETE_ROWS_EVENTv0, replication.DELETE_ROWS_EVENTv1, replication.DELETE_ROWS_EVENTv2:
		return "DELETE", true
	default:
		return "", false
	}
}

func classifyDDL(statement string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(statement))
	if len(fields) == 0 {
		return "", false
	}
	kind := strings.ToUpper(fields[0])
	return kind, ddlStatement[kind]
}

func ddlObject(statement string) string {
	match := ddlObjectRE.FindStringSubmatch(statement)
	if len(match) < 2 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	if dot := strings.LastIndex(value, "."); dot >= 0 {
		value = strings.TrimSpace(value[dot+1:])
	}
	return strings.Trim(value, "`")
}

func compactSQL(statement string, limit int) string {
	text := strings.Join(strings.Fields(statement), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}

func uniqueSorted(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
