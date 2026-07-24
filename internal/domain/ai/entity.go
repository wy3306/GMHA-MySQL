// Package ai defines the durable configuration, conversation and execution
// plan records used by the AI operations control plane.
package ai

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("AI resource not found")
	ErrConflict = errors.New("AI resource state conflict")
)

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// Secret contains only the encrypted API key. Application services clear it
	// before returning a Provider through HTTP.
	Secret       string     `json:"secret,omitempty"`
	APIKey       string     `json:"api_key,omitempty"`
	HasAPIKey    bool       `json:"has_api_key"`
	Enabled      bool       `json:"enabled"`
	IsDefault    bool       `json:"is_default"`
	LastStatus   string     `json:"last_status,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastTestedAt *time.Time `json:"last_tested_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Settings struct {
	Enabled                 bool      `json:"enabled"`
	DefaultProviderID       string    `json:"default_provider_id,omitempty"`
	AutoAnalyzeAlerts       bool      `json:"auto_analyze_alerts"`
	AnalysisIntervalMinutes int       `json:"analysis_interval_minutes"`
	AnalysisScope           string    `json:"analysis_scope"`
	AutoExecuteLowRisk      bool      `json:"auto_execute_low_risk"`
	RequireApprovalMedium   bool      `json:"require_approval_medium"`
	AlwaysConfirmHighRisk   bool      `json:"always_confirm_high_risk"`
	AllowedActions          []string  `json:"allowed_actions"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	PlanID    string    `json:"plan_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ConversationSession struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Status        string     `json:"status"`
	MessageCount  int        `json:"message_count"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

// SessionMemory is the durable, compact context for one AI conversation.
// Summary and OpenQuestions are model-authored navigation aids only.
// ActiveIntent is populated from server-validated plans so execution-critical
// parameters never depend solely on a generated summary.
type SessionMemory struct {
	SessionID     string        `json:"session_id"`
	Enabled       bool          `json:"enabled"`
	Instructions  string        `json:"instructions,omitempty"`
	Summary       string        `json:"summary,omitempty"`
	OpenQuestions []string      `json:"open_questions,omitempty"`
	ActiveIntent  *MemoryIntent `json:"active_intent,omitempty"`
	LastMessageID string        `json:"last_message_id,omitempty"`
	MessageCount  int           `json:"message_count"`
	Revision      int           `json:"revision"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type MemoryIntent struct {
	Action     string            `json:"action"`
	TargetID   string            `json:"target_id,omitempty"`
	TargetName string            `json:"target_name,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	PlanID     string            `json:"plan_id,omitempty"`
	Status     string            `json:"status,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// PlanStep makes an AI proposal reviewable as an operations workflow instead
// of presenting one opaque action. The server may replace model-authored steps
// with authoritative preflight, execution, verification and recovery steps.
type PlanStep struct {
	Order        int    `json:"order"`
	Phase        string `json:"phase"`
	Title        string `json:"title"`
	Detail       string `json:"detail"`
	Verification string `json:"verification,omitempty"`
	OnFailure    string `json:"on_failure,omitempty"`
	Executable   bool   `json:"executable"`
}

type Plan struct {
	ID                 string            `json:"id"`
	SessionID          string            `json:"session_id,omitempty"`
	RunID              string            `json:"run_id,omitempty"`
	Title              string            `json:"title"`
	Summary            string            `json:"summary"`
	Action             string            `json:"action"`
	ActionLabel        string            `json:"action_label"`
	Risk               string            `json:"risk"`
	TargetID           string            `json:"target_id,omitempty"`
	TargetName         string            `json:"target_name,omitempty"`
	Parameters         map[string]string `json:"parameters,omitempty"`
	Evidence           []string          `json:"evidence,omitempty"`
	Steps              []PlanStep        `json:"steps,omitempty"`
	Rollback           string            `json:"rollback,omitempty"`
	Status             string            `json:"status"`
	ConfirmationPhrase string            `json:"confirmation_phrase,omitempty"`
	ExpiresAt          time.Time         `json:"expires_at"`
	TaskID             string            `json:"task_id,omitempty"`
	Error              string            `json:"error,omitempty"`
	ExecutionStage     string            `json:"execution_stage,omitempty"`
	FailureAnalysis    string            `json:"failure_analysis,omitempty"`
	RecoveryPlanID     string            `json:"recovery_plan_id,omitempty"`
	ParentPlanID       string            `json:"parent_plan_id,omitempty"`
	RecoveryDepth      int               `json:"recovery_depth,omitempty"`
	WorkflowID         string            `json:"workflow_id,omitempty"`
	OperationID        string            `json:"operation_id,omitempty"`
	DependsOn          []string          `json:"depends_on,omitempty"`
	LastObservedAt     *time.Time        `json:"last_observed_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	ExecutedAt         *time.Time        `json:"executed_at,omitempty"`
}

// WorkflowOperation is one durable, dependency-aware unit in an AI workflow.
// PlanID points at the independently safety-checked action definition while
// runtime fields record exactly how far execution progressed.
type WorkflowOperation struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	Title          string     `json:"title"`
	Action         string     `json:"action"`
	ActionLabel    string     `json:"action_label"`
	TargetID       string     `json:"target_id"`
	TargetName     string     `json:"target_name,omitempty"`
	Risk           string     `json:"risk"`
	DependsOn      []string   `json:"depends_on,omitempty"`
	Status         string     `json:"status"`
	TaskID         string     `json:"task_id,omitempty"`
	Attempt        int        `json:"attempt"`
	MaxAttempts    int        `json:"max_attempts"`
	ExecutionStage string     `json:"execution_stage,omitempty"`
	Error          string     `json:"error,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	LastObservedAt *time.Time `json:"last_observed_at,omitempty"`
}

// WorkflowCheckpoint is append-only evidence for a workflow decision. It
// allows a Manager restart to resume from the last verified boundary without
// repeating an operation whose submission result is ambiguous.
type WorkflowCheckpoint struct {
	ID                 string    `json:"id"`
	OperationID        string    `json:"operation_id,omitempty"`
	Phase              string    `json:"phase"`
	ContextFingerprint string    `json:"context_fingerprint,omitempty"`
	Summary            []string  `json:"summary,omitempty"`
	Result             string    `json:"result"`
	CreatedAt          time.Time `json:"created_at"`
}

// WorkflowRun is the durable parent state machine for one user goal. Individual
// actions remain Tasks in the task center and are linked as child task IDs.
type WorkflowRun struct {
	ID                 string               `json:"id"`
	SessionID          string               `json:"session_id,omitempty"`
	Goal               string               `json:"goal"`
	Status             string               `json:"status"`
	Risk               string               `json:"risk"`
	ConfirmationPhrase string               `json:"confirmation_phrase,omitempty"`
	Operations         []WorkflowOperation  `json:"operations"`
	Checkpoints        []WorkflowCheckpoint `json:"checkpoints,omitempty"`
	CurrentOperationID string               `json:"current_operation_id,omitempty"`
	ParentTaskID       string               `json:"parent_task_id,omitempty"`
	Error              string               `json:"error,omitempty"`
	PauseReason        string               `json:"pause_reason,omitempty"`
	ResumeRequired     bool                 `json:"resume_required"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	StartedAt          *time.Time           `json:"started_at,omitempty"`
	FinishedAt         *time.Time           `json:"finished_at,omitempty"`
}

type Finding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

type AnalysisRun struct {
	ID         string     `json:"id"`
	Trigger    string     `json:"trigger"`
	ProviderID string     `json:"provider_id,omitempty"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary,omitempty"`
	Findings   []Finding  `json:"findings,omitempty"`
	PlanIDs    []string   `json:"plan_ids,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type State struct {
	Providers []Provider            `json:"providers"`
	Settings  Settings              `json:"settings"`
	Sessions  []ConversationSession `json:"sessions,omitempty"`
	Messages  []Message             `json:"messages"`
	Memories  []SessionMemory       `json:"memories,omitempty"`
	Plans     []Plan                `json:"plans"`
	Workflows []WorkflowRun         `json:"workflows"`
	Runs      []AnalysisRun         `json:"runs"`
}

type Repository interface {
	Migrate() error
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}
