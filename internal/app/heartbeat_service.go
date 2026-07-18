package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	agentdomain "gmha/internal/domain/agent"
	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
	hbgrpc "gmha/pkg/rpc/heartbeat"
)

// HeartbeatConfig 是心跳服务的配置参数。
type HeartbeatConfig struct {
	SuspectAfter  time.Duration
	OfflineAfter  time.Duration
	RecoverAfter  int
	DegradeAfter  int
	ReconcileTick time.Duration
}

// HeartbeatService 是心跳处理的核心服务，负责处理 Agent 上报的心跳数据、
// 管理 Agent 状态转换（INIT→ONLINE→SUSPECT→DEGRADED→OFFLINE）、
// 协调循环检查超时、同步操作状态到 Agent/Machine 实体、管理动态采集配置。
type HeartbeatService struct {
	repo          hbdomain.Repository
	cfg           HeartbeatConfig
	agents        agentStatusUpdater
	machines      machineStatusUpdater
	mysql         mysqlStatusUpdater
	alertObserver interface {
		ObserveHeartbeat(context.Context, hbdomain.HeartbeatPayload)
	}
	mu                 sync.RWMutex
	latest             map[string]hbdomain.LatestStatus
	metricSnapshotAt   map[string]time.Time
	dynamicConfig      dynamicdomain.DynamicCollectConfig
	mysqlDynamicConfig dynamicdomain.DynamicCollectConfig
}

// SetAlertObserver attaches the Manager-side alert engine. Evaluation is
// non-blocking and remains outside the Agent heartbeat critical path.
func (s *HeartbeatService) SetAlertObserver(observer interface {
	ObserveHeartbeat(context.Context, hbdomain.HeartbeatPayload)
}) {
	s.mu.Lock()
	s.alertObserver = observer
	s.mu.Unlock()
}

type agentStatusUpdater interface {
	UpdateState(ctx context.Context, machineID string, state agentdomain.State, lastError string) error
	UpdateHeartbeat(ctx context.Context, machineID string, at time.Time) error
}

type machineStatusUpdater interface {
	UpdateStatus(ctx context.Context, machineID string, status machinedomain.Status, lastError string) error
}

type mysqlStatusUpdater interface {
	UpdateStatus(ctx context.Context, machineID string, port int, status string) error
}

type HeartbeatView struct {
	AgentID           string                       `json:"agent_id"`
	MachineID         string                       `json:"machine_id"`
	ClusterID         string                       `json:"cluster_id"`
	Hostname          string                       `json:"hostname"`
	Version           string                       `json:"version"`
	CurrentState      hbdomain.AgentState          `json:"current_state"`
	OverallHealth     hbdomain.HealthLevel         `json:"overall_health"`
	LastHeartbeatAt   time.Time                    `json:"last_heartbeat_at"`
	LastHealthyAt     *time.Time                   `json:"last_healthy_at,omitempty"`
	LastStateChangeAt time.Time                    `json:"last_state_change_at"`
	LastErrorSummary  string                       `json:"last_error_summary"`
	Checks            []hbdomain.HealthCheck       `json:"checks"`
	Metrics           []dynamicdomain.MetricResult `json:"metrics"`
}

func NewHeartbeatService(repo hbdomain.Repository, cfg HeartbeatConfig, agentRepo agentStatusUpdater, machineRepo machineStatusUpdater, mysqlRepo mysqlStatusUpdater) *HeartbeatService {
	if cfg.SuspectAfter <= 0 {
		cfg.SuspectAfter = 15 * time.Second
	}
	if cfg.OfflineAfter <= 0 {
		cfg.OfflineAfter = 30 * time.Second
	}
	if cfg.RecoverAfter <= 0 {
		cfg.RecoverAfter = 2
	}
	if cfg.DegradeAfter <= 0 {
		cfg.DegradeAfter = 2
	}
	if cfg.ReconcileTick <= 0 {
		cfg.ReconcileTick = 5 * time.Second
	}
	return &HeartbeatService{
		repo:               repo,
		cfg:                cfg,
		agents:             agentRepo,
		machines:           machineRepo,
		mysql:              mysqlRepo,
		latest:             make(map[string]hbdomain.LatestStatus),
		metricSnapshotAt:   make(map[string]time.Time),
		dynamicConfig:      dynamicdomain.BuildDefaultDynamicCollectConfig(),
		mysqlDynamicConfig: dynamicdomain.BuildDefaultMySQLDynamicCollectConfig(),
	}
}

func (s *HeartbeatService) GetDynamicCollectConfig() dynamicdomain.DynamicCollectConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dynamicConfig
}

func (s *HeartbeatService) UpdateDynamicCollectConfig(cfg dynamicdomain.DynamicCollectConfig) dynamicdomain.DynamicCollectConfig {
	hostTasks := make([]dynamicdomain.CollectTaskSpec, 0, len(cfg.Tasks))
	for _, task := range cfg.Tasks {
		if !strings.HasPrefix(strings.TrimSpace(task.Name), "mysql_") {
			hostTasks = append(hostTasks, task)
		}
	}
	cfg.Tasks = hostTasks
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}
	if cfg.Version == "" {
		cfg.Version = cfg.UpdatedAt.Format("20060102T150405.000000000Z")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dynamicConfig = cfg
	return cfg
}

func (s *HeartbeatService) GetMySQLDynamicCollectConfig() dynamicdomain.DynamicCollectConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mysqlDynamicConfig
}

func (s *HeartbeatService) UpdateMySQLDynamicCollectConfig(cfg dynamicdomain.DynamicCollectConfig) dynamicdomain.DynamicCollectConfig {
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}
	if cfg.Version == "" {
		cfg.Version = "mysql-" + cfg.UpdatedAt.Format("20060102T150405.000000000Z")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mysqlDynamicConfig = cfg
	return cfg
}

func (s *HeartbeatService) LoadLatest(ctx context.Context) error {
	items, err := s.repo.ListLatest(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		s.latest[item.AgentID] = item
	}
	return nil
}

func (s *HeartbeatService) TickInterval() time.Duration {
	return s.cfg.ReconcileTick
}

func (s *HeartbeatService) Snapshot() []HeartbeatView {
	_ = s.LoadLatest(context.Background())
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HeartbeatView, 0, len(s.latest))
	for _, item := range s.latest {
		out = append(out, HeartbeatView{
			AgentID:           item.AgentID,
			MachineID:         item.MachineID,
			ClusterID:         item.ClusterID,
			Hostname:          item.Hostname,
			Version:           item.Version,
			CurrentState:      item.CurrentState,
			OverallHealth:     item.OverallHealth,
			LastHeartbeatAt:   item.LastHeartbeatAt,
			LastHealthyAt:     item.LastHealthyAt,
			LastStateChangeAt: item.LastStateChangeAt,
			LastErrorSummary:  item.LastErrorSummary,
			Checks:            append([]hbdomain.HealthCheck(nil), item.Checks...),
			Metrics:           append([]dynamicdomain.MetricResult(nil), item.Metrics...),
		})
	}
	return out
}

func (s *HeartbeatService) GetByMachineID(ctx context.Context, machineID string) (HeartbeatView, bool, error) {
	if err := s.LoadLatest(ctx); err != nil {
		return HeartbeatView{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.latest {
		if item.MachineID != machineID {
			continue
		}
		return HeartbeatView{
			AgentID:           item.AgentID,
			MachineID:         item.MachineID,
			ClusterID:         item.ClusterID,
			Hostname:          item.Hostname,
			Version:           item.Version,
			CurrentState:      item.CurrentState,
			OverallHealth:     item.OverallHealth,
			LastHeartbeatAt:   item.LastHeartbeatAt,
			LastHealthyAt:     item.LastHealthyAt,
			LastStateChangeAt: item.LastStateChangeAt,
			LastErrorSummary:  item.LastErrorSummary,
			Checks:            append([]hbdomain.HealthCheck(nil), item.Checks...),
			Metrics:           append([]dynamicdomain.MetricResult(nil), item.Metrics...),
		}, true, nil
	}
	return HeartbeatView{}, false, nil
}

func (s *HeartbeatService) RemoveMachine(ctx context.Context, machineID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	for agentID, item := range s.latest {
		if item.MachineID == machineID {
			delete(s.latest, agentID)
		}
	}
	s.mu.Unlock()
	if cleaner, ok := s.repo.(interface {
		DeleteByMachineID(context.Context, string) error
	}); ok {
		return cleaner.DeleteByMachineID(ctx, machineID)
	}
	return s.repo.DeleteLatestByMachineID(ctx, machineID)
}

func (s *HeartbeatService) WaitForOnline(ctx context.Context, machineID string, timeout time.Duration) error {
	if s == nil {
		return errors.New("heartbeat service not configured")
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastErr := ""
	for {
		item, ok, err := s.getByMachineIDRefreshing(waitCtx, machineID)
		if err != nil {
			return err
		}
		if ok {
			switch item.CurrentState {
			case hbdomain.StateOnline, hbdomain.StateDegraded:
				return nil
			case hbdomain.StateOffline, hbdomain.StateSuspect:
				if strings.TrimSpace(item.LastErrorSummary) != "" {
					lastErr = item.LastErrorSummary
				} else {
					lastErr = "agent heartbeat did not become healthy"
				}
			}
		}

		select {
		case <-waitCtx.Done():
			if strings.TrimSpace(lastErr) != "" {
				return errors.New(lastErr)
			}
			return errors.New("timed out waiting for first agent heartbeat")
		case <-ticker.C:
		}
	}
}

func (s *HeartbeatService) WaitForFreshHeartbeat(ctx context.Context, machineID string, startedAt time.Time, timeout time.Duration) error {
	if s == nil {
		return errors.New("heartbeat service not configured")
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		item, ok, err := s.getByMachineIDRefreshing(waitCtx, machineID)
		if err != nil {
			return err
		}
		if ok &&
			(item.CurrentState == hbdomain.StateOnline || item.CurrentState == hbdomain.StateDegraded) &&
			!item.LastHeartbeatAt.IsZero() &&
			!item.LastHeartbeatAt.Before(startedAt) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return errors.New("timed out waiting for upgraded agent heartbeat")
		case <-ticker.C:
		}
	}
}

func (s *HeartbeatService) getByMachineIDRefreshing(ctx context.Context, machineID string) (HeartbeatView, bool, error) {
	if s.repo == nil {
		return s.GetByMachineID(ctx, machineID)
	}
	items, err := s.repo.ListLatest(ctx)
	if err != nil {
		return HeartbeatView{}, false, err
	}
	s.mu.Lock()
	for _, latest := range items {
		s.latest[latest.AgentID] = latest
	}
	s.mu.Unlock()
	return s.GetByMachineID(ctx, machineID)
}

func (s *HeartbeatService) ProcessHeartbeat(ctx context.Context, req *hbgrpc.HeartbeatRequest) (*hbgrpc.HeartbeatResponse, error) {
	now := time.Now().UTC()
	payload := s.enrichAlertPayload(ctx, mapRequest(req))
	payload = s.filterUnregisteredMySQLMetrics(ctx, payload)

	s.mu.Lock()
	current, ok := s.latest[payload.AgentID]
	if !ok {
		current = hbdomain.LatestStatus{
			AgentID:           payload.AgentID,
			MachineID:         payload.MachineID,
			ClusterID:         payload.ClusterID,
			Hostname:          payload.Hostname,
			Version:           payload.Version,
			CurrentState:      hbdomain.StateInit,
			LastStateChangeAt: now,
		}
	}
	next, changed, reason := s.onReceive(current, payload, now)
	s.latest[payload.AgentID] = next
	s.mu.Unlock()

	if err := s.repo.UpsertLatestStatus(ctx, next); err != nil {
		return nil, err
	}
	if writer, ok := s.repo.(hbdomain.MetricSnapshotWriter); ok {
		metrics := s.dashboardMetricSnapshot(next.AgentID, next.Metrics, now)
		if len(metrics) > 0 {
			if err := writer.AppendMetricSnapshot(ctx, hbdomain.MetricSnapshot{
				AgentID: next.AgentID, MachineID: next.MachineID, ClusterID: next.ClusterID,
				Metrics: metrics, CollectedAt: now,
			}); err != nil {
				return nil, err
			}
		}
	}
	s.syncOperationalState(ctx, next)
	if changed {
		_ = s.repo.AppendEvent(ctx, buildEvent(current, next, reason, payload, now))
	}
	s.mu.RLock()
	observer := s.alertObserver
	s.mu.RUnlock()
	if observer != nil {
		observer.ObserveHeartbeat(ctx, withAlertHealthMetrics(payload, 1))
	}

	cfg := s.GetDynamicCollectConfig()
	mysqlCfg := s.GetMySQLDynamicCollectConfig()
	return &hbgrpc.HeartbeatResponse{
		ServerTimeUnixMS:    now.UnixMilli(),
		State:               string(next.CurrentState),
		Message:             reason,
		DynamicCollect:      &cfg,
		MySQLDynamicCollect: &mysqlCfg,
	}, nil
}

func (s *HeartbeatService) dashboardMetricSnapshot(agentID string, metrics []dynamicdomain.MetricResult, now time.Time) []dynamicdomain.MetricResult {
	s.mu.Lock()
	last := s.metricSnapshotAt[agentID]
	if !last.IsZero() && now.Sub(last) < 15*time.Second {
		s.mu.Unlock()
		return nil
	}
	s.metricSnapshotAt[agentID] = now
	s.mu.Unlock()
	wanted := map[string]bool{
		"mysql_qps": true, "mysql_tps": true, "cpu_usage_percent": true,
		"io_status": true, "filesystem_usage": true, "network_throughput": true,
	}
	out := make([]dynamicdomain.MetricResult, 0, 8)
	for _, metric := range metrics {
		if wanted[metric.Name] {
			out = append(out, metric)
		}
	}
	return out
}

// MetricHistory returns persisted Agent metric snapshots for dashboard trend
// aggregation. Repositories without history support return an empty series.
func (s *HeartbeatService) MetricHistory(ctx context.Context, clusterID string, since time.Time, limit int) ([]hbdomain.MetricSnapshot, error) {
	reader, ok := s.repo.(hbdomain.MetricSnapshotReader)
	if !ok {
		return []hbdomain.MetricSnapshot{}, nil
	}
	return reader.ListMetricSnapshots(ctx, strings.TrimSpace(clusterID), since, limit)
}

func (s *HeartbeatService) Reconcile(ctx context.Context) error {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for agentID, item := range s.latest {
		next, changed, reason := s.onTick(item, now)
		if !changed {
			continue
		}
		s.latest[agentID] = next
		if err := s.repo.UpsertLatestStatus(ctx, next); err != nil {
			return err
		}
		s.syncOperationalState(ctx, next)
		_ = s.repo.AppendEvent(ctx, buildEvent(item, next, reason, hbdomain.HeartbeatPayload{
			AgentID:   item.AgentID,
			MachineID: item.MachineID,
			ClusterID: item.ClusterID,
			Hostname:  item.Hostname,
			Version:   item.Version,
			Seq:       item.LastSeq,
		}, now))
		if s.alertObserver != nil {
			payload := s.enrichAlertPayload(ctx, hbdomain.HeartbeatPayload{AgentID: item.AgentID, MachineID: item.MachineID, ClusterID: item.ClusterID, Hostname: item.Hostname, OverallHealth: item.OverallHealth})
			s.alertObserver.ObserveHeartbeat(ctx, withAlertHealthMetrics(payload, 0))
		}
	}
	return nil
}

func (s *HeartbeatService) enrichAlertPayload(ctx context.Context, payload hbdomain.HeartbeatPayload) hbdomain.HeartbeatPayload {
	payload.MachineName = payload.Hostname
	reader, ok := s.machines.(interface {
		GetByID(context.Context, string) (machinedomain.Machine, bool, error)
	})
	if !ok {
		return payload
	}
	machine, found, err := reader.GetByID(ctx, payload.MachineID)
	if err != nil || !found {
		return payload
	}
	if payload.ClusterID == "" {
		payload.ClusterID = machine.Cluster
	}
	payload.MachineName, payload.MachineIP = machine.Name, machine.IP
	return payload
}

// filterUnregisteredMySQLMetrics prevents an Agent without mysql-heartbeat
// configuration from inventing a default :3306 instance and raising alarms.
// Only metrics that map to a Manager-registered instance are retained.
func (s *HeartbeatService) filterUnregisteredMySQLMetrics(ctx context.Context, payload hbdomain.HeartbeatPayload) hbdomain.HeartbeatPayload {
	reader, ok := s.mysql.(interface {
		Get(context.Context, string, int) (mysqlapp.Instance, bool, error)
	})
	if !ok {
		return payload
	}
	filtered := make([]dynamicdomain.MetricResult, 0, len(payload.Metrics))
	registered := make(map[int]bool)
	checked := make(map[int]bool)
	for _, metric := range payload.Metrics {
		if !strings.HasPrefix(metric.Name, "mysql_") {
			filtered = append(filtered, metric)
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(metric.Labels["mysql_port"]))
		if err != nil || port <= 0 {
			continue
		}
		if !checked[port] {
			_, registered[port], _ = reader.Get(ctx, payload.MachineID, port)
			checked[port] = true
		}
		if registered[port] {
			filtered = append(filtered, metric)
		}
	}
	payload.Metrics = filtered
	return payload
}

func withAlertHealthMetrics(payload hbdomain.HeartbeatPayload, heartbeatAlive float64) hbdomain.HeartbeatPayload {
	now := time.Now().UTC()
	healthValue := float64(0)
	if payload.OverallHealth != hbdomain.HealthHealthy {
		healthValue = 1
	}
	payload.Metrics = append(payload.Metrics,
		dynamicdomain.MetricResult{Name: "agent_heartbeat_alive", Category: "agent", Success: true, ValueType: dynamicdomain.ValueTypeFloat, Value: heartbeatAlive, CollectedAt: now, Labels: map[string]string{"metric_scope": "agent_health"}},
		dynamicdomain.MetricResult{Name: "agent_overall_health", Category: "agent", Success: true, ValueType: dynamicdomain.ValueTypeFloat, Value: healthValue, CollectedAt: now, Labels: map[string]string{"metric_scope": "agent_health"}},
	)
	for _, check := range payload.Checks {
		value := float64(0)
		if check.Status != hbdomain.CheckOK {
			value = 1
		}
		payload.Metrics = append(payload.Metrics, dynamicdomain.MetricResult{Name: "agent_health_check_failed", Category: "agent", Success: true, ValueType: dynamicdomain.ValueTypeFloat, Value: value, CollectedAt: now, Labels: map[string]string{"metric_scope": "agent_health", "check_name": check.Name}})
	}
	return payload
}

func (s *HeartbeatService) syncOperationalState(ctx context.Context, item hbdomain.LatestStatus) {
	if s.agents == nil || s.machines == nil {
		return
	}
	s.syncMySQLState(ctx, item)
	switch item.CurrentState {
	case hbdomain.StateOnline, hbdomain.StateDegraded:
		_ = s.agents.UpdateHeartbeat(ctx, item.MachineID, item.LastHeartbeatAt)
		_ = s.agents.UpdateState(ctx, item.MachineID, agentdomain.StateOnline, "")
		_ = s.machines.UpdateStatus(ctx, item.MachineID, machinedomain.StatusAgentOnline, "")
	case hbdomain.StateOffline, hbdomain.StateSuspect:
		msg := item.LastErrorSummary
		_ = s.agents.UpdateState(ctx, item.MachineID, agentdomain.StateError, msg)
		_ = s.machines.UpdateStatus(ctx, item.MachineID, machinedomain.StatusAgentError, msg)
	}
}

// machineStatusFromHeartbeat maps the live heartbeat transport state to the
// machine-management status. Health degradation is shown separately from
// connectivity, so a DEGRADED Agent remains online while SUSPECT/OFFLINE is
// consistently reported as an Agent error.
func machineStatusFromHeartbeat(state hbdomain.AgentState, lastError string) (machinedomain.Status, string, bool) {
	switch state {
	case hbdomain.StateOnline, hbdomain.StateDegraded:
		return machinedomain.StatusAgentOnline, "", true
	case hbdomain.StateOffline, hbdomain.StateSuspect:
		return machinedomain.StatusAgentError, lastError, true
	default:
		return "", "", false
	}
}

func (s *HeartbeatService) syncMySQLState(ctx context.Context, item hbdomain.LatestStatus) {
	if s.mysql == nil {
		return
	}
	for _, check := range item.Checks {
		if !strings.HasPrefix(check.Name, "mysql.heartbeat.") {
			continue
		}
		portText := strings.TrimPrefix(check.Name, "mysql.heartbeat.")
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 {
			continue
		}
		status := mysqlapp.StatusRunning
		if check.Status == hbdomain.CheckFail {
			status = mysqlapp.StatusHeartbeatFailed
			if strings.Contains(check.Detail, "systemctl start failed") {
				status = mysqlapp.StatusInstanceError
			}
		}
		_ = s.mysql.UpdateStatus(ctx, item.MachineID, port, status)
	}
}

func (s *HeartbeatService) onReceive(cur hbdomain.LatestStatus, payload hbdomain.HeartbeatPayload, now time.Time) (hbdomain.LatestStatus, bool, string) {
	next := cur
	next.AgentID = payload.AgentID
	next.MachineID = payload.MachineID
	next.ClusterID = payload.ClusterID
	next.Hostname = payload.Hostname
	next.Version = payload.Version
	next.LastHeartbeatAt = now
	next.LastSeq = payload.Seq
	next.LastBootID = payload.BootID
	next.OverallHealth = payload.OverallHealth
	next.Checks = payload.Checks
	next.Metrics = payload.Metrics
	next.UpdatedAt = now
	next.ConsecutiveMisses = 0

	hasBad := payload.OverallHealth != hbdomain.HealthHealthy
	if hasBad {
		next.ConsecutiveBadChecks++
		next.LastErrorSummary = payload.Summary
	} else {
		next.ConsecutiveBadChecks = 0
		next.LastErrorSummary = ""
		t := now
		next.LastHealthyAt = &t
	}

	prev := cur.CurrentState
	if prev == "" {
		prev = hbdomain.StateInit
	}
	if hasBad && next.ConsecutiveBadChecks >= s.cfg.DegradeAfter {
		next.CurrentState = hbdomain.StateDegraded
	} else if hasBad && (prev == hbdomain.StateOffline || prev == hbdomain.StateSuspect || prev == hbdomain.StateInit) {
		next.CurrentState = hbdomain.StateDegraded
	} else if !hasBad {
		next.CurrentState = hbdomain.StateOnline
	} else {
		next.CurrentState = prev
	}

	changed := next.CurrentState != prev
	reason := "heartbeat accepted"
	if changed {
		next.LastStateChangeAt = now
		reason = fmt.Sprintf("state changed from %s to %s", prev, next.CurrentState)
	}
	return next, changed, reason
}

func (s *HeartbeatService) onTick(cur hbdomain.LatestStatus, now time.Time) (hbdomain.LatestStatus, bool, string) {
	if cur.LastHeartbeatAt.IsZero() {
		return cur, false, ""
	}
	next := cur
	next.UpdatedAt = now
	elapsed := now.Sub(cur.LastHeartbeatAt)
	switch {
	case elapsed >= s.cfg.OfflineAfter && cur.CurrentState != hbdomain.StateOffline:
		next.CurrentState = hbdomain.StateOffline
		next.ConsecutiveMisses++
	case elapsed >= s.cfg.SuspectAfter && cur.CurrentState != hbdomain.StateSuspect && cur.CurrentState != hbdomain.StateOffline:
		next.CurrentState = hbdomain.StateSuspect
		next.ConsecutiveMisses++
	default:
		return cur, false, ""
	}
	next.LastStateChangeAt = now
	next.LastErrorSummary = fmt.Sprintf("heartbeat timeout after %s", elapsed.Round(time.Second))
	return next, true, next.LastErrorSummary
}

func mapRequest(req *hbgrpc.HeartbeatRequest) hbdomain.HeartbeatPayload {
	checks := make([]hbdomain.HealthCheck, 0, len(req.Health.Checks))
	for _, item := range req.Health.Checks {
		checks = append(checks, hbdomain.HealthCheck{
			Name:      item.Name,
			Status:    hbdomain.CheckStatus(item.Status),
			Detail:    item.Detail,
			CheckedAt: time.UnixMilli(item.CheckedAtUnixMS).UTC(),
		})
	}
	metrics := []dynamicdomain.MetricResult(nil)
	if req.Metrics != nil {
		metrics = append(metrics, tagMetricScope(req.Metrics.Items, "machine_dynamic")...)
	}
	if req.MySQLMetrics != nil {
		metrics = append(metrics, tagMetricScope(req.MySQLMetrics.Items, "mysql_dynamic")...)
	}
	return hbdomain.HeartbeatPayload{
		AgentID:             req.Identity.AgentID,
		MachineID:           req.Identity.MachineID,
		ClusterID:           req.Identity.ClusterID,
		Hostname:            req.Identity.Hostname,
		Version:             req.Identity.Version,
		BootID:              req.Identity.BootID,
		StreamID:            req.Runtime.StreamID,
		Seq:                 req.Runtime.Seq,
		SentAt:              time.UnixMilli(req.Runtime.SentAtUnixMS).UTC(),
		UptimeSec:           req.Runtime.UptimeSec,
		HeartbeatIntervalMs: req.Runtime.HeartbeatIntervalMS,
		OverallHealth:       hbdomain.HealthLevel(req.Health.Overall),
		Summary:             req.Health.Summary,
		Checks:              checks,
		Metrics:             metrics,
	}
}

func tagMetricScope(items []dynamicdomain.MetricResult, scope string) []dynamicdomain.MetricResult {
	out := make([]dynamicdomain.MetricResult, 0, len(items))
	for _, item := range items {
		labels := make(map[string]string, len(item.Labels)+1)
		for k, v := range item.Labels {
			labels[k] = v
		}
		labels["metric_scope"] = scope
		item.Labels = labels
		out = append(out, item)
	}
	return out
}

func buildEvent(prev, next hbdomain.LatestStatus, reason string, payload hbdomain.HeartbeatPayload, now time.Time) hbdomain.StateEvent {
	data, _ := json.Marshal(payload)
	return hbdomain.StateEvent{
		ID:           fmt.Sprintf("%s-%d", payload.AgentID, now.UnixNano()),
		AgentID:      payload.AgentID,
		MachineID:    payload.MachineID,
		EventType:    "state_change",
		PrevState:    prev.CurrentState,
		NewState:     next.CurrentState,
		Reason:       reason,
		HeartbeatSeq: payload.Seq,
		PayloadJSON:  string(data),
		CreatedAt:    now,
	}
}
