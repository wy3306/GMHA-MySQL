package dynamic

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

// DynamicCollectManager 是主机动态指标采集管理器，负责管理采集任务的生命周期、热更新配置和存储采集结果。
type DynamicCollectManager struct {
	agentID string
	reg     *CollectorRegistry
	mu      sync.RWMutex
	cfg     dyndomain.DynamicCollectConfig
	runners map[string]*taskRunner
	last    map[string]dyndomain.MetricResult
}

type taskRunner struct {
	spec   dyndomain.CollectTaskSpec
	cancel context.CancelFunc
}

// NewDynamicCollectManager 创建一个新的主机动态指标采集管理器实例。
func NewDynamicCollectManager(agentID string, reg *CollectorRegistry) *DynamicCollectManager {
	return &DynamicCollectManager{
		agentID: agentID,
		reg:     reg,
		runners: make(map[string]*taskRunner),
		last:    make(map[string]dyndomain.MetricResult),
	}
}

// Start 启动动态指标采集管理器，根据配置启动相应的采集任务。
func (m *DynamicCollectManager) Start(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
	m.UpdateCollectConfig(ctx, cfg)
}

// StopDynamicCollectors 停止所有正在运行的动态采集任务。
func (m *DynamicCollectManager) StopDynamicCollectors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, runner := range m.runners {
		runner.cancel()
		delete(m.runners, name)
	}
}

// ReloadDynamicCollectors 重新加载动态采集配置，热更新采集任务。
func (m *DynamicCollectManager) ReloadDynamicCollectors(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
	m.UpdateCollectConfig(ctx, cfg)
}

// UpdateCollectConfig 更新采集配置，自动处理采集任务的增删改，支持热更新。
func (m *DynamicCollectManager) UpdateCollectConfig(ctx context.Context, cfg dyndomain.DynamicCollectConfig) {
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
			delete(m.last, name)
			continue
		}
		if !sameSpec(runner.spec, spec) {
			runner.cancel()
			delete(m.runners, name)
		}
	}
	for name := range m.last {
		if _, ok := desired[name]; !ok {
			delete(m.last, name)
		}
	}
	for name, spec := range desired {
		if _, ok := m.runners[name]; ok {
			continue
		}
		collector, err := m.collectorFor(spec)
		if err != nil {
			m.last[name] = metricError(spec, err, 0)
			log.Printf("dynamic collector %s not started: %v", name, err)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		m.runners[name] = &taskRunner{spec: spec, cancel: cancel}
		go m.runTask(runCtx, spec, collector)
	}
}

// CollectOnce 执行一次性的指标采集，不启动周期任务。
func (m *DynamicCollectManager) CollectOnce(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	collector, err := m.collectorFor(normalizeSpec(spec))
	if err != nil {
		return metricError(spec, err, 0)
	}
	return collector.Collect(ctx, normalizeSpec(spec))
}

// GetLastMetricResult 获取指定名称采集器的最近一次采集结果。
func (m *DynamicCollectManager) GetLastMetricResult(name string) (dyndomain.MetricResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.last[name]
	return item, ok
}

// LastBatch 获取所有采集器的最近一次采集结果批次，用于心跳上报。
func (m *DynamicCollectManager) LastBatch() *dyndomain.MetricBatchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]dyndomain.MetricResult, 0, len(m.last))
	for _, item := range m.last {
		items = append(items, item)
	}
	cfgVersion := m.cfg.Version
	return &dyndomain.MetricBatchResult{AgentID: m.agentID, Version: cfgVersion, GeneratedAt: time.Now().UTC(), Items: items}
}

func (m *DynamicCollectManager) runTask(ctx context.Context, spec dyndomain.CollectTaskSpec, collector DynamicCollector) {
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

func (m *DynamicCollectManager) collectAndStore(ctx context.Context, spec dyndomain.CollectTaskSpec, collector DynamicCollector) {
	runCtx := ctx
	var cancel context.CancelFunc
	if spec.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	}
	started := time.Now()
	result := collector.Collect(runCtx, spec)
	if cancel != nil {
		cancel()
	}
	if result.DurationMS == 0 {
		result.DurationMS = time.Since(started).Milliseconds()
	}
	m.mu.Lock()
	m.last[spec.Name] = result
	m.mu.Unlock()
	if !result.Success {
		log.Printf("dynamic collector %s failed: %s", spec.Name, result.Error)
	}
}

func (m *DynamicCollectManager) collectorFor(spec dyndomain.CollectTaskSpec) (DynamicCollector, error) {
	if spec.Type == dyndomain.TaskTypeCommand {
		return NewCommandCollector(spec.Name), nil
	}
	return m.reg.Create(spec.Name)
}

func normalizeSpec(spec dyndomain.CollectTaskSpec) dyndomain.CollectTaskSpec {
	if spec.Type == "" {
		spec.Type = dyndomain.TaskTypeBuiltin
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
