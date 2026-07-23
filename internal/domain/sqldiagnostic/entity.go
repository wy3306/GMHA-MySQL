// Package sqldiagnostic defines the SQL diagnostic data captured from managed
// MySQL instances. Timestamps are always UTC at the storage/API boundary.
package sqldiagnostic

import "time"

const (
	DefaultCollectionInterval = 5 * time.Second
	DefaultSlowThreshold      = time.Second
	DefaultRetention          = 24 * time.Hour
	DefaultMaxSQLTextBytes    = 64 * 1024
)

// Config controls collection fidelity and storage cost.
type Config struct {
	Enabled                   bool      `json:"enabled"`
	CollectionIntervalSeconds int       `json:"collection_interval_seconds"`
	SlowThresholdMS           int64     `json:"slow_threshold_ms"`
	RetentionHours            int       `json:"retention_hours"`
	MaxSQLTextBytes           int       `json:"max_sql_text_bytes"`
	CaptureSQLText            bool      `json:"capture_sql_text"`
	RedactLiterals            bool      `json:"redact_literals"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                   true,
		CollectionIntervalSeconds: int(DefaultCollectionInterval / time.Second),
		SlowThresholdMS:           DefaultSlowThreshold.Milliseconds(),
		RetentionHours:            int(DefaultRetention / time.Hour),
		MaxSQLTextBytes:           DefaultMaxSQLTextBytes,
		CaptureSQLText:            true,
		RedactLiterals:            false,
	}
}

// Instance identifies one managed MySQL listener.
type Instance struct {
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	MachineIP   string `json:"machine_ip"`
	Cluster     string `json:"cluster"`
	Port        int    `json:"port"`
	Version     string `json:"version"`
}

func (i Instance) Key() string { return i.MachineID + ":" + itoa(i.Port) }

// Session is a currently running query or one persisted query lifecycle.
type Session struct {
	ID                string     `json:"id"`
	Instance          Instance   `json:"instance"`
	ProcessID         uint64     `json:"process_id"`
	ThreadID          uint64     `json:"thread_id,omitempty"`
	User              string     `json:"user"`
	ClientHost        string     `json:"client_host"`
	Database          string     `json:"database"`
	Command           string     `json:"command"`
	State             string     `json:"state"`
	SQLText           string     `json:"sql_text"`
	Digest            string     `json:"digest"`
	DigestText        string     `json:"digest_text,omitempty"`
	QueryStartedAt    time.Time  `json:"query_started_at"`
	FirstSeenAt       time.Time  `json:"first_seen_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	EndedAt           *time.Time `json:"ended_at,omitempty"`
	ElapsedMS         int64      `json:"elapsed_ms"`
	MaxElapsedMS      int64      `json:"max_elapsed_ms"`
	TimingSource      string     `json:"timing_source"`
	TimingPrecisionMS int64      `json:"timing_precision_ms"`
	SampleCount       int64      `json:"sample_count"`
	Source            string     `json:"source"`
	SQLTextTruncated  bool       `json:"sql_text_truncated"`
}

// StatementEvent is an individual completed statement captured from
// performance_schema.events_statements_history_long.
type StatementEvent struct {
	ID             string    `json:"id"`
	Instance       Instance  `json:"instance"`
	ServerBootID   int64     `json:"server_boot_id"`
	ThreadID       uint64    `json:"thread_id"`
	EventID        uint64    `json:"event_id"`
	EventName      string    `json:"event_name"`
	User           string    `json:"user"`
	ClientHost     string    `json:"client_host"`
	Database       string    `json:"database"`
	SQLText        string    `json:"sql_text"`
	Digest         string    `json:"digest"`
	DigestText     string    `json:"digest_text"`
	DurationMS     float64   `json:"duration_ms"`
	LockTimeMS     float64   `json:"lock_time_ms"`
	RowsAffected   uint64    `json:"rows_affected"`
	RowsSent       uint64    `json:"rows_sent"`
	RowsExamined   uint64    `json:"rows_examined"`
	CreatedTmpDisk uint64    `json:"created_tmp_disk_tables"`
	NoIndexUsed    bool      `json:"no_index_used"`
	ErrorCount     uint64    `json:"error_count"`
	WarningCount   uint64    `json:"warning_count"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
	CollectedAt    time.Time `json:"collected_at"`
}

type StatementEventQuery struct {
	Start             time.Time
	End               time.Time
	MinimumDurationMS float64
	Cluster           string
	Machine           string
	Port              int
	Database          string
	Keyword           string
	Limit             int
}

// DigestSnapshot contains cumulative MySQL counters. API aggregation must use
// deltas between adjacent snapshots; a snapshot is never itself a time window.
type DigestSnapshot struct {
	ID              string    `json:"id"`
	Instance        Instance  `json:"instance"`
	ServerBootID    int64     `json:"server_boot_id"`
	Digest          string    `json:"digest"`
	DigestText      string    `json:"digest_text"`
	Database        string    `json:"database"`
	Count           uint64    `json:"count"`
	SumTimerWaitMS  float64   `json:"sum_timer_wait_ms"`
	MaxTimerWaitMS  float64   `json:"max_timer_wait_ms"`
	SumLockTimeMS   float64   `json:"sum_lock_time_ms"`
	SumRowsAffected uint64    `json:"sum_rows_affected"`
	SumRowsSent     uint64    `json:"sum_rows_sent"`
	SumRowsExamined uint64    `json:"sum_rows_examined"`
	SumErrors       uint64    `json:"sum_errors"`
	SumWarnings     uint64    `json:"sum_warnings"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	CollectedAt     time.Time `json:"collected_at"`
}

// InstanceStatus makes partial failures explicit instead of silently returning
// incomplete cluster-wide results.
type InstanceStatus struct {
	Instance                   Instance  `json:"instance"`
	Status                     string    `json:"status"`
	CollectionMode             string    `json:"collection_mode"`
	LastAttemptAt              time.Time `json:"last_attempt_at"`
	LastSuccessAt              time.Time `json:"last_success_at"`
	LastError                  string    `json:"last_error,omitempty"`
	LiveSessionCount           int       `json:"live_session_count"`
	PerformanceSchemaAvailable bool      `json:"performance_schema_available"`
	HistoryLongConsumerEnabled bool      `json:"history_long_consumer_enabled"`
	DigestConsumerEnabled      bool      `json:"digest_consumer_enabled"`
	SlowLogTableAvailable      bool      `json:"slow_log_table_available"`
	SlowLogThresholdMS         int64     `json:"slow_log_threshold_ms"`
	ServerClockOffsetMS        int64     `json:"server_clock_offset_ms"`
	SQLTextLimit               int       `json:"sql_text_limit"`
}

type KillAudit struct {
	ID                string     `json:"id"`
	Instance          Instance   `json:"instance"`
	ProcessID         uint64     `json:"process_id"`
	ExpectedDigest    string     `json:"expected_digest"`
	ExpectedStartedAt time.Time  `json:"expected_started_at"`
	SQLText           string     `json:"sql_text"`
	User              string     `json:"user"`
	ClientHost        string     `json:"client_host"`
	Reason            string     `json:"reason"`
	RequestSource     string     `json:"request_source"`
	Status            string     `json:"status"`
	Error             string     `json:"error,omitempty"`
	RequestedAt       time.Time  `json:"requested_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[pos:])
}
