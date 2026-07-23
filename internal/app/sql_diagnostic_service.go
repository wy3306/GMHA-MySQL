package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	machinedomain "gmha/internal/domain/machine"
	sqldomain "gmha/internal/domain/sqldiagnostic"
	mysqlapp "gmha/internal/mysql"
)

var (
	ErrSQLDiagnosticConflict  = errors.New("SQL 查杀目标已变化")
	ErrSQLDiagnosticForbidden = errors.New("SQL 查杀操作被拒绝")
	ErrSQLExplainInvalid      = errors.New("执行计划请求无效")
)

type SQLDiagnosticRepository interface {
	LoadConfig(ctx context.Context) (sqldomain.Config, error)
	SaveConfig(ctx context.Context, cfg sqldomain.Config) error
	SaveSessionSnapshot(ctx context.Context, instance sqldomain.Instance, observedAt time.Time, sessions []sqldomain.Session) error
	SaveStatementEvents(ctx context.Context, events []sqldomain.StatementEvent) error
	SaveDigestSnapshots(ctx context.Context, items []sqldomain.DigestSnapshot) error
	SaveInstanceStatus(ctx context.Context, item sqldomain.InstanceStatus) error
	ListInstanceStatuses(ctx context.Context) ([]sqldomain.InstanceStatus, error)
	ListCollectionStatuses(ctx context.Context, start, end time.Time) ([]sqldomain.InstanceStatus, error)
	ListSessions(ctx context.Context, start, end time.Time) ([]sqldomain.Session, error)
	ListStatementEvents(ctx context.Context, query sqldomain.StatementEventQuery) ([]sqldomain.StatementEvent, error)
	ListDigestSnapshots(ctx context.Context, baselineStart, end time.Time) ([]sqldomain.DigestSnapshot, error)
	SaveKillAudit(ctx context.Context, item sqldomain.KillAudit) error
	ListKillAudits(ctx context.Context, start, end time.Time) ([]sqldomain.KillAudit, error)
	PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type SQLDiagnosticService struct {
	repo      SQLDiagnosticRepository
	instances MySQLInstanceRepository
	machines  machinedomain.Repository
	presets   MySQLAccountPresetRepository
	client    mysqlapp.DiagnosticClient
	explainer mysqlapp.ExecutionPlanExplainer

	configMu       sync.RWMutex
	config         sqldomain.Config
	runMu          sync.Mutex
	stateMu        sync.Mutex
	digests        map[string]digestCounter
	slowLogCursors map[string]time.Time
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

type digestCounter struct {
	BootID        int64
	Count         uint64
	TimerWaitMS   float64
	LockTimeMS    float64
	RowsAffected  uint64
	RowsSent      uint64
	RowsExamined  uint64
	Errors        uint64
	Warnings      uint64
	LastPersisted time.Time
}

type SQLDiagnosticCurrentResult struct {
	CollectedAt time.Time                  `json:"collected_at"`
	Items       []sqldomain.Session        `json:"items"`
	Statuses    []sqldomain.InstanceStatus `json:"statuses"`
	Complete    bool                       `json:"complete"`
	Warnings    []string                   `json:"warnings,omitempty"`
}

type SQLDiagnosticHistoryQuery struct {
	Start         time.Time
	End           time.Time
	Cluster       string
	Machine       string
	Port          int
	User          string
	Database      string
	Keyword       string
	SortBy        string
	SortDirection string
	Limit         int
	Offset        int
}

type SQLDiagnosticHistoryResult struct {
	Start     time.Time        `json:"start"`
	End       time.Time        `json:"end"`
	Total     int              `json:"total"`
	Items     []SQLHistoryItem `json:"items"`
	Coverage  SQLCoverage      `json:"coverage"`
	Truncated bool             `json:"truncated"`
}

type SQLHistoryItem struct {
	Kind         string             `json:"kind"`
	ID           string             `json:"id"`
	Instance     sqldomain.Instance `json:"instance"`
	ProcessID    uint64             `json:"process_id,omitempty"`
	User         string             `json:"user,omitempty"`
	ClientHost   string             `json:"client_host,omitempty"`
	Database     string             `json:"database"`
	SQLText      string             `json:"sql_text"`
	Digest       string             `json:"digest"`
	DigestText   string             `json:"digest_text,omitempty"`
	StartedAt    time.Time          `json:"started_at"`
	EndedAt      *time.Time         `json:"ended_at,omitempty"`
	DurationMS   float64            `json:"duration_ms"`
	LockTimeMS   float64            `json:"lock_time_ms,omitempty"`
	RowsSent     uint64             `json:"rows_sent,omitempty"`
	RowsExamined uint64             `json:"rows_examined,omitempty"`
	ErrorCount   uint64             `json:"error_count,omitempty"`
	NoIndexUsed  bool               `json:"no_index_used,omitempty"`
	SampleCount  int64              `json:"sample_count,omitempty"`
	Source       string             `json:"source"`
}

type SQLTopItem struct {
	Rank             int                  `json:"rank"`
	Digest           string               `json:"digest"`
	DigestText       string               `json:"digest_text"`
	Database         string               `json:"database"`
	ExecutionCount   uint64               `json:"execution_count"`
	TotalLatencyMS   float64              `json:"total_latency_ms"`
	AverageLatencyMS float64              `json:"average_latency_ms"`
	MaxObservedMS    float64              `json:"max_observed_ms"`
	LockTimeMS       float64              `json:"lock_time_ms"`
	RowsAffected     uint64               `json:"rows_affected"`
	RowsSent         uint64               `json:"rows_sent"`
	RowsExamined     uint64               `json:"rows_examined"`
	ErrorCount       uint64               `json:"error_count"`
	WarningCount     uint64               `json:"warning_count"`
	Instances        []sqldomain.Instance `json:"instances"`
	FirstSeenAt      time.Time            `json:"first_seen_at"`
	LastSeenAt       time.Time            `json:"last_seen_at"`
}

type SQLTopResult struct {
	Start            time.Time    `json:"start"`
	End              time.Time    `json:"end"`
	OrderBy          string       `json:"order_by"`
	SortDirection    string       `json:"sort_direction"`
	Items            []SQLTopItem `json:"items"`
	Coverage         SQLCoverage  `json:"coverage"`
	CounterSemantics string       `json:"counter_semantics"`
	MaxLatencySource string       `json:"max_latency_source"`
}

type SQLSlowResult struct {
	Start       time.Time        `json:"start"`
	End         time.Time        `json:"end"`
	ThresholdMS int64            `json:"threshold_ms"`
	Items       []SQLHistoryItem `json:"items"`
	Total       int              `json:"total"`
	Coverage    SQLCoverage      `json:"coverage"`
	Truncated   bool             `json:"truncated"`
}

type SQLCoverage struct {
	Complete bool                       `json:"complete"`
	Statuses []sqldomain.InstanceStatus `json:"statuses"`
	Warnings []string                   `json:"warnings,omitempty"`
}

type KillSQLRequest struct {
	MachineID         string
	Port              int
	ProcessID         uint64
	ExpectedDigest    string
	ExpectedStartedAt time.Time
	Confirmation      string
	Reason            string
	RequestSource     string
}

type KillSQLResult struct {
	Audit  sqldomain.KillAudit `json:"audit"`
	Killed bool                `json:"killed"`
}

type SQLExplainRequest struct {
	MachineID string
	Port      int
	Database  string
	SQL       string
}

func NewSQLDiagnosticService(repo SQLDiagnosticRepository, instances MySQLInstanceRepository, machines machinedomain.Repository, presets MySQLAccountPresetRepository) (*SQLDiagnosticService, error) {
	cfg, err := repo.LoadConfig(context.Background())
	if err != nil {
		return nil, err
	}
	return &SQLDiagnosticService{
		repo: repo, instances: instances, machines: machines, presets: presets,
		client:    mysqlapp.DiagnosticClient{ConnectTimeout: 3 * time.Second, QueryTimeout: 5 * time.Second},
		explainer: mysqlapp.ExecutionPlanClient{ConnectTimeout: 3 * time.Second, QueryTimeout: 30 * time.Second},
		config:    cfg, digests: make(map[string]digestCounter), slowLogCursors: make(map[string]time.Time),
	}, nil
}

func (s *SQLDiagnosticService) Explain(ctx context.Context, req SQLExplainRequest) (mysqlapp.ExecutionPlan, error) {
	statement, err := mysqlapp.NormalizeExplainStatement(req.SQL)
	if err != nil {
		return mysqlapp.ExecutionPlan{}, fmt.Errorf("%w：%v", ErrSQLExplainInvalid, err)
	}
	if strings.TrimSpace(req.MachineID) == "" || req.Port < 1 || req.Port > 65535 {
		return mysqlapp.ExecutionPlan{}, fmt.Errorf("%w：请选择有效的 MySQL 实例", ErrSQLExplainInvalid)
	}
	instance, found, err := s.target(ctx, strings.TrimSpace(req.MachineID), req.Port)
	if err != nil {
		return mysqlapp.ExecutionPlan{}, err
	}
	if !found {
		return mysqlapp.ExecutionPlan{}, fmt.Errorf("%w：MySQL 实例 %s:%d 未登记", ErrSQLExplainInvalid, req.MachineID, req.Port)
	}
	_, credential, err := s.credentials(ctx)
	if err != nil {
		return mysqlapp.ExecutionPlan{}, fmt.Errorf("%w：%v", ErrSQLExplainInvalid, err)
	}
	if s.explainer == nil {
		return mysqlapp.ExecutionPlan{}, errors.New("execution plan explainer is unavailable")
	}
	return s.explainer.Explain(ctx, instance, credential, strings.TrimSpace(req.Database), statement)
}

func (s *SQLDiagnosticService) Start() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go s.collectionLoop(ctx)
}

func (s *SQLDiagnosticService) Close() {
	s.runMu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.runMu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
	}
}

func (s *SQLDiagnosticService) Config() sqldomain.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config
}

func (s *SQLDiagnosticService) SaveConfig(ctx context.Context, cfg sqldomain.Config) (sqldomain.Config, error) {
	if err := validateSQLDiagnosticConfig(cfg); err != nil {
		return sqldomain.Config{}, err
	}
	cfg.UpdatedAt = time.Now().UTC()
	if err := s.repo.SaveConfig(ctx, cfg); err != nil {
		return sqldomain.Config{}, err
	}
	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()
	return cfg, nil
}

func validateSQLDiagnosticConfig(cfg sqldomain.Config) error {
	if cfg.CollectionIntervalSeconds < 2 || cfg.CollectionIntervalSeconds > 60 {
		return errors.New("采集间隔必须在 2–60 秒之间")
	}
	if cfg.SlowThresholdMS < 1 || cfg.SlowThresholdMS > 24*time.Hour.Milliseconds() {
		return errors.New("慢 SQL 阈值必须在 1–86400000 毫秒之间")
	}
	if cfg.RetentionHours < 1 || cfg.RetentionHours > 24*365 {
		return errors.New("历史保留时间必须在 1–8760 小时之间")
	}
	if cfg.MaxSQLTextBytes < 256 || cfg.MaxSQLTextBytes > 4*1024*1024 {
		return errors.New("单条 SQL 文本上限必须在 256–4194304 字节之间")
	}
	return nil
}

func (s *SQLDiagnosticService) collectionLoop(ctx context.Context) {
	defer s.wg.Done()
	var lastPurge time.Time
	for {
		cfg := s.Config()
		if cfg.Enabled {
			_, _ = s.collectAll(ctx, false)
			if time.Since(lastPurge) >= time.Hour {
				_, _ = s.repo.PurgeBefore(ctx, time.Now().UTC().Add(-time.Duration(cfg.RetentionHours)*time.Hour))
				lastPurge = time.Now()
			}
		}
		timer := time.NewTimer(time.Duration(cfg.CollectionIntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *SQLDiagnosticService) Current(ctx context.Context, cluster, machine string, port int) (SQLDiagnosticCurrentResult, error) {
	result, err := s.collectAll(ctx, true)
	result.Items = filterCurrentSessions(result.Items, cluster, machine, port)
	result.Statuses = filterInstanceStatuses(result.Statuses, cluster, machine, port)
	result.Complete = true
	result.Warnings = nil
	for _, status := range result.Statuses {
		if status.Status != "error" {
			continue
		}
		result.Complete = false
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d: %s", status.Instance.MachineIP, status.Instance.Port, status.LastError))
	}
	if len(result.Statuses) == 0 {
		result.Complete = false
		result.Warnings = []string{"当前筛选范围没有已登记且可连接的 MySQL 实例"}
		return result, nil
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].ElapsedMS > result.Items[j].ElapsedMS })
	if len(result.Warnings) == len(result.Statuses) && err != nil {
		return result, err
	}
	return result, nil
}

func (s *SQLDiagnosticService) collectAll(ctx context.Context, liveOnly bool) (SQLDiagnosticCurrentResult, error) {
	targets, err := s.targets(ctx)
	if err != nil {
		return SQLDiagnosticCurrentResult{}, err
	}
	readCredential, _, err := s.credentials(ctx)
	if err != nil {
		return SQLDiagnosticCurrentResult{}, err
	}
	result := SQLDiagnosticCurrentResult{CollectedAt: time.Now().UTC(), Complete: true}
	if len(targets) == 0 {
		result.Warnings = append(result.Warnings, "没有已登记且可连接的 MySQL 实例")
		return result, nil
	}
	type collected struct {
		sessions []sqldomain.Session
		status   sqldomain.InstanceStatus
		err      error
	}
	output := make(chan collected, len(targets))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				output <- collected{status: failedDiagnosticStatus(target, ctx.Err(), collectionMode(liveOnly)), err: ctx.Err()}
				return
			}
			sessions, status, collectErr := s.collectInstance(ctx, target, readCredential, liveOnly)
			output <- collected{sessions: sessions, status: status, err: collectErr}
		}()
	}
	wg.Wait()
	close(output)
	var failures []string
	for item := range output {
		result.Items = append(result.Items, item.sessions...)
		result.Statuses = append(result.Statuses, item.status)
		if item.err != nil {
			result.Complete = false
			failures = append(failures, fmt.Sprintf("%s:%d: %v", item.status.Instance.MachineIP, item.status.Instance.Port, item.err))
		}
	}
	sort.Slice(result.Statuses, func(i, j int) bool {
		return result.Statuses[i].Instance.Key() < result.Statuses[j].Instance.Key()
	})
	result.Warnings = failures
	if len(failures) > 0 {
		return result, fmt.Errorf("%d/%d mysql instances failed diagnostic collection", len(failures), len(targets))
	}
	return result, nil
}

func (s *SQLDiagnosticService) collectInstance(ctx context.Context, instance sqldomain.Instance, credential mysqlapp.DiagnosticCredential, liveOnly bool) ([]sqldomain.Session, sqldomain.InstanceStatus, error) {
	status := sqldomain.InstanceStatus{Instance: instance, Status: "collecting", CollectionMode: collectionMode(liveOnly), LastAttemptAt: time.Now().UTC()}
	db, err := s.client.Open(instance, credential)
	if err != nil {
		status = failedDiagnosticStatus(instance, err, status.CollectionMode)
		_ = s.repo.SaveInstanceStatus(context.Background(), status)
		return nil, status, err
	}
	defer db.Close()
	if err := s.client.Ping(ctx, db); err != nil {
		status = failedDiagnosticStatus(instance, err, status.CollectionMode)
		_ = s.repo.SaveInstanceStatus(context.Background(), status)
		return nil, status, err
	}
	cfg := s.Config()
	sessions, serverUnix, err := s.client.LiveSessions(ctx, db, instance, cfg)
	if err != nil {
		status = failedDiagnosticStatus(instance, err, status.CollectionMode)
		_ = s.repo.SaveInstanceStatus(context.Background(), status)
		return nil, status, err
	}
	observedAt := time.Now().UTC()
	for index := range sessions {
		sessions[index].FirstSeenAt = observedAt
		sessions[index].LastSeenAt = observedAt
	}
	if err := s.repo.SaveSessionSnapshot(ctx, instance, observedAt, sessions); err != nil {
		status = failedDiagnosticStatus(instance, err, status.CollectionMode)
		_ = s.repo.SaveInstanceStatus(context.Background(), status)
		return sessions, status, err
	}
	status.LiveSessionCount = len(sessions)
	if serverUnix > 0 {
		status.ServerClockOffsetMS = unixFloatMillis(serverUnix) - observedAt.UnixMilli()
	}
	var degraded []string
	caps, capsErr := s.client.Capabilities(ctx, db)
	if capsErr != nil {
		degraded = append(degraded, capsErr.Error())
	} else {
		if caps.ServerUnix > 0 {
			status.ServerClockOffsetMS = unixFloatMillis(caps.ServerUnix) - time.Now().UTC().UnixMilli()
		}
		status.PerformanceSchemaAvailable = caps.PerformanceSchema
		status.HistoryLongConsumerEnabled = caps.HistoryLong
		status.DigestConsumerEnabled = caps.DigestStatements
		status.SlowLogTableAvailable = caps.SlowLogTable
		status.SlowLogThresholdMS = caps.SlowLogThresholdMS
		status.SQLTextLimit = caps.SQLTextLimit
	}
	if !liveOnly && capsErr == nil {
		events, eventErr := s.client.StatementHistory(ctx, db, instance, caps, cfg)
		if eventErr != nil {
			degraded = append(degraded, "statement history: "+eventErr.Error())
		}
		if caps.SlowLogTable {
			slowEvents, slowErr := s.client.SlowLogEvents(ctx, db, instance, caps, s.slowLogSince(instance, cfg), cfg)
			if slowErr != nil {
				degraded = append(degraded, "mysql.slow_log: "+slowErr.Error())
			} else {
				events = append(events, slowEvents...)
				s.advanceSlowLogCursor(instance, slowEvents, cfg)
			}
		}
		if len(events) > 0 {
			if err := s.repo.SaveStatementEvents(ctx, events); err != nil {
				degraded = append(degraded, "save statement history: "+err.Error())
			}
		}
		if snapshots, snapshotErr := s.client.DigestSnapshots(ctx, db, instance, caps, cfg); snapshotErr != nil {
			degraded = append(degraded, "digest summary: "+snapshotErr.Error())
		} else if changed := s.changedDigestSnapshots(snapshots, cfg); len(changed) > 0 {
			if err := s.repo.SaveDigestSnapshots(ctx, changed); err != nil {
				degraded = append(degraded, "save digest summary: "+err.Error())
			}
		}
	}
	status.LastSuccessAt = observedAt
	if len(degraded) > 0 {
		status.Status = "degraded"
		status.LastError = strings.Join(degraded, "; ")
	} else {
		status.Status = "ok"
	}
	if err := s.repo.SaveInstanceStatus(ctx, status); err != nil {
		return sessions, status, err
	}
	return sessions, status, nil
}

func (s *SQLDiagnosticService) changedDigestSnapshots(items []sqldomain.DigestSnapshot, cfg sqldomain.Config) []sqldomain.DigestSnapshot {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	out := make([]sqldomain.DigestSnapshot, 0, len(items))
	for _, item := range items {
		key := item.Instance.Key() + ":" + item.Digest
		current := digestCounter{
			BootID: item.ServerBootID, Count: item.Count, TimerWaitMS: item.SumTimerWaitMS,
			LockTimeMS: item.SumLockTimeMS, RowsAffected: item.SumRowsAffected,
			RowsSent: item.SumRowsSent, RowsExamined: item.SumRowsExamined,
			Errors: item.SumErrors, Warnings: item.SumWarnings,
		}
		previous, exists := s.digests[key]
		heartbeatDue := exists && item.CollectedAt.Sub(previous.LastPersisted) >= time.Hour
		changed := !exists || previous.BootID != current.BootID || previous.Count != current.Count ||
			previous.TimerWaitMS != current.TimerWaitMS || previous.RowsExamined != current.RowsExamined ||
			heartbeatDue
		if changed {
			current.LastPersisted = item.CollectedAt
			out = append(out, item)
		} else {
			current.LastPersisted = previous.LastPersisted
		}
		s.digests[key] = current
	}
	return out
}

func (s *SQLDiagnosticService) slowLogSince(instance sqldomain.Instance, cfg sqldomain.Config) time.Time {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if value := s.slowLogCursors[instance.Key()]; !value.IsZero() {
		return value
	}
	return time.Now().UTC().Add(-time.Duration(cfg.RetentionHours) * time.Hour)
}

func (s *SQLDiagnosticService) advanceSlowLogCursor(instance sqldomain.Instance, events []sqldomain.StatementEvent, cfg sqldomain.Config) {
	cursor := time.Now().UTC().Add(-2 * time.Duration(cfg.CollectionIntervalSeconds) * time.Second)
	for _, event := range events {
		if event.StartedAt.After(cursor) {
			cursor = event.StartedAt
		}
	}
	// Query one second of overlap and rely on the event primary key for
	// de-duplication. This avoids losing rows with coarse slow_log timestamps.
	cursor = cursor.Add(-time.Second)
	s.stateMu.Lock()
	s.slowLogCursors[instance.Key()] = cursor
	s.stateMu.Unlock()
}

func (s *SQLDiagnosticService) History(ctx context.Context, query SQLDiagnosticHistoryQuery) (SQLDiagnosticHistoryResult, error) {
	query, err := s.normalizeHistoryQuery(query)
	if err != nil {
		return SQLDiagnosticHistoryResult{}, err
	}
	sessions, err := s.repo.ListSessions(ctx, query.Start, query.End)
	if err != nil {
		return SQLDiagnosticHistoryResult{}, err
	}
	events, err := s.repo.ListStatementEvents(ctx, statementEventQuery(query, 0))
	if err != nil {
		return SQLDiagnosticHistoryResult{}, err
	}
	items := make([]SQLHistoryItem, 0, len(sessions)+len(events))
	for _, item := range sessions {
		history := historyFromSession(item)
		if matchesHistoryQuery(history, query) {
			items = append(items, history)
		}
	}
	for _, item := range events {
		history := historyFromEvent(item)
		if matchesHistoryQuery(history, query) {
			items = append(items, history)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.After(items[j].StartedAt) })
	total := len(items)
	start := query.Offset
	if start > total {
		start = total
	}
	end := start + query.Limit
	if end > total {
		end = total
	}
	coverage, _ := s.coverage(ctx, query.Start, query.End, query.Cluster, query.Machine, query.Port)
	truncated := len(events) == 100000
	if truncated {
		coverage.Complete = false
		coverage.Warnings = append(coverage.Warnings, "已完成语句明细达到 100000 行安全上限，请缩小时间范围或限定实例")
	}
	return SQLDiagnosticHistoryResult{Start: query.Start, End: query.End, Total: total, Items: items[start:end], Coverage: coverage, Truncated: truncated}, nil
}

func (s *SQLDiagnosticService) TopSQL(ctx context.Context, query SQLDiagnosticHistoryQuery, orderBy string) (SQLTopResult, error) {
	query, err := s.normalizeHistoryQuery(query)
	if err != nil {
		return SQLTopResult{}, err
	}
	baselineStart := query.Start.Add(-time.Duration(s.Config().RetentionHours) * time.Hour)
	snapshots, err := s.repo.ListDigestSnapshots(ctx, baselineStart, query.End)
	if err != nil {
		return SQLTopResult{}, err
	}
	type topAggregate struct {
		item      SQLTopItem
		instances map[string]sqldomain.Instance
	}
	aggregates := make(map[string]*topAggregate)
	previous := make(map[string]sqldomain.DigestSnapshot)
	missingBaselines := 0
	for _, current := range snapshots {
		instanceDigest := fmt.Sprintf("%s:%d:%d:%s", current.Instance.MachineID, current.Instance.Port, current.ServerBootID, current.Digest)
		before, ok := previous[instanceDigest]
		previous[instanceDigest] = current
		if current.CollectedAt.Before(query.Start) || current.CollectedAt.After(query.End) {
			continue
		}
		if !matchesInstance(current.Instance, query.Cluster, query.Machine, query.Port) {
			continue
		}
		if query.Database != "" && !strings.EqualFold(query.Database, current.Database) {
			continue
		}
		if keyword := strings.ToLower(strings.TrimSpace(query.Keyword)); keyword != "" &&
			!strings.Contains(strings.ToLower(current.Digest+" "+current.DigestText), keyword) {
			continue
		}
		if !ok {
			createdInWindow := !current.FirstSeenAt.IsZero() && !current.FirstSeenAt.Before(query.Start)
			restartedInWindow := current.ServerBootID >= query.Start.Unix()
			if !createdInWindow && !restartedInWindow {
				missingBaselines++
				continue
			}
			before = sqldomain.DigestSnapshot{ServerBootID: current.ServerBootID}
		}
		deltaCount, reset := counterDelta(current.Count, before.Count)
		if deltaCount == 0 && !reset {
			continue
		}
		key := current.Digest + "\x00" + current.Database
		agg := aggregates[key]
		if agg == nil {
			agg = &topAggregate{item: SQLTopItem{Digest: current.Digest, DigestText: current.DigestText, Database: current.Database}, instances: make(map[string]sqldomain.Instance)}
			aggregates[key] = agg
		}
		agg.item.ExecutionCount += deltaCount
		agg.item.TotalLatencyMS += floatDelta(current.SumTimerWaitMS, before.SumTimerWaitMS, reset)
		agg.item.LockTimeMS += floatDelta(current.SumLockTimeMS, before.SumLockTimeMS, reset)
		agg.item.RowsAffected += uintDelta(current.SumRowsAffected, before.SumRowsAffected, reset)
		agg.item.RowsSent += uintDelta(current.SumRowsSent, before.SumRowsSent, reset)
		agg.item.RowsExamined += uintDelta(current.SumRowsExamined, before.SumRowsExamined, reset)
		agg.item.ErrorCount += uintDelta(current.SumErrors, before.SumErrors, reset)
		agg.item.WarningCount += uintDelta(current.SumWarnings, before.SumWarnings, reset)
		agg.instances[current.Instance.Key()] = current.Instance
		if agg.item.FirstSeenAt.IsZero() || current.FirstSeenAt.Before(agg.item.FirstSeenAt) {
			agg.item.FirstSeenAt = current.FirstSeenAt
		}
		if current.LastSeenAt.After(agg.item.LastSeenAt) {
			agg.item.LastSeenAt = current.LastSeenAt
		}
	}
	// Individual completed events provide an actual per-window maximum, unlike
	// the cumulative MAX_TIMER_WAIT counter.
	events, _ := s.repo.ListStatementEvents(ctx, statementEventQuery(query, 0))
	for _, event := range events {
		if !matchesInstance(event.Instance, query.Cluster, query.Machine, query.Port) {
			continue
		}
		key := event.Digest + "\x00" + event.Database
		if agg := aggregates[key]; agg != nil && event.DurationMS > agg.item.MaxObservedMS {
			agg.item.MaxObservedMS = event.DurationMS
		}
	}
	items := make([]SQLTopItem, 0, len(aggregates))
	for _, aggregate := range aggregates {
		if aggregate.item.ExecutionCount > 0 {
			aggregate.item.AverageLatencyMS = aggregate.item.TotalLatencyMS / float64(aggregate.item.ExecutionCount)
		}
		for _, instance := range aggregate.instances {
			aggregate.item.Instances = append(aggregate.item.Instances, instance)
		}
		sort.Slice(aggregate.item.Instances, func(i, j int) bool { return aggregate.item.Instances[i].Key() < aggregate.item.Instances[j].Key() })
		items = append(items, aggregate.item)
	}
	orderBy = normalizeTopOrder(orderBy)
	sortDirection := normalizeSortDirection(query.SortDirection)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := topValue(items[i], orderBy), topValue(items[j], orderBy)
		if left == right {
			return items[i].Digest < items[j].Digest
		}
		if sortDirection == "asc" {
			return left < right
		}
		return left > right
	})
	if len(items) > query.Limit {
		items = items[:query.Limit]
	}
	for index := range items {
		items[index].Rank = index + 1
	}
	coverage, _ := s.coverage(ctx, query.Start, query.End, query.Cluster, query.Machine, query.Port)
	for _, status := range coverage.Statuses {
		if !status.DigestConsumerEnabled {
			coverage.Complete = false
			coverage.Warnings = append(coverage.Warnings, fmt.Sprintf("%s:%d 未启用 performance_schema statements_digest，无法获得 TOP-SQL 计数", status.Instance.MachineIP, status.Instance.Port))
		}
	}
	if missingBaselines > 0 {
		coverage.Complete = false
		coverage.Warnings = append(coverage.Warnings, fmt.Sprintf("%d 个 Digest 序列缺少区间前基线，已排除以避免把生命周期累计值误算到当前区间", missingBaselines))
	}
	if len(events) == 100000 {
		coverage.Complete = false
		coverage.Warnings = append(coverage.Warnings, "区间最大耗时样本达到 100000 行安全上限；总耗时仍使用完整的 Digest 计数器增量")
	}
	return SQLTopResult{
		Start: query.Start, End: query.End, OrderBy: orderBy, SortDirection: sortDirection, Items: items, Coverage: coverage,
		CounterSemantics: "performance_schema cumulative counters converted to adjacent-snapshot deltas; reset-safe",
		MaxLatencySource: "events_statements_history_long completed events observed in the selected window",
	}, nil
}

func (s *SQLDiagnosticService) SlowSQL(ctx context.Context, query SQLDiagnosticHistoryQuery, thresholdMS int64) (SQLSlowResult, error) {
	query, err := s.normalizeHistoryQuery(query)
	if err != nil {
		return SQLSlowResult{}, err
	}
	if thresholdMS <= 0 {
		thresholdMS = s.Config().SlowThresholdMS
	}
	events, err := s.repo.ListStatementEvents(ctx, statementEventQuery(query, float64(thresholdMS)))
	if err != nil {
		return SQLSlowResult{}, err
	}
	sessions, err := s.repo.ListSessions(ctx, query.Start, query.End)
	if err != nil {
		return SQLSlowResult{}, err
	}
	items := make([]SQLHistoryItem, 0, len(events)+len(sessions))
	for _, event := range events {
		item := historyFromEvent(event)
		if matchesHistoryQuery(item, query) {
			items = append(items, item)
		}
	}
	for _, session := range sessions {
		if session.MaxElapsedMS < thresholdMS {
			continue
		}
		if sessionHasCompletedEvent(session, events) {
			continue
		}
		item := historyFromSession(session)
		if matchesHistoryQuery(item, query) {
			items = append(items, item)
		}
	}
	sortSlowSQLItems(items, query.SortBy, query.SortDirection)
	total := len(items)
	if total > query.Limit {
		items = items[:query.Limit]
	}
	coverage, _ := s.coverage(ctx, query.Start, query.End, query.Cluster, query.Machine, query.Port)
	for _, status := range coverage.Statuses {
		if !status.HistoryLongConsumerEnabled && !status.SlowLogTableAvailable {
			coverage.Complete = false
			coverage.Warnings = append(coverage.Warnings, fmt.Sprintf("%s:%d 既无语句历史消费者也无 TABLE 慢日志，可能漏掉已完成的慢 SQL", status.Instance.MachineIP, status.Instance.Port))
		} else if !status.HistoryLongConsumerEnabled && status.SlowLogThresholdMS > thresholdMS {
			coverage.Complete = false
			coverage.Warnings = append(coverage.Warnings, fmt.Sprintf("%s:%d 慢日志阈值为 %d 毫秒，高于本次查询的 %d 毫秒", status.Instance.MachineIP, status.Instance.Port, status.SlowLogThresholdMS, thresholdMS))
		}
	}
	truncated := len(events) == 100000
	if truncated {
		coverage.Complete = false
		coverage.Warnings = append(coverage.Warnings, "慢 SQL 明细达到 100000 行安全上限，请缩小时间范围或限定实例")
	}
	return SQLSlowResult{Start: query.Start, End: query.End, ThresholdMS: thresholdMS, Items: items, Total: total, Coverage: coverage, Truncated: truncated}, nil
}

func (s *SQLDiagnosticService) KillQuery(ctx context.Context, req KillSQLRequest) (KillSQLResult, error) {
	if req.ProcessID == 0 || strings.TrimSpace(req.MachineID) == "" || req.Port <= 0 {
		return KillSQLResult{}, errors.New("查杀必须指定 machine_id、port 和 process_id")
	}
	if req.ExpectedDigest == "" || req.ExpectedStartedAt.IsZero() {
		return KillSQLResult{}, errors.New("安全查杀必须携带 expected_digest 和 expected_started_at")
	}
	if req.Confirmation != fmt.Sprintf("KILL %d", req.ProcessID) {
		return KillSQLResult{}, errors.New("确认短语必须精确匹配 KILL <process_id>")
	}
	if len(strings.TrimSpace(req.Reason)) < 3 {
		return KillSQLResult{}, errors.New("查杀原因至少需要 3 个字符")
	}
	instance, ok, err := s.target(ctx, req.MachineID, req.Port)
	if err != nil {
		return KillSQLResult{}, err
	}
	if !ok {
		return KillSQLResult{}, errors.New("未找到已登记的 MySQL 实例")
	}
	_, killCredential, err := s.credentials(ctx)
	if err != nil {
		return KillSQLResult{}, err
	}
	db, err := s.client.Open(instance, killCredential)
	if err != nil {
		return KillSQLResult{}, err
	}
	defer db.Close()
	cfg := s.Config()
	sessions, _, err := s.client.LiveSessions(ctx, db, instance, cfg)
	if err != nil {
		return KillSQLResult{}, err
	}
	var target *sqldomain.Session
	for index := range sessions {
		if sessions[index].ProcessID == req.ProcessID {
			target = &sessions[index]
			break
		}
	}
	if target == nil {
		return KillSQLResult{}, fmt.Errorf("%w：进程 %d 已不再执行 SQL", ErrSQLDiagnosticConflict, req.ProcessID)
	}
	if !strings.EqualFold(target.Digest, req.ExpectedDigest) || absDuration(target.QueryStartedAt.Sub(req.ExpectedStartedAt)) > 2*time.Second {
		return KillSQLResult{}, fmt.Errorf("%w：进程 %d 当前正在执行另一条 SQL", ErrSQLDiagnosticConflict, req.ProcessID)
	}
	if protectedDiagnosticUser(target.User) {
		return KillSQLResult{}, fmt.Errorf("%w：拒绝查杀 MySQL 受保护账号 %s", ErrSQLDiagnosticForbidden, target.User)
	}
	audit := sqldomain.KillAudit{
		ID: killAuditID(instance, req.ProcessID), Instance: instance, ProcessID: req.ProcessID,
		ExpectedDigest: req.ExpectedDigest, ExpectedStartedAt: req.ExpectedStartedAt,
		SQLText: target.SQLText, User: target.User, ClientHost: target.ClientHost,
		Reason: strings.TrimSpace(req.Reason), RequestSource: req.RequestSource,
		Status: "requested", RequestedAt: time.Now().UTC(),
	}
	if err := s.repo.SaveKillAudit(ctx, audit); err != nil {
		return KillSQLResult{}, err
	}
	err = s.client.KillQuery(ctx, db, req.ProcessID)
	completed := time.Now().UTC()
	audit.CompletedAt = &completed
	if err != nil {
		audit.Status, audit.Error = "failed", err.Error()
		_ = s.repo.SaveKillAudit(context.Background(), audit)
		return KillSQLResult{Audit: audit}, err
	}
	audit.Status = "success"
	if err := s.repo.SaveKillAudit(ctx, audit); err != nil {
		return KillSQLResult{Audit: audit, Killed: true}, err
	}
	return KillSQLResult{Audit: audit, Killed: true}, nil
}

func (s *SQLDiagnosticService) KillAudits(ctx context.Context, start, end time.Time) ([]sqldomain.KillAudit, error) {
	if start.IsZero() {
		start = time.Now().UTC().Add(-24 * time.Hour)
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}
	return s.repo.ListKillAudits(ctx, start, end)
}

func (s *SQLDiagnosticService) coverage(ctx context.Context, start, end time.Time, cluster, machine string, port int) (SQLCoverage, error) {
	statuses, err := s.repo.ListInstanceStatuses(ctx)
	if err != nil {
		return SQLCoverage{}, err
	}
	statuses = filterInstanceStatuses(statuses, cluster, machine, port)
	result := SQLCoverage{Complete: true, Statuses: statuses}
	cfg := s.Config()
	interval := time.Duration(cfg.CollectionIntervalSeconds) * time.Second
	runs, err := s.repo.ListCollectionStatuses(ctx, start.Add(-2*interval), end.Add(2*interval))
	if err != nil {
		return SQLCoverage{}, err
	}
	runsByInstance := make(map[string][]sqldomain.InstanceStatus)
	for _, run := range runs {
		if !matchesInstance(run.Instance, cluster, machine, port) {
			continue
		}
		runsByInstance[run.Instance.Key()] = append(runsByInstance[run.Instance.Key()], run)
	}
	targets, targetErr := s.targets(ctx)
	if targetErr != nil {
		return SQLCoverage{}, targetErr
	}
	latestByInstance := make(map[string]sqldomain.InstanceStatus, len(statuses))
	for _, status := range statuses {
		latestByInstance[status.Instance.Key()] = status
	}
	filteredTargets := make([]sqldomain.Instance, 0, len(targets))
	for _, target := range targets {
		if matchesInstance(target, cluster, machine, port) {
			filteredTargets = append(filteredTargets, target)
		}
	}
	for _, target := range filteredTargets {
		status, hasLatest := latestByInstance[target.Key()]
		targetRuns := runsByInstance[target.Key()]
		if !hasLatest || len(targetRuns) == 0 {
			result.Complete = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 在所选区间没有完整诊断采样", target.MachineIP, target.Port))
			continue
		}
		var firstSuccess, lastSuccess time.Time
		for _, run := range targetRuns {
			if run.Status == "error" || run.LastSuccessAt.IsZero() {
				result.Complete = false
				continue
			}
			if !lastSuccess.IsZero() && run.LastSuccessAt.Sub(lastSuccess) > 3*interval {
				result.Complete = false
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 采集在 %s 至 %s 之间中断", target.MachineIP, target.Port, lastSuccess.Format(time.RFC3339), run.LastSuccessAt.Format(time.RFC3339)))
			}
			if firstSuccess.IsZero() {
				firstSuccess = run.LastSuccessAt
			}
			lastSuccess = run.LastSuccessAt
		}
		if firstSuccess.IsZero() || firstSuccess.After(start.Add(2*interval)) {
			result.Complete = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 完整采集未覆盖所选区间起点", target.MachineIP, target.Port))
		}
		if lastSuccess.IsZero() || lastSuccess.Before(end.Add(-2*interval)) {
			result.Complete = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 完整采集未覆盖所选区间终点", target.MachineIP, target.Port))
		}
		if status.Status != "ok" {
			result.Complete = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 最近采集状态为 %s：%s", target.MachineIP, target.Port, status.Status, status.LastError))
		}
		if !status.HistoryLongConsumerEnabled {
			result.Complete = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d 未启用 performance_schema events_statements_history_long，短时已完成 SQL 可能缺失", target.MachineIP, target.Port))
		}
	}
	if len(filteredTargets) == 0 {
		result.Complete = false
		result.Warnings = append(result.Warnings, "当前筛选范围没有已登记且可连接的 MySQL 实例")
	}
	return result, nil
}

func (s *SQLDiagnosticService) normalizeHistoryQuery(query SQLDiagnosticHistoryQuery) (SQLDiagnosticHistoryQuery, error) {
	now := time.Now().UTC()
	if query.End.IsZero() {
		query.End = now
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-time.Hour)
	}
	query.Start, query.End = query.Start.UTC(), query.End.UTC()
	if !query.Start.Before(query.End) {
		return query, errors.New("开始时间必须早于结束时间")
	}
	retention := time.Duration(s.Config().RetentionHours) * time.Hour
	if query.Start.Before(now.Add(-retention - time.Minute)) {
		return query, fmt.Errorf("开始时间超出已配置的 %d 小时保留期", s.Config().RetentionHours)
	}
	if query.End.After(now.Add(5 * time.Minute)) {
		return query, errors.New("结束时间不能晚于当前时间")
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 1000 {
		query.Limit = 1000
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	return query, nil
}

func (s *SQLDiagnosticService) targets(ctx context.Context) ([]sqldomain.Instance, error) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sqldomain.Instance, 0, len(items))
	for _, item := range items {
		machine, ok, err := s.machines.GetByID(ctx, item.MachineID)
		if err != nil {
			return nil, err
		}
		if !ok || strings.TrimSpace(machine.IP) == "" {
			continue
		}
		out = append(out, sqldomain.Instance{
			MachineID: item.MachineID, MachineName: machine.Name, MachineIP: machine.IP,
			Cluster: machine.Cluster, Port: item.Port, Version: item.Version,
		})
	}
	return out, nil
}

func (s *SQLDiagnosticService) target(ctx context.Context, machineID string, port int) (sqldomain.Instance, bool, error) {
	items, err := s.targets(ctx)
	if err != nil {
		return sqldomain.Instance{}, false, err
	}
	for _, item := range items {
		if item.MachineID == machineID && item.Port == port {
			return item, true, nil
		}
	}
	return sqldomain.Instance{}, false, nil
}

func (s *SQLDiagnosticService) credentials(ctx context.Context) (mysqlapp.DiagnosticCredential, mysqlapp.DiagnosticCredential, error) {
	items, err := s.presets.List(ctx)
	if err != nil {
		return mysqlapp.DiagnosticCredential{}, mysqlapp.DiagnosticCredential{}, err
	}
	items = normalizeMySQLAccountPresets(items)
	var read, kill mysqlapp.DiagnosticCredential
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		credential := mysqlapp.DiagnosticCredential{Username: strings.TrimSpace(item.Username), Password: item.Password}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case mysqlapp.AccountRoleMonitor:
			read = credential
		case mysqlapp.AccountRoleMHA:
			kill = credential
		}
	}
	if read.Username == "" {
		return read, kill, errors.New("SQL 诊断需要已启用的监控账号预设")
	}
	if kill.Username == "" {
		kill = read
	}
	return read, kill, nil
}

func filterCurrentSessions(items []sqldomain.Session, cluster, machine string, port int) []sqldomain.Session {
	out := make([]sqldomain.Session, 0, len(items))
	for _, item := range items {
		if matchesInstance(item.Instance, cluster, machine, port) {
			out = append(out, item)
		}
	}
	return out
}

func filterInstanceStatuses(items []sqldomain.InstanceStatus, cluster, machine string, port int) []sqldomain.InstanceStatus {
	out := make([]sqldomain.InstanceStatus, 0, len(items))
	for _, item := range items {
		if matchesInstance(item.Instance, cluster, machine, port) {
			out = append(out, item)
		}
	}
	return out
}

func matchesInstance(instance sqldomain.Instance, cluster, machine string, port int) bool {
	if strings.TrimSpace(cluster) != "" && !strings.EqualFold(instance.Cluster, strings.TrimSpace(cluster)) {
		return false
	}
	if value := strings.TrimSpace(machine); value != "" &&
		!strings.EqualFold(instance.MachineID, value) &&
		!strings.EqualFold(instance.MachineName, value) &&
		!strings.EqualFold(instance.MachineIP, value) {
		return false
	}
	return port <= 0 || instance.Port == port
}

func matchesHistoryQuery(item SQLHistoryItem, query SQLDiagnosticHistoryQuery) bool {
	if !matchesInstance(item.Instance, query.Cluster, query.Machine, query.Port) {
		return false
	}
	if query.User != "" && !strings.EqualFold(item.User, query.User) {
		return false
	}
	if query.Database != "" && !strings.EqualFold(item.Database, query.Database) {
		return false
	}
	if keyword := strings.ToLower(strings.TrimSpace(query.Keyword)); keyword != "" {
		haystack := strings.ToLower(item.SQLText + " " + item.DigestText + " " + item.Digest)
		if !strings.Contains(haystack, keyword) {
			return false
		}
	}
	return true
}

func statementEventQuery(query SQLDiagnosticHistoryQuery, minimumDurationMS float64) sqldomain.StatementEventQuery {
	return sqldomain.StatementEventQuery{
		Start: query.Start, End: query.End, MinimumDurationMS: minimumDurationMS,
		Cluster: query.Cluster, Machine: query.Machine, Port: query.Port,
		Database: query.Database, Keyword: query.Keyword, Limit: 100000,
	}
}

func historyFromSession(item sqldomain.Session) SQLHistoryItem {
	duration := float64(item.MaxElapsedMS)
	return SQLHistoryItem{
		Kind: "session", ID: item.ID, Instance: item.Instance, ProcessID: item.ProcessID,
		User: item.User, ClientHost: item.ClientHost, Database: item.Database,
		SQLText: item.SQLText, Digest: item.Digest, DigestText: item.DigestText,
		StartedAt: item.QueryStartedAt, EndedAt: item.EndedAt, DurationMS: duration,
		SampleCount: item.SampleCount, Source: item.Source,
	}
}

func historyFromEvent(item sqldomain.StatementEvent) SQLHistoryItem {
	ended := item.EndedAt
	source := "performance_schema.events_statements_history_long"
	if item.EventName == "mysql.slow_log" {
		source = "mysql.slow_log"
	}
	return SQLHistoryItem{
		Kind: "statement", ID: item.ID, Instance: item.Instance, User: item.User,
		ClientHost: item.ClientHost, Database: item.Database,
		SQLText: item.SQLText, Digest: item.Digest, DigestText: item.DigestText,
		StartedAt: item.StartedAt, EndedAt: &ended, DurationMS: item.DurationMS,
		LockTimeMS: item.LockTimeMS, RowsSent: item.RowsSent, RowsExamined: item.RowsExamined,
		ErrorCount: item.ErrorCount, NoIndexUsed: item.NoIndexUsed,
		Source: source,
	}
}

func sessionHasCompletedEvent(session sqldomain.Session, events []sqldomain.StatementEvent) bool {
	for _, event := range events {
		if event.Instance.Key() != session.Instance.Key() || !strings.EqualFold(event.Digest, session.Digest) {
			continue
		}
		tolerance := 2 * time.Second
		if session.TimingPrecisionMS > 0 && time.Duration(session.TimingPrecisionMS)*time.Millisecond > tolerance {
			tolerance = time.Duration(session.TimingPrecisionMS) * time.Millisecond
		}
		if absDuration(event.StartedAt.Sub(session.QueryStartedAt)) <= tolerance {
			return true
		}
	}
	return false
}

func counterDelta(current, previous uint64) (uint64, bool) {
	if current >= previous {
		return current - previous, false
	}
	return current, true
}

func uintDelta(current, previous uint64, reset bool) uint64 {
	if reset || current < previous {
		return current
	}
	return current - previous
}

func floatDelta(current, previous float64, reset bool) float64 {
	if reset || current < previous {
		return current
	}
	return current - previous
}

func normalizeTopOrder(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "count", "execution_count":
		return "execution_count"
	case "avg", "average_latency", "average_latency_ms":
		return "average_latency_ms"
	case "rows", "rows_examined":
		return "rows_examined"
	case "errors", "error_count":
		return "error_count"
	default:
		return "total_latency_ms"
	}
}

func topValue(item SQLTopItem, order string) float64 {
	switch order {
	case "execution_count":
		return float64(item.ExecutionCount)
	case "average_latency_ms":
		return item.AverageLatencyMS
	case "rows_examined":
		return float64(item.RowsExamined)
	case "error_count":
		return float64(item.ErrorCount)
	default:
		return item.TotalLatencyMS
	}
}

func normalizeSortDirection(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "asc") {
		return "asc"
	}
	return "desc"
}

func sortSlowSQLItems(items []SQLHistoryItem, sortBy, direction string) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "started_at", "time":
		sortBy = "started_at"
	case "rows_examined":
		sortBy = "rows_examined"
	case "rows_sent":
		sortBy = "rows_sent"
	case "error_count":
		sortBy = "error_count"
	default:
		sortBy = "duration_ms"
	}
	direction = normalizeSortDirection(direction)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := slowSortValue(items[i], sortBy), slowSortValue(items[j], sortBy)
		if left == right {
			return items[i].ID < items[j].ID
		}
		if direction == "asc" {
			return left < right
		}
		return left > right
	})
}

func slowSortValue(item SQLHistoryItem, sortBy string) float64 {
	switch sortBy {
	case "started_at":
		return float64(item.StartedAt.UnixNano())
	case "rows_examined":
		return float64(item.RowsExamined)
	case "rows_sent":
		return float64(item.RowsSent)
	case "error_count":
		return float64(item.ErrorCount)
	default:
		return item.DurationMS
	}
}

func failedDiagnosticStatus(instance sqldomain.Instance, err error, mode string) sqldomain.InstanceStatus {
	return sqldomain.InstanceStatus{
		Instance: instance, Status: "error", CollectionMode: mode, LastAttemptAt: time.Now().UTC(), LastError: err.Error(),
	}
}

func collectionMode(liveOnly bool) string {
	if liveOnly {
		return "live"
	}
	return "full"
}

func protectedDiagnosticUser(user string) bool {
	switch strings.ToLower(strings.TrimSpace(user)) {
	case "system user", "event_scheduler", "mysql.session", "mysql.sys", "mysql.infoschema":
		return true
	default:
		return false
	}
}

func killAuditID(instance sqldomain.Instance, processID uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", instance.Key(), processID, time.Now().UnixNano())))
	return hex.EncodeToString(sum[:])
}

func unixFloatMillis(value float64) int64 {
	return int64(value * 1000)
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
