// Package mysqldynamic 提供 MySQL 动态指标采集框架，支持内置采集器和自定义命令采集器，可热更新采集配置。
package mysqldynamic

import (
	"context"
	"fmt"
	"sync"

	dyndomain "gmha/internal/domain/dynamic"
)

// MySQLDynamicCollector 定义 MySQL 动态指标采集器接口，所有 MySQL 级采集器需实现此接口。
type MySQLDynamicCollector interface {
	Name() string
	Category() string
	Collect(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult
}

// CollectorFactory 是 MySQL 采集器工厂函数类型，用于延迟创建采集器实例。
type CollectorFactory func() MySQLDynamicCollector

// CollectorRegistry 是 MySQL 动态采集器注册表，管理所有已注册的 MySQL 采集器工厂。
type CollectorRegistry struct {
	mu        sync.RWMutex
	factories map[string]CollectorFactory
}

// NewCollectorRegistry 创建一个新的 MySQL 动态采集器注册表实例。
func NewCollectorRegistry() *CollectorRegistry {
	return &CollectorRegistry{factories: make(map[string]CollectorFactory)}
}

// Register 注册一个 MySQL 采集器工厂，按名称进行索引。
func (r *CollectorRegistry) Register(name string, factory CollectorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Create 根据名称创建 MySQL 采集器实例，若未注册则返回错误。
func (r *CollectorRegistry) Create(name string) (MySQLDynamicCollector, error) {
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("mysql dynamic collector not registered: %s", name)
	}
	return factory(), nil
}

func RegisterBuiltinMySQLCollectors(reg *CollectorRegistry) {
	for _, spec := range dyndomain.BuildDefaultMySQLDynamicCollectConfig().Tasks {
		name := spec.Name
		reg.Register(name, func() MySQLDynamicCollector { return NewBuiltinCollector(name) })
	}
	for _, name := range []string{
		"mysql_connectivity",
		"mysql_uptime",
		"mysql_read_only",
		"mysql_threads_running",
		"mysql_replication_basic_status",
	} {
		name := name
		reg.Register(name, func() MySQLDynamicCollector { return NewBuiltinCollector(name) })
	}
}
