package sqlite

import (
	"context"
	"encoding/json"
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
			last_value real not null default 0, updated_at text not null
		);
		create table if not exists alert_channel (
			id text primary key, name text not null, type text not null, enabled integer not null default 1,
			minimum_severity text not null default 'warning', config_json text not null default '{}',
			last_status text not null default '', last_error text not null default '', last_delivered_at text,
			created_at text not null, updated_at text not null
		);
		create table if not exists alert_delivery (
			id text primary key, event_id text not null, rule_name text not null, severity text not null,
			machine_id text not null, channel_id text not null, channel_name text not null, channel_type text not null,
			status text not null, error text not null default '', delivered_at text not null
		);
		create index if not exists idx_alert_delivery_time on alert_delivery(delivered_at desc);
		create table if not exists alert_metric_config (kind text primary key, config_json text not null, updated_at text not null);
	`)
	if err != nil {
		return err
	}
	_, _ = r.db.Exec(`alter table alert_rule add column thresholds_json text not null default '[]'`)
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
	_, err := r.db.ExecContext(ctx, `delete from alert_rule where id=?`, id)
	return err
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
	_, err := r.db.ExecContext(ctx, `delete from alert_filter where id=?`, id)
	return err
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
		query += " and (rule_name like ? or metric like ? or machine_id like ?)"
		q := "%" + f.Keyword + "%"
		args = append(args, q, q, q)
	}
	query += " order by last_seen_at desc"
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	query += " limit ?"
	args = append(args, f.Limit)
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
func (r *AlertRepository) SaveEvent(ctx context.Context, x alertdomain.Event) error {
	labels, _ := json.Marshal(x.Labels)
	_, err := r.db.ExecContext(ctx, `insert into alert_event(id,fingerprint,rule_id,rule_name,metric,machine_id,agent_id,cluster_id,labels_json,severity,status,value,threshold,operator,occurrence_count,notification_count,first_seen_at,last_seen_at,last_notified_at,resolved_at,acknowledged_at,acknowledged_by,silenced_until,automation_state) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set cluster_id=excluded.cluster_id,labels_json=excluded.labels_json,severity=excluded.severity,status=excluded.status,value=excluded.value,threshold=excluded.threshold,operator=excluded.operator,occurrence_count=excluded.occurrence_count,notification_count=excluded.notification_count,last_seen_at=excluded.last_seen_at,last_notified_at=excluded.last_notified_at,resolved_at=excluded.resolved_at,acknowledged_at=excluded.acknowledged_at,acknowledged_by=excluded.acknowledged_by,silenced_until=excluded.silenced_until,automation_state=excluded.automation_state`, x.ID, x.Fingerprint, x.RuleID, x.RuleName, x.Metric, x.MachineID, x.AgentID, x.ClusterID, string(labels), string(x.Severity), x.Status, x.Value, x.Threshold, x.Operator, x.OccurrenceCount, x.NotificationCount, x.FirstSeenAt.Format(time.RFC3339Nano), x.LastSeenAt.Format(time.RFC3339Nano), formatAlertTime(x.LastNotifiedAt), formatAlertTime(x.ResolvedAt), formatAlertTime(x.AcknowledgedAt), x.AcknowledgedBy, formatAlertTime(x.SilencedUntil), x.AutomationState)
	return err
}
func (r *AlertRepository) UpdateEventAction(ctx context.Context, id, action, actor string, until *time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch action {
	case "acknowledge":
		_, err := r.db.ExecContext(ctx, `update alert_event set acknowledged_at=?,acknowledged_by=? where id=?`, now, actor, id)
		return err
	case "silence":
		_, err := r.db.ExecContext(ctx, `update alert_event set silenced_until=? where id=?`, formatAlertTime(until), id)
		return err
	case "resolve":
		_, err := r.db.ExecContext(ctx, `update alert_event set status='resolved',resolved_at=? where id=?`, now, id)
		return err
	}
	return nil
}
func (r *AlertRepository) UpdateAutomationState(ctx context.Context, id, state string) error {
	_, err := r.db.ExecContext(ctx, `update alert_event set automation_state=? where id=?`, state, id)
	return err
}
func (r *AlertRepository) GetEvaluationState(ctx context.Context, fp string) (alertdomain.EvaluationState, bool, error) {
	var x alertdomain.EvaluationState
	var updated string
	err := r.db.QueryRowContext(ctx, `select fingerprint,rule_id,consecutive,last_value,updated_at from alert_evaluation_state where fingerprint=?`, fp).Scan(&x.Fingerprint, &x.RuleID, &x.Consecutive, &x.LastValue, &updated)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return x, false, nil
		}
		return x, false, err
	}
	x.UpdatedAt = parseAlertTime(updated)
	return x, true, nil
}
func (r *AlertRepository) SaveEvaluationState(ctx context.Context, x alertdomain.EvaluationState) error {
	_, err := r.db.ExecContext(ctx, `insert into alert_evaluation_state(fingerprint,rule_id,consecutive,last_value,updated_at) values(?,?,?,?,?) on conflict(fingerprint) do update set consecutive=excluded.consecutive,last_value=excluded.last_value,updated_at=excluded.updated_at`, x.Fingerprint, x.RuleID, x.Consecutive, x.LastValue, x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *AlertRepository) ListChannels(ctx context.Context) ([]alertdomain.Channel, error) {
	rows, err := r.db.QueryContext(ctx, `select id,name,type,enabled,minimum_severity,config_json,last_status,last_error,last_delivered_at,created_at,updated_at from alert_channel order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertdomain.Channel
	for rows.Next() {
		var x alertdomain.Channel
		var sev, cfg, last, created, updated string
		if err := rows.Scan(&x.ID, &x.Name, &x.Type, &x.Enabled, &sev, &cfg, &x.LastStatus, &x.LastError, &last, &created, &updated); err != nil {
			return nil, err
		}
		x.MinimumSeverity = alertdomain.Severity(sev)
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
	_, err := r.db.ExecContext(ctx, `insert into alert_channel(id,name,type,enabled,minimum_severity,config_json,last_status,last_error,last_delivered_at,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set name=excluded.name,type=excluded.type,enabled=excluded.enabled,minimum_severity=excluded.minimum_severity,config_json=excluded.config_json,last_status=excluded.last_status,last_error=excluded.last_error,last_delivered_at=excluded.last_delivered_at,updated_at=excluded.updated_at`, x.ID, x.Name, x.Type, x.Enabled, string(x.MinimumSeverity), string(cfg), x.LastStatus, x.LastError, formatAlertTime(x.LastDeliveredAt), x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (r *AlertRepository) DeleteChannel(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `delete from alert_channel where id=?`, id)
	return err
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
