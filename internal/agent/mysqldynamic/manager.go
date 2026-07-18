package mysqldynamic

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

// EnvBuilder 是单实例采集环境构建器函数类型，用于延迟创建单个 MySQL 实例的采集环境。
type EnvBuilder func() (*CollectEnv, error)

// EnvListBuilder 是多实例采集环境构建器函数类型，用于延迟创建多个 MySQL 实例的采集环境。
type EnvListBuilder func() ([]*CollectEnv, error)

// MySQLDynamicCollectManager 是 MySQL 动态指标采集管理器，负责管理多个采集任务的生命周期，
// 支持单实例和多实例模式，可热更新采集配置，周期性执行采集并缓存最新结果。
type MySQLDynamicCollectManager struct {
	agentID   string
	reg       *CollectorRegistry
	buildEnv  EnvBuilder
	buildEnvs EnvListBuilder
	mu        sync.RWMutex
	cfg       dyndomain.DynamicCollectConfig
	runners   map[string]*taskRunner
	last      map[string]dyndomain.MetricResult
}

type taskRunner struct {
	spec   dyndomain.CollectTaskSpec
	cancel context.CancelFunc
}

// NewMySQLDynamicCollectManager 创建单实例模式的 MySQL 动态采集管理器。
func NewMySQLDynamicCollectManager(agentID string, reg *CollectorRegistry, buildEnv EnvBuilder) *MySQLDynamicCollectManager {
	return &MySQLDynamicCollectManager{
		agentID:  agentID,
		reg:      reg,
		buildEnv: buildEnv,
		runners:  make(map[string]*taskRunner),
		last:     make(map[string]dyndomain.MetricResult),
	}
}

// NewMultiInstanceMySQLDynamicCollectManager 创建多实例模式的 MySQL 动态采集管理器，支持同时采集多个 MySQL 实例的指标。
func NewMultiInstanceMySQLDynamicCollectManager(agentID string, reg *CollectorRegistry, buildEnvs EnvListBuilder) *MySQLDynamicCollectManager {
	return &MySQLDynamicCollectManager{
		agentID:   agentID,
		reg:       reg,
		buildEnvs: buildEnvs,
		runners:   make(map[string]*taskRunner),
		last:      make(map[string]dyndomain.MetricResult),
	}
}

// Start 启动采集管理器，根据配置启动所有已启用的采集任务。
func (m *MySQLDynamicCollectManager) Start(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
	m.UpdateMySQLDynamicCollectConfig(ctx, cfg)
}

// StopMySQLDynamicCollectors 停止所有正在运行的 MySQL 动态采集任务。
func (m *MySQLDynamicCollectManager) StopMySQLDynamicCollectors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, runner := range m.runners {
		runner.cancel()
		delete(m.runners, name)
	}
}

// ReloadMySQLDynamicCollectors 重新加载采集配置，等同于 UpdateMySQLDynamicCollectConfig。
func (m *MySQLDynamicCollectManager) ReloadMySQLDynamicCollectors(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
	m.UpdateMySQLDynamicCollectConfig(ctx, cfg)
}

// UpdateMySQLDynamicCollectConfig 热更新采集配置，会自动停止已移除或变更的采集任务，启动新增的采集任务。
func (m *MySQLDynamicCollectManager) UpdateMySQLDynamicCollectConfig(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}
	desired := make(map[string]dyndomain.CollectTaskSpec)
	if cfg.Enabled {
		for _, spec := range cfg.Tasks {
			spec = normalizeSpec(spec)
			if spec.Enabled {
				desired[spec.Name] = spec
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	for name, runner := range m.runners {
		spec, ok := desired[name]
		if !ok {
			runner.cancel()
			delete(m.runners, name)
			m.deleteLastMetricResults(name)
			continue
		}
		if !sameSpec(runner.spec, spec) {
			runner.cancel()
			delete(m.runners, name)
		}
	}
	for key := range m.last {
		name := strings.SplitN(key, "@", 2)[0]
		if _, ok := desired[name]; !ok {
			delete(m.last, key)
		}
	}
	for name, spec := range desired {
		if _, ok := m.runners[name]; ok {
			continue
		}
		collector, err := m.collectorFor(spec)
		if err != nil {
			m.last[name] = metricError(spec, err, 0)
			log.Printf("mysql dynamic collector %s not started: %v", name, err)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		m.runners[name] = &taskRunner{spec: spec, cancel: cancel}
		go m.runTask(runCtx, spec, collector)
	}
}

// CollectOnce 执行一次性的指标采集，不注册为周期任务，适用于按需查询场景。
func (m *MySQLDynamicCollectManager) CollectOnce(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	spec = normalizeSpec(spec)
	collector, err := m.collectorFor(spec)
	if err != nil {
		return metricError(spec, err, 0)
	}
	env, err := m.env()
	if err != nil {
		return metricError(spec, err, 0)
	}
	return collector.Collect(ctx, env, spec)
}

// GetLastMetricResult 获取指定指标的最新采集结果，支持按指标名称或 "name@instance" 格式查询。
func (m *MySQLDynamicCollectManager) GetLastMetricResult(name string) (dyndomain.MetricResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.last[name]
	if ok {
		return item, true
	}
	for key, item := range m.last {
		if strings.HasPrefix(key, name+"@") {
			return item, true
		}
	}
	return item, ok
}

// LastBatch 返回所有指标的最新采集结果批次，用于上报给 Manager。
func (m *MySQLDynamicCollectManager) LastBatch() *dyndomain.MetricBatchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]dyndomain.MetricResult, 0, len(m.last))
	for _, item := range m.last {
		items = append(items, item)
	}
	return &dyndomain.MetricBatchResult{
		AgentID:     m.agentID,
		Version:     m.cfg.Version,
		GeneratedAt: time.Now().UTC(),
		Items:       items,
	}
}

func (m *MySQLDynamicCollectManager) runTask(ctx context.Context, spec dyndomain.CollectTaskSpec, collector MySQLDynamicCollector) {
	m.collectAndStore(ctx, spec, collector)
	ticker := time.NewTicker(time.Duration(spec.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.collectAndStore(ctx, spec, collector)
		}
	}
}

func (m *MySQLDynamicCollectManager) collectAndStore(ctx context.Context, spec dyndomain.CollectTaskSpec, collector MySQLDynamicCollector) {
	runCtx := ctx
	var cancel context.CancelFunc
	if spec.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	}
	started := time.Now()
	envs, err := m.envs()
	results := make([]dyndomain.MetricResult, 0, len(envs))
	if err != nil {
		results = append(results, metricError(spec, err, time.Since(started).Milliseconds()))
	} else {
		for _, env := range envs {
			result := collector.Collect(runCtx, env, spec)
			result = tagMetricInstance(result, env)
			if result.DurationMS == 0 {
				result.DurationMS = time.Since(started).Milliseconds()
			}
			results = append(results, result)
		}
	}
	if cancel != nil {
		cancel()
	}
	m.mu.Lock()
	if err == nil && len(envs) == 0 {
		m.deleteLastMetricResults(spec.Name)
	}
	for _, result := range results {
		m.last[metricResultKey(result)] = result
	}
	m.mu.Unlock()
	for _, result := range results {
		if !result.Success {
			log.Printf("mysql dynamic collector %s failed: %s", metricResultKey(result), result.Error)
		}
	}
}

// deleteLastMetricResults removes both a single-instance key and all
// name@instance keys. The caller must hold m.mu.
func (m *MySQLDynamicCollectManager) deleteLastMetricResults(name string) {
	delete(m.last, name)
	for key := range m.last {
		if strings.HasPrefix(key, name+"@") {
			delete(m.last, key)
		}
	}
}

func (m *MySQLDynamicCollectManager) collectorFor(spec dyndomain.CollectTaskSpec) (MySQLDynamicCollector, error) {
	if spec.Type == dyndomain.TaskTypeCommand {
		return NewMySQLCommandCollector(spec.Name), nil
	}
	return m.reg.Create(spec.Name)
}

func (m *MySQLDynamicCollectManager) env() (*CollectEnv, error) {
	if m.buildEnv == nil {
		return nil, nil
	}
	return m.buildEnv()
}

func (m *MySQLDynamicCollectManager) envs() ([]*CollectEnv, error) {
	if m.buildEnvs != nil {
		return m.buildEnvs()
	}
	env, err := m.env()
	if err != nil {
		return nil, err
	}
	if env == nil {
		return nil, nil
	}
	return []*CollectEnv{env}, nil
}

func tagMetricInstance(result dyndomain.MetricResult, env *CollectEnv) dyndomain.MetricResult {
	if env == nil {
		return result
	}
	labels := make(map[string]string, len(result.Labels)+4)
	for k, v := range result.Labels {
		labels[k] = v
	}
	port := env.port()
	labels["mysql_port"] = strconv.Itoa(port)
	labels["mysql_host"] = defaultString(env.Connect.Host, "127.0.0.1")
	labels["mysql_instance"] = env.Instance
	labels["mysql_endpoint"] = fmt.Sprintf("%s:%d", defaultString(env.Connect.Host, "127.0.0.1"), port)
	result.Labels = labels
	return result
}

func metricResultKey(result dyndomain.MetricResult) string {
	instance := result.Labels["mysql_instance"]
	if instance == "" {
		instance = result.Labels["mysql_endpoint"]
	}
	if instance == "" {
		return result.Name
	}
	return result.Name + "@" + instance
}

func normalizeSpec(spec dyndomain.CollectTaskSpec) dyndomain.CollectTaskSpec {
	if spec.Type == "" {
		spec.Type = dyndomain.TaskTypeBuiltin
	}
	if spec.Category == "" {
		spec.Category = "mysql"
	}
	if spec.IntervalSeconds <= 0 {
		spec.IntervalSeconds = 1
	}
	if spec.TimeoutSeconds <= 0 {
		spec.TimeoutSeconds = 1
	}
	if spec.Params == nil {
		spec.Params = map[string]string{}
	}
	if spec.Labels == nil {
		spec.Labels = map[string]string{}
	}
	return spec
}

func sameSpec(a, b dyndomain.CollectTaskSpec) bool {
	aj, _ := json.Marshal(normalizeSpec(a))
	bj, _ := json.Marshal(normalizeSpec(b))
	return reflect.DeepEqual(aj, bj)
}
