package flamegraph

import (
	"context"
	"time"
)

const (
	TargetSystem  = "system"
	TargetPID     = "pid"
	TargetProcess = "process"

	BackendAuto   = "auto"
	BackendPerf   = "perf"
	BackendProcFS = "procfs"

	ScheduleOnce     = "once"
	ScheduleInterval = "interval"
	ScheduleDaily    = "daily"

	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Profile is one durable flame graph capture. FoldedStacks uses the standard
// "root;child;leaf count" representation so it can be rendered or exported
// without depending on Brendan Gregg's Perl scripts at runtime.
type Profile struct {
	ID            string     `json:"id"`
	ScheduleID    string     `json:"schedule_id,omitempty"`
	TaskID        string     `json:"task_id,omitempty"`
	Cluster       string     `json:"cluster"`
	MachineID     string     `json:"machine_id"`
	MachineName   string     `json:"machine_name,omitempty"`
	MachineIP     string     `json:"machine_ip,omitempty"`
	TargetType    string     `json:"target_type"`
	Target        string     `json:"target,omitempty"`
	DurationSec   int        `json:"duration_seconds"`
	FrequencyHz   int        `json:"frequency_hz"`
	RequestedTool string     `json:"requested_backend"`
	Backend       string     `json:"backend,omitempty"`
	Status        string     `json:"status"`
	SampleCount   int64      `json:"sample_count"`
	StackCount    int        `json:"stack_count"`
	FoldedStacks  string     `json:"folded_stacks,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Schedule describes when a capture window starts. DurationSec is the length
// of that window; interval schedules use IntervalMinutes between starts.
type Schedule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Cluster         string    `json:"cluster"`
	MachineID       string    `json:"machine_id"`
	TargetType      string    `json:"target_type"`
	Target          string    `json:"target,omitempty"`
	DurationSec     int       `json:"duration_seconds"`
	FrequencyHz     int       `json:"frequency_hz"`
	Backend         string    `json:"backend"`
	ScheduleType    string    `json:"schedule_type"`
	IntervalMinutes int       `json:"interval_minutes,omitempty"`
	StartAt         time.Time `json:"start_at"`
	Enabled         bool      `json:"enabled"`
	LastRunAt       time.Time `json:"last_run_at,omitempty"`
	NextRunAt       time.Time `json:"next_run_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Repository interface {
	CreateProfile(context.Context, Profile) error
	AttachProfileTask(context.Context, string, string) error
	CompleteProfile(context.Context, string, string, string, int64, int, string, string, time.Time, time.Time) error
	GetProfile(context.Context, string) (Profile, bool, error)
	ListProfiles(context.Context, string, int) ([]Profile, error)
	DeleteProfile(context.Context, string) error

	SaveSchedule(context.Context, Schedule) error
	GetSchedule(context.Context, string) (Schedule, bool, error)
	ListSchedules(context.Context, string) ([]Schedule, error)
	ListDueSchedules(context.Context, time.Time) ([]Schedule, error)
	UpdateScheduleRun(context.Context, string, time.Time, time.Time, bool) error
	DeleteSchedule(context.Context, string) error
}
