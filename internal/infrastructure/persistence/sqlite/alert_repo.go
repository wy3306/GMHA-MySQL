package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	alertdomain "gmha/internal/domain/alert"
	dynamicdomain "gmha/internal/domain/dynamic"
)

type AlertRepository struct{ db *DB }

func NewAlertRepository(db *DB) *AlertRepository { return &AlertRepository{db: db} }

func (r *AlertRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists alert_rule (
			id text primary key, name text not null, description text not null default '', metric text not null,
			scope text not null default '', cluster_id text not null default '', labels_json text not null default '{}',
			enabled integer not null default 1, operator text not null, threshold real not null, severity text not null,
			thresholds_json text not null default '[]',
			consecutive_count integer not null default 1, repeat_interval_seconds integer not null default 300,
			max_notifications integer not null default 0, created_at text not null, updated_at text not null
		);
		create index if not exists idx_alert_rule_metric on alert_rule(metric, enabled);
		create table if not exists alert_filter (
			id text primary key, name text not null, enabled integer not null default 1,
			cluster_pattern text not null default '', machine_pattern text not null default '', ip_cidr text not null default '',
			category_pattern text not null default '', message_pattern text not null default '', use_regex integer not null default 0,
			created_at text not null, updated_at text not null
		);
		create table if not exists alert_event (
			id text primary key, fingerprint text not null, rule_id text not null, rule_name text not null, metric text not null,
			machine_id text not null, agent_id text not null, cluster_id text not null default '', labels_json text not null default '{}',
			severity text not null, status text not null, value real not null, threshold real not null, operator text not null,
			occurrence_count integer not null default 1, notification_count integer not null default 0,
			first_seen_at text not null, last_seen_at text not null, last_notified_at text, resolved_at text,
			acknowledged_at text, acknowledged_by text not null default '', silenced_until text,
			automation_state text not null default 'pending'
		);
		create index if not exists idx_alert_event_active on alert_event(fingerprint, status);
		create index if not exists idx_alert_event_time on alert_event(last_seen_at desc);
		create table if not exists alert_evaluation_state (
			fingerprint text primary key, rule_id text not null, consecutive integer not null default 0,
			last_value real not null default 0, last_sample_at text not null default '', updated_at text not null
		);
		create table if not exists alert_channel (
			id text primary key, name text not null, type text not null, enabled integer not null default 1,
			minimum_severity text not null default 'warning', config_json text not null default '{}',
			recipient_roles_json text not null default '["oncall"]', content_filter_json text not null default '{}',
			recipient_ids_json text not null default '[]',
			last_status text not null default '', last_error text not null default '', last_delivered_at text,
			created_at text not null, updated_at text not null
		);
		create table if not exists alert_notification_role (
			id text primary key, name text not null unique, description text not null default '',
			enabled integer not null default 1, created_at text not null, updated_at text not null
		);
		create table if not exists alert_notification_recipient (
			id text primary key, name text not null, email text not null unique,
			role_ids_json text not null default '[]', enabled integer not null default 1,
			created_at text not null, updated_at text not null
		);
		create table if not exists alert_delivery (
			id text primary key, event_id text not null, rule_name text not null, severity text not null,
			machine_id text not null, channel_id text not null, channel_name text not null, channel_type text not null,
			status text not null, error text not null default '', delivered_at text not null
		);
		create index if not exists idx_alert_delivery_time on alert_delivery(delivered_at desc);
		create table if not exists alert_notification_outbox (
			id text primary key, event_json text not null, status text not null default 'pending',
			attempt_count integer not null default 0, last_error text not null default '',
			created_at text not null, updated_at text not null
		);
		create index if not exists idx_alert_notification_pending on alert_notification_outbox(status, updated_at);
		create table if not exists alert_metric_config (kind text primary key, config_json text not null, updated_at text not null);
	`)
	if err != nil {
		return err
	}
	_, _ = r.db.Exec(`alter table alert_rule add column thresholds_json text not null default '[]'`)
	_, _ = r.db.Exec(`alter table alert_evaluation_state add column last_sample_at text not null default ''`)
	_, _ = r.db.Exec(`alter table alert_channel add column recipient_roles_json text not null default '["oncall"]'`)
	_, _ = r.db.Exec(`alter table alert_channel add column content_filter_json text not null default '{}'`)
	_, _ = r.db.Exec(`alter table alert_channel add column recipient_ids_json text not null default '[]'`)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, role := range []struct{ id, name, description string }{
		{"role-oncall", "值班人员", "当前轮值并负责第一响应的人员"},
		{"role-dba", "DBA", "负责数据库运行、性能与高可用"},
		{"role-ops", "运维", "负责主机、网络与基础设施"},
		{"role-developer", "研发", "负责应用与业务故障协同"},
		{"role-manager", "管理者", "接收重要事件与升级通知"},
	} {
		_, _ = r.db.Exec(`insert into alert_notification_role(id,name,description,enabled,created_at,updated_at) values(?,?,?,1,?,?) on conflict do nothing`, role.id, role.name, role.description, now, now)
	}
	return nil
}

func (r *AlertRepository) ListRules(ctx context.Context) ([]alertdomain.Rule, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,description,metric,scope,cluster_id,labels_json,enabled,operator,threshold,severity,thresholds_json,consecutive_count,repeat_interval_seconds,max_notifications,created_at,updated_at from alert_rule order by severity desc,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Rule
	for rows.Next() {
		var x alertdomain.Rule
		var labels, severity, thresholds, created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.Metric, &x.Scope, &x.ClusterID, &labels, &x.Enabled, &x.Operator, &x.Threshold, &severity, &thresholds, &x.ConsecutiveCount, &x.RepeatIntervalSeconds, &x.MaxNotifications, &created, &updated); err != nil {
			return nil, err
		}
		x.Severity = alertdomain.Severity(severity)
		_ = json.Unmarshal([]byte(labels), &x.Labels)
		_ = json.Unmarshal([]byte(thresholds), &x.Thresholds)
		if len(x.Thresholds) == 0 {
			x.Thresholds = []alertdomain.ThresholdLevel{{Severity: x.Severity, Threshold: x.Threshold, Enabled: true}}
		}
		x.CreatedAt = parseAlertTime(created)
		x.UpdatedAt = parseAlertTime(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *AlertRepository) SaveRule(ctx context.Context, x alertdomain.Rule) error {
	labels, _ := json.Marshal(x.Labels)
	thresholds, _ := json.Marshal(x.Thresholds)
	_, err := r.db.ExecContext(ctx, `insert into alert_rule(id,name,description,metric,scope,cluster_id,labels_json,enabled,operator,threshold,severity,thresholds_json,consecutive_count,repeat_interval_seconds,max_notifications,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,description=excluded.description,metric=excluded.metric,scope=excluded.scope,cluster_id=excluded.cluster_id,labels_json=excluded.labels_json,enabled=excluded.enabled,operator=excluded.operator,threshold=excluded.threshold,severity=excluded.severity,thresholds_json=excluded.thresholds_json,consecutive_count=excluded.consecutive_count,repeat_interval_seconds=excluded.repeat_interval_seconds,max_notifications=excluded.max_notifications,updated_at=excluded.updated_at`, x.ID, x.Name, x.Description, x.Metric, x.Scope, x.ClusterID, string(labels), x.Enabled, x.Operator, x.Threshold, string(x.Severity), string(thresholds), x.ConsecutiveCount, x.RepeatIntervalSeconds, x.MaxNotifications, x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (r *AlertRepository) DeleteRule(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `delete from alert_rule where id=?`, id)
	return alertMutationResult(result, err)
}

func (r *AlertRepository) ListFilters(ctx context.Context) ([]alertdomain.Filter, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,enabled,cluster_pattern,machine_pattern,ip_cidr,category_pattern,message_pattern,use_regex,created_at,updated_at from alert_filter order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Filter
	for rows.Next() {
		var x alertdomain.Filter
		var created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Enabled, &x.ClusterPattern, &x.MachinePattern, &x.IPCIDR, &x.CategoryPattern, &x.MessagePattern, &x.UseRegex, &created, &updated); err != nil {
			return nil, err
		}
		x.CreatedAt, x.UpdatedAt = parseAlertTime(created), parseAlertTime(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *AlertRepository) SaveFilter(ctx context.Context, x alertdomain.Filter) error {
	_, err := r.db.ExecContext(ctx, `insert into alert_filter(id,name,enabled,cluster_pattern,machine_pattern,ip_cidr,category_pattern,message_pattern,use_regex,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,enabled=excluded.enabled,cluster_pattern=excluded.cluster_pattern,machine_pattern=excluded.machine_pattern,ip_cidr=excluded.ip_cidr,category_pattern=excluded.category_pattern,message_pattern=excluded.message_pattern,use_regex=excluded.use_regex,updated_at=excluded.updated_at`, x.ID, x.Name, x.Enabled, x.ClusterPattern, x.MachinePattern, x.IPCIDR, x.CategoryPattern, x.MessagePattern, x.UseRegex, x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (r *AlertRepository) DeleteFilter(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `delete from alert_filter where id=?`, id)
	return alertMutationResult(result, err)
}

func (r *AlertRepository) ListEvents(ctx context.Context, f alertdomain.EventFilter) ([]alertdomain.Event, error) {
	query := `select id,fingerprint,rule_id,rule_name,metric,machine_id,agent_id,cluster_id,labels_json,severity,status,value,threshold,operator,occurrence_count,notification_count,first_seen_at,last_seen_at,last_notified_at,resolved_at,acknowledged_at,acknowledged_by,silenced_until,automation_state from alert_event where 1=1`
	args := []any{}
	if f.Status != "" && f.Status != "all" {
		query += " and status=?"
		args = append(args, f.Status)
	}
	if f.Severity != "" && f.Severity != "all" {
		query += " and severity=?"
		args = append(args, f.Severity)
	}
	if f.ClusterID != "" && f.ClusterID != "all" {
		query += " and cluster_id=?"
		args = append(args, f.ClusterID)
	}
	if f.Keyword != "" {
		query += " and (rule_name like ? or metric like ? or machine_id like ? or agent_id like ? or cluster_id like ? or labels_json like ?)"
		q := "%" + f.Keyword + "%"
		args = append(args, q, q, q, q, q, q)
	}
	query += " order by last_seen_at desc"
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	query += " limit ? offset ?"
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Event
	for rows.Next() {
		x, err := scanAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type alertRowScanner interface{ Scan(...any) error }

func scanAlertEvent(row alertRowScanner) (alertdomain.Event, error) {
	var x alertdomain.Event
	var labels, severity, first, last, lastNotified, resolved, ack, silenced string
	err := row.Scan(&x.ID, &x.Fingerprint, &x.RuleID, &x.RuleName, &x.Metric, &x.MachineID, &x.AgentID, &x.ClusterID, &labels, &severity, &x.Status, &x.Value, &x.Threshold, &x.Operator, &x.OccurrenceCount, &x.NotificationCount, &first, &last, &lastNotified, &resolved, &ack, &x.AcknowledgedBy, &silenced, &x.AutomationState)
	if err != nil {
		return x, err
	}
	x.Severity = alertdomain.Severity(severity)
	_ = json.Unmarshal([]byte(labels), &x.Labels)
	x.FirstSeenAt = parseAlertTime(first)
	x.LastSeenAt = parseAlertTime(last)
	x.LastNotifiedAt = parseAlertTimePtr(lastNotified)
	x.ResolvedAt = parseAlertTimePtr(resolved)
	x.AcknowledgedAt = parseAlertTimePtr(ack)
	x.SilencedUntil = parseAlertTimePtr(silenced)
	return x, nil
}
func (r *AlertRepository) GetActiveEvent(ctx context.Context, fp string) (alertdomain.Event, bool, error) {
	row := r.db.QueryRowContext(ctx, `select id,fingerprint,rule_id,rule_name,metric,machine_id,agent_id,cluster_id,labels_json,severity,status,value,threshold,operator,occurrence_count,notification_count,first_seen_at,last_seen_at,last_notified_at,resolved_at,acknowledged_at,acknowledged_by,silenced_until,automation_state from alert_event where fingerprint=? and status='firing' order by last_seen_at desc limit 1`, fp)
	x, err := scanAlertEvent(row)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return x, false, nil
		}
		return x, false, err
	}
	return x, true, nil
}
func (r *AlertRepository) ListActiveEventsForRuleTarget(ctx context.Context, ruleID, machineID string) ([]alertdomain.Event, error) {
	rows, err := r.db.QueryContext(ctx, `select id,fingerprint,rule_id,rule_name,metric,machine_id,agent_id,cluster_id,labels_json,severity,status,value,threshold,operator,occurrence_count,notification_count,first_seen_at,last_seen_at,last_notified_at,resolved_at,acknowledged_at,acknowledged_by,silenced_until,automation_state from alert_event where rule_id=? and machine_id=? and status='firing' order by last_seen_at desc`, ruleID, machineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]alertdomain.Event, 0)
	for rows.Next() {
		x, err := scanAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *AlertRepository) SaveEvent(ctx context.Context, x alertdomain.Event) error {
	labels, _ := json.Marshal(x.Labels)
	_, err := r.db.ExecContext(ctx, `insert into alert_event(id,fingerprint,rule_id,rule_name,metric,machine_id,agent_id,cluster_id,labels_json,severity,status,value,threshold,operator,occurrence_count,notification_count,first_seen_at,last_seen_at,last_notified_at,resolved_at,acknowledged_at,acknowledged_by,silenced_until,automation_state) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set fingerprint=excluded.fingerprint,cluster_id=excluded.cluster_id,labels_json=excluded.labels_json,severity=excluded.severity,status=excluded.status,value=excluded.value,threshold=excluded.threshold,operator=excluded.operator,occurrence_count=excluded.occurrence_count,notification_count=excluded.notification_count,last_seen_at=excluded.last_seen_at,last_notified_at=excluded.last_notified_at,resolved_at=excluded.resolved_at,acknowledged_at=excluded.acknowledged_at,acknowledged_by=excluded.acknowledged_by,silenced_until=excluded.silenced_until,automation_state=excluded.automation_state`, x.ID, x.Fingerprint, x.RuleID, x.RuleName, x.Metric, x.MachineID, x.AgentID, x.ClusterID, string(labels), string(x.Severity), x.Status, x.Value, x.Threshold, x.Operator, x.OccurrenceCount, x.NotificationCount, x.FirstSeenAt.Format(time.RFC3339Nano), x.LastSeenAt.Format(time.RFC3339Nano), formatAlertTime(x.LastNotifiedAt), formatAlertTime(x.ResolvedAt), formatAlertTime(x.AcknowledgedAt), x.AcknowledgedBy, formatAlertTime(x.SilencedUntil), x.AutomationState)
	return err
}
func (r *AlertRepository) UpdateEventAction(ctx context.Context, id, action, actor string, until *time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch action {
	case "acknowledge":
		result, err := r.db.ExecContext(ctx, `update alert_event set acknowledged_at=?,acknowledged_by=? where id=?`, now, actor, id)
		return alertMutationResult(result, err)
	case "silence":
		result, err := r.db.ExecContext(ctx, `update alert_event set silenced_until=? where id=?`, formatAlertTime(until), id)
		return alertMutationResult(result, err)
	case "resolve":
		result, err := r.db.ExecContext(ctx, `update alert_event set status='resolved',resolved_at=? where id=?`, now, id)
		return alertMutationResult(result, err)
	}
	return errors.New("invalid alert event action")
}
func (r *AlertRepository) UpdateAutomationState(ctx context.Context, id, state, expectedState string) error {
	query := `update alert_event set automation_state=? where id=?`
	args := []any{state, id}
	if expectedState != "" {
		query += ` and automation_state=?`
		args = append(args, expectedState)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected > 0 {
		return err
	}
	var current string
	if err := r.db.QueryRowContext(ctx, `select automation_state from alert_event where id=?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return alertdomain.ErrNotFound
		}
		return err
	}
	if current == state || expectedState == "" {
		return nil
	}
	return alertdomain.ErrConflict
}
func (r *AlertRepository) GetEvaluationState(ctx context.Context, fp string) (alertdomain.EvaluationState, bool, error) {
	var x alertdomain.EvaluationState
	var sampled, updated string
	err := r.db.QueryRowContext(ctx, `select fingerprint,rule_id,consecutive,last_value,last_sample_at,updated_at from alert_evaluation_state where fingerprint=?`, fp).Scan(&x.Fingerprint, &x.RuleID, &x.Consecutive, &x.LastValue, &sampled, &updated)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return x, false, nil
		}
		return x, false, err
	}
	x.LastSampleAt, x.UpdatedAt = parseAlertTime(sampled), parseAlertTime(updated)
	return x, true, nil
}
func (r *AlertRepository) SaveEvaluationState(ctx context.Context, x alertdomain.EvaluationState) error {
	_, err := r.db.ExecContext(ctx, `insert into alert_evaluation_state(fingerprint,rule_id,consecutive,last_value,last_sample_at,updated_at) values(?,?,?,?,?,?) on conflict(fingerprint) do update set consecutive=excluded.consecutive,last_value=excluded.last_value,last_sample_at=excluded.last_sample_at,updated_at=excluded.updated_at`, x.Fingerprint, x.RuleID, x.Consecutive, x.LastValue, formatAlertTimeValue(x.LastSampleAt), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) ClassifyMySQLRestart(ctx context.Context, machineID string, port int, bootAt, observedAt time.Time) (alertdomain.MySQLRestartClassification, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id,type,status,spec_json,created_at,coalesce(started_at,''),coalesce(finished_at,'')
		from tasks
		where machine_id=? and created_at>=? and created_at<=?
		order by created_at desc`,
		machineID, observedAt.Add(-24*time.Hour).Format(time.RFC3339Nano), observedAt.Add(2*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		return alertdomain.MySQLRestartClassification{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, taskType, status, rawSpec, createdText, startedText, finishedText string
		if err := rows.Scan(&id, &taskType, &status, &rawSpec, &createdText, &startedText, &finishedText); err != nil {
			return alertdomain.MySQLRestartClassification{}, err
		}
		if status == "pending" || status == "skipped" {
			continue
		}
		startedAt := parseAlertTime(startedText)
		if startedAt.IsZero() {
			startedAt = parseAlertTime(createdText)
		}
		finishedAt := parseAlertTime(finishedText)
		if bootAt.Before(startedAt.Add(-2 * time.Minute)) {
			continue
		}
		if !finishedAt.IsZero() && bootAt.After(finishedAt.Add(10*time.Minute)) {
			continue
		}
		var spec struct {
			Operation string `json:"operation"`
			Port      int    `json:"port"`
			Command   string `json:"command"`
			Commands  []struct {
				Command string `json:"command"`
			} `json:"commands"`
		}
		if json.Unmarshal([]byte(rawSpec), &spec) != nil {
			continue
		}
		if port > 0 && spec.Port > 0 && port != spec.Port {
			continue
		}
		if !manualMySQLRestartTask(taskType, spec.Operation, spec.Command, spec.Commands) {
			continue
		}
		return alertdomain.MySQLRestartClassification{Manual: true, TaskID: id, Operation: spec.Operation}, nil
	}
	return alertdomain.MySQLRestartClassification{}, rows.Err()
}

func manualMySQLRestartTask(taskType, operation, command string, commands []struct {
	Command string `json:"command"`
}) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	switch operation {
	case "mysql_restart", "ai_restart_mysql", "mysql_parameters_apply", "mysql_upgrade",
		"mysql_architecture_step", "mysql_cluster_rolling_upgrade":
		return true
	}
	for _, prefix := range []string{"mysql_cluster_upgrade_", "mysql_restore_"} {
		if strings.HasPrefix(operation, prefix) {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "mysql_upgrade", "mysql_topology", "architecture_adjustment", "mysql_cluster_bootstrap", "mysql_cluster_upgrade":
		return true
	}
	combined := strings.ToLower(command)
	for _, step := range commands {
		combined += "\n" + strings.ToLower(step.Command)
	}
	return strings.Contains(combined, "restart") &&
		(strings.Contains(combined, "mysqld") || strings.Contains(combined, "mysql"))
}

func (r *AlertRepository) ListChannels(ctx context.Context) ([]alertdomain.Channel, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,type,enabled,minimum_severity,recipient_roles_json,recipient_ids_json,content_filter_json,config_json,last_status,last_error,last_delivered_at,created_at,updated_at from alert_channel order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Channel
	for rows.Next() {
		var x alertdomain.Channel
		var sev, roles, recipients, contentFilter, cfg, last, created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Type, &x.Enabled, &sev, &roles, &recipients, &contentFilter, &cfg, &x.LastStatus, &x.LastError, &last, &created, &updated); err != nil {
			return nil, err
		}
		x.MinimumSeverity = alertdomain.Severity(sev)
		_ = json.Unmarshal([]byte(roles), &x.RecipientRoles)
		_ = json.Unmarshal([]byte(recipients), &x.RecipientIDs)
		_ = json.Unmarshal([]byte(contentFilter), &x.ContentFilter)
		_ = json.Unmarshal([]byte(cfg), &x.Config)
		x.LastDeliveredAt = parseAlertTimePtr(last)
		x.CreatedAt = parseAlertTime(created)
		x.UpdatedAt = parseAlertTime(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *AlertRepository) SaveChannel(ctx context.Context, x alertdomain.Channel) error {
	cfg, _ := json.Marshal(x.Config)
	roles, _ := json.Marshal(x.RecipientRoles)
	recipients, _ := json.Marshal(x.RecipientIDs)
	contentFilter, _ := json.Marshal(x.ContentFilter)
	_, err := r.db.ExecContext(ctx, `insert into alert_channel(id,name,type,enabled,minimum_severity,recipient_roles_json,recipient_ids_json,content_filter_json,config_json,last_status,last_error,last_delivered_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,type=excluded.type,enabled=excluded.enabled,minimum_severity=excluded.minimum_severity,recipient_roles_json=excluded.recipient_roles_json,recipient_ids_json=excluded.recipient_ids_json,content_filter_json=excluded.content_filter_json,config_json=excluded.config_json,last_status=excluded.last_status,last_error=excluded.last_error,last_delivered_at=excluded.last_delivered_at,updated_at=excluded.updated_at`, x.ID, x.Name, x.Type, x.Enabled, string(x.MinimumSeverity), string(roles), string(recipients), string(contentFilter), string(cfg), x.LastStatus, x.LastError, formatAlertTime(x.LastDeliveredAt), x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) ListNotificationRoles(ctx context.Context) ([]alertdomain.NotificationRole, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,description,enabled,created_at,updated_at from alert_notification_role order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]alertdomain.NotificationRole, 0)
	for rows.Next() {
		var x alertdomain.NotificationRole
		var created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Description, &x.Enabled, &created, &updated); err != nil {
			return nil, err
		}
		x.CreatedAt, x.UpdatedAt = parseAlertTime(created), parseAlertTime(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *AlertRepository) SaveNotificationRole(ctx context.Context, x alertdomain.NotificationRole) error {
	_, err := r.db.ExecContext(ctx, `insert into alert_notification_role(id,name,description,enabled,created_at,updated_at) values(?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,description=excluded.description,enabled=excluded.enabled,updated_at=excluded.updated_at`, x.ID, x.Name, x.Description, x.Enabled, x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) DeleteNotificationRole(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `delete from alert_notification_role where id=?`, id)
	return alertMutationResult(result, err)
}

func (r *AlertRepository) ListNotificationRecipients(ctx context.Context) ([]alertdomain.NotificationRecipient, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,email,role_ids_json,enabled,created_at,updated_at from alert_notification_recipient order by name,email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]alertdomain.NotificationRecipient, 0)
	for rows.Next() {
		var x alertdomain.NotificationRecipient
		var roleIDs, created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Email, &roleIDs, &x.Enabled, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(roleIDs), &x.RoleIDs)
		x.CreatedAt, x.UpdatedAt = parseAlertTime(created), parseAlertTime(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *AlertRepository) SaveNotificationRecipient(ctx context.Context, x alertdomain.NotificationRecipient) error {
	roleIDs, _ := json.Marshal(x.RoleIDs)
	_, err := r.db.ExecContext(ctx, `insert into alert_notification_recipient(id,name,email,role_ids_json,enabled,created_at,updated_at) values(?,?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,email=excluded.email,role_ids_json=excluded.role_ids_json,enabled=excluded.enabled,updated_at=excluded.updated_at`, x.ID, x.Name, x.Email, string(roleIDs), x.Enabled, x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) DeleteNotificationRecipient(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `delete from alert_notification_recipient where id=?`, id)
	return alertMutationResult(result, err)
}
func (r *AlertRepository) UpdateChannelDeliveryStatus(ctx context.Context, id, status, lastError string, lastDeliveredAt *time.Time, updatedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `update alert_channel set last_status=?,last_error=?,last_delivered_at=?,updated_at=? where id=?`, status, lastError, formatAlertTime(lastDeliveredAt), updatedAt.UTC().Format(time.RFC3339Nano), id)
	return alertMutationResult(result, err)
}
func (r *AlertRepository) DeleteChannel(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `delete from alert_channel where id=?`, id)
	return alertMutationResult(result, err)
}
func (r *AlertRepository) ListDeliveries(ctx context.Context, limit int) ([]alertdomain.Delivery, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `select id,event_id,rule_name,severity,machine_id,channel_id,channel_name,channel_type,status,error,delivered_at from alert_delivery order by delivered_at desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Delivery
	for rows.Next() {
		var x alertdomain.Delivery
		var severity, delivered string
		if err := rows.Scan(&x.ID, &x.EventID, &x.RuleName, &severity, &x.MachineID, &x.ChannelID, &x.ChannelName, &x.ChannelType, &x.Status, &x.Error, &delivered); err != nil {
			return nil, err
		}
		x.Severity, x.DeliveredAt = alertdomain.Severity(severity), parseAlertTime(delivered)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *AlertRepository) SaveDelivery(ctx context.Context, x alertdomain.Delivery) error {
	_, err := r.db.ExecContext(ctx, `insert into alert_delivery(id,event_id,rule_name,severity,machine_id,channel_id,channel_name,channel_type,status,error,delivered_at) values(?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.EventID, x.RuleName, string(x.Severity), x.MachineID, x.ChannelID, x.ChannelName, x.ChannelType, x.Status, x.Error, x.DeliveredAt.Format(time.RFC3339Nano))
	return err
}
func (r *AlertRepository) SaveNotificationJob(ctx context.Context, job alertdomain.NotificationJob) error {
	eventJSON, err := json.Marshal(job.Event)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `insert into alert_notification_outbox(id,event_json,status,attempt_count,last_error,created_at,updated_at) values(?,?,'pending',0,'',?,?) on conflict(id) do nothing`,
		job.ID, string(eventJSON), job.CreatedAt.UTC().Format(time.RFC3339Nano), job.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (r *AlertRepository) ListPendingNotificationJobs(ctx context.Context, before time.Time, limit int) ([]alertdomain.NotificationJob, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `select id,event_json,attempt_count,last_error,created_at,updated_at from alert_notification_outbox where status='pending' and updated_at<=? order by updated_at limit ?`, before.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]alertdomain.NotificationJob, 0)
	for rows.Next() {
		var job alertdomain.NotificationJob
		var eventJSON, created, updated string
		if err := rows.Scan(&job.ID, &eventJSON, &job.Attempts, &job.LastError, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(eventJSON), &job.Event); err != nil {
			return nil, err
		}
		job.CreatedAt, job.UpdatedAt = parseAlertTime(created), parseAlertTime(updated)
		out = append(out, job)
	}
	return out, rows.Err()
}
func (r *AlertRepository) FinishNotificationJob(ctx context.Context, id string, succeeded bool, lastError string, now time.Time) error {
	if succeeded {
		result, err := r.db.ExecContext(ctx, `delete from alert_notification_outbox where id=?`, id)
		return alertMutationResult(result, err)
	}
	result, err := r.db.ExecContext(ctx, `update alert_notification_outbox set attempt_count=attempt_count+1,last_error=?,updated_at=? where id=?`, lastError, now.UTC().Format(time.RFC3339Nano), id)
	return alertMutationResult(result, err)
}
func (r *AlertRepository) CountPendingNotificationJobs(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `select count(*) from alert_notification_outbox where status='pending'`).Scan(&count)
	return count, err
}
func (r *AlertRepository) LoadMetricConfig(ctx context.Context, kind string) (dynamicdomain.DynamicCollectConfig, bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `select config_json from alert_metric_config where kind=?`, kind).Scan(&raw)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return dynamicdomain.DynamicCollectConfig{}, false, nil
		}
		return dynamicdomain.DynamicCollectConfig{}, false, err
	}
	var cfg dynamicdomain.DynamicCollectConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, false, err
	}
	return cfg, true, nil
}
func (r *AlertRepository) SaveMetricConfig(ctx context.Context, kind string, cfg dynamicdomain.DynamicCollectConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `insert into alert_metric_config(kind,config_json,updated_at) values(?,?,?) on conflict(kind) do update set config_json=excluded.config_json,updated_at=excluded.updated_at`, kind, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) SummarizeEvents(ctx context.Context, now time.Time) (alertdomain.EventSummary, error) {
	out := alertdomain.EventSummary{Counts: map[string]int{"firing": 0, "resolved": 0, "notice": 0, "warning": 0, "critical": 0, "fatal": 0}}
	rows, err := r.db.QueryContext(ctx, `select status,severity,count(*) from alert_event group by status,severity`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var status, severity string
		var count int
		if err := rows.Scan(&status, &severity, &count); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts[status] += count
		out.Total += count
		if status == "firing" {
			out.Counts[severity] += count
		}
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := r.db.QueryRowContext(ctx, `select count(*) from alert_event where status='firing' and acknowledged_at<>''`).Scan(&out.ActiveAcknowledged); err != nil {
		return out, err
	}
	if err := r.db.QueryRowContext(ctx, `select count(*) from alert_event where status='firing' and silenced_until>?`, now.UTC().Format(time.RFC3339Nano)).Scan(&out.ActiveSilenced); err != nil {
		return out, err
	}
	if err := r.db.QueryRowContext(ctx, `select count(*) from alert_event where last_seen_at>=?`, now.UTC().Add(-24*time.Hour).Format(time.RFC3339Nano)).Scan(&out.Last24Hours); err != nil {
		return out, err
	}
	return out, nil
}

func alertMutationResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return alertdomain.ErrNotFound
	}
	return nil
}
func parseAlertTime(v string) time.Time { t, _ := time.Parse(time.RFC3339Nano, v); return t }
func parseAlertTimePtr(v string) *time.Time {
	if v == "" {
		return nil
	}
	t := parseAlertTime(v)
	return &t
}
func formatAlertTime(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.UTC().Format(time.RFC3339Nano)
}
func formatAlertTimeValue(v time.Time) string {
	if v.IsZero() {
		return ""
	}
	return v.UTC().Format(time.RFC3339Nano)
}
