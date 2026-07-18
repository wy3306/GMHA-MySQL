// Package dynamic 提供主机动态指标采集框架，支持内置采集器和自定义命令采集器，可热更新采集配置。
package dynamic

import (
	"context"
	"errors"
	"sync"

	dyndomain "gmha/internal/domain/dynamic"
)

// DynamicCollector 定义动态指标采集器接口，所有主机级采集器需实现此接口。
type DynamicCollector interface {
	Name() string
	Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult
}

// CollectorFactory 是采集器工厂函数类型，用于延迟创建采集器实例。
type CollectorFactory func() DynamicCollector

// CollectorRegistry 是采集器注册表，管理所有已注册的动态采集器工厂。
type CollectorRegistry struct {
	mu        sync.RWMutex
	factories map[string]CollectorFactory
}

// NewCollectorRegistry 创建一个新的采集器注册表实例。
func NewCollectorRegistry() *CollectorRegistry {
	return &CollectorRegistry{factories: make(map[string]CollectorFactory)}
}

// Register 注册一个采集器工厂，按名称进行索引。
func (r *CollectorRegistry) Register(name string, factory CollectorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Create 根据名称创建采集器实例，若未注册则返回错误。
func (r *CollectorRegistry) Create(name string) (DynamicCollector, error) {
	r.mu.RLock()
	factory := r.factories[name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, errors.New("dynamic collector not found: " + name)
	}
	return factory(), nil
}

// RegisterBuiltinCollectors 注册所有内置的主机动态采集器，包括 CPU、内存、IO、负载、NTP、SSH、inode 以及 MySQL 相关采集器。
func RegisterBuiltinCollectors(reg *CollectorRegistry, mysqlConfigPath string) {
	reg.Register("cpu_usage_percent", func() DynamicCollector { return NewCPUCollector() })
	reg.Register("mem_usage_percent", func() DynamicCollector { return builtinFunc("mem_usage_percent", "host", collectMemUsage) })
	reg.Register("agent_cpu_usage_percent", func() DynamicCollector { return NewAgentCPUCollector() })
	reg.Register("agent_memory_rss_mb", func() DynamicCollector { return builtinFunc("agent_memory_rss_mb", "agent", collectAgentRSS) })
	reg.Register("io_status", func() DynamicCollector { return NewIOCollector() })
	reg.Register("filesystem_usage", func() DynamicCollector { return builtinFunc("filesystem_usage", "host", collectFilesystemUsage) })
	reg.Register("network_throughput", func() DynamicCollector { return NewNetworkCollector() })
	reg.Register("load_average", func() DynamicCollector { return builtinFunc("load_average", "host", collectLoadAverage) })
	reg.Register("ntp_offset_ms", func() DynamicCollector { return builtinFunc("ntp_offset_ms", "host", collectNTPOffset) })
	reg.Register("ssh_probe", func() DynamicCollector { return builtinFunc("ssh_probe", "host", collectSSHProbe) })
	reg.Register("inode_usage", func() DynamicCollector { return builtinFunc("inode_usage", "host", collectInodeUsage) })

	reg.Register("mysql_process_alive", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_process_alive") })
	reg.Register("mysql_port_listening", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_port_listening") })
	reg.Register("mysql_socket_ok", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_socket_ok") })
	reg.Register("mysql_connectivity", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_connectivity") })
	reg.Register("mysql_data_disk_usage", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_data_disk_usage") })
	reg.Register("mysql_binlog_disk_usage", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_binlog_disk_usage") })
	reg.Register("mysql_redo_disk_usage", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_redo_disk_usage") })
	reg.Register("mysql_tmp_disk_usage", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_tmp_disk_usage") })
	reg.Register("mysql_undo_disk_usage", func() DynamicCollector { return NewMySQLCollector(mysqlConfigPath, "mysql_undo_disk_usage") })
}
