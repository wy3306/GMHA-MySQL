// Package alert defines alert rules, events and notification channels.  The
// domain is deliberately automation-neutral so future remediation and AI
// handlers can subscribe to the same durable event stream.
package alert

import (
	"context"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
)

type Severity string

const (
	SeverityNotice   Severity = "notice"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
	SeverityFatal    Severity = "fatal"
)

// ThresholdLevel lets one rule express the complete escalation policy instead
// of requiring one duplicated rule per severity.
type ThresholdLevel struct {
	Severity  Severity `json:"severity"`
	Threshold float64  `json:"threshold"`
	Enabled   bool     `json:"enabled"`
}

type Rule struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Description           string            `json:"description,omitempty"`
	Metric                string            `json:"metric"`
	Scope                 string            `json:"scope,omitempty"`
	ClusterID             string            `json:"cluster_id,omitempty"`
	Labels                map[string]string `json:"labels,omitempty"`
	Enabled               bool              `json:"enabled"`
	Operator              string            `json:"operator"`
	Threshold             float64           `json:"threshold"`
	Severity              Severity          `json:"severity"`
	Thresholds            []ThresholdLevel  `json:"thresholds,omitempty"`
	ConsecutiveCount      int               `json:"consecutive_count"`
	RepeatIntervalSeconds int               `json:"repeat_interval_seconds"`
	MaxNotifications      int               `json:"max_notifications"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

// Filter suppresses matching alerts before an event is created or delivered.
// Text patterns are exact/substring matches by default and regular expressions
// when UseRegex is enabled. IPCIDR always uses standard CIDR notation.
type Filter struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	ClusterPattern  string    `json:"cluster_pattern,omitempty"`
	MachinePattern  string    `json:"machine_pattern,omitempty"`
	IPCIDR          string    `json:"ip_cidr,omitempty"`
	CategoryPattern string    `json:"category_pattern,omitempty"`
	MessagePattern  string    `json:"message_pattern,omitempty"`
	UseRegex        bool      `json:"use_regex"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Event struct {
	ID                string            `json:"id"`
	Fingerprint       string            `json:"fingerprint"`
	RuleID            string            `json:"rule_id"`
	RuleName          string            `json:"rule_name"`
	Metric            string            `json:"metric"`
	MachineID         string            `json:"machine_id"`
	AgentID           string            `json:"agent_id"`
	ClusterID         string            `json:"cluster_id,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Severity          Severity          `json:"severity"`
	Status            string            `json:"status"`
	Value             float64           `json:"value"`
	Threshold         float64           `json:"threshold"`
	Operator          string            `json:"operator"`
	OccurrenceCount   int               `json:"occurrence_count"`
	NotificationCount int               `json:"notification_count"`
	FirstSeenAt       time.Time         `json:"first_seen_at"`
	LastSeenAt        time.Time         `json:"last_seen_at"`
	LastNotifiedAt    *time.Time        `json:"last_notified_at,omitempty"`
	ResolvedAt        *time.Time        `json:"resolved_at,omitempty"`
	AcknowledgedAt    *time.Time        `json:"acknowledged_at,omitempty"`
	AcknowledgedBy    string            `json:"acknowledged_by,omitempty"`
	SilencedUntil     *time.Time        `json:"silenced_until,omitempty"`
	AutomationState   string            `json:"automation_state,omitempty"`
}

type Channel struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Enabled         bool              `json:"enabled"`
	MinimumSeverity Severity          `json:"minimum_severity"`
	Config          map[string]string `json:"config"`
	LastStatus      string            `json:"last_status,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time        `json:"last_delivered_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type Delivery struct {
	ID          string    `json:"id"`
	EventID     string    `json:"event_id"`
	RuleName    string    `json:"rule_name"`
	Severity    Severity  `json:"severity"`
	MachineID   string    `json:"machine_id"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	ChannelType string    `json:"channel_type"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	DeliveredAt time.Time `json:"delivered_at"`
}

type EvaluationState struct {
	Fingerprint string
	RuleID      string
	Consecutive int
	LastValue   float64
	UpdatedAt   time.Time
}

type EventFilter struct {
	Status, Severity, ClusterID, Keyword string
	Limit                                int
}

type Repository interface {
	ListRules(context.Context) ([]Rule, error)
	SaveRule(context.Context, Rule) error
	DeleteRule(context.Context, string) error
	ListFilters(context.Context) ([]Filter, error)
	SaveFilter(context.Context, Filter) error
	DeleteFilter(context.Context, string) error
	ListEvents(context.Context, EventFilter) ([]Event, error)
	GetActiveEvent(context.Context, string) (Event, bool, error)
	SaveEvent(context.Context, Event) error
	UpdateEventAction(context.Context, string, string, string, *time.Time) error
	UpdateAutomationState(context.Context, string, string) error
	GetEvaluationState(context.Context, string) (EvaluationState, bool, error)
	SaveEvaluationState(context.Context, EvaluationState) error
	ListChannels(context.Context) ([]Channel, error)
	SaveChannel(context.Context, Channel) error
	DeleteChannel(context.Context, string) error
	ListDeliveries(context.Context, int) ([]Delivery, error)
	SaveDelivery(context.Context, Delivery) error
	LoadMetricConfig(context.Context, string) (dynamicdomain.DynamicCollectConfig, bool, error)
	SaveMetricConfig(context.Context, string, dynamicdomain.DynamicCollectConfig) error
}

func SeverityRank(value Severity) int {
	switch value {
	case SeverityFatal:
		return 4
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityNotice:
		return 1
	default:
		return 0
	}
}
