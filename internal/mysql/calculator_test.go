// calculator_test.go 包含配置计算器的单元测试。
package mysql

import (
	"strings"
	"testing"

	collectdomain "gmha/internal/collect"
)

func TestApplyRuntimeParameters(t *testing.T) {
	vars := ConfigVars{MaxConnections: 100, BufferPoolSize: "1G", BinlogFormat: "ROW", ReadOnly: 1}
	err := ApplyRuntimeParameters(&vars, map[string]string{
		"max_connections":         "250",
		"innodb_buffer_pool_size": "4g",
		"binlog_format":           "mixed",
		"read_only":               "0",
		"table_open_cache":        "", // empty keeps the calculated value
	})
	if err != nil {
		t.Fatalf("apply runtime parameters: %v", err)
	}
	if vars.MaxConnections != 250 || vars.BufferPoolSize != "4G" || vars.BinlogFormat != "MIXED" || vars.ReadOnly != 0 {
		t.Fatalf("unexpected overridden vars: %+v", vars)
	}
}

func TestApplyRuntimeParametersRejectsUnsafeOrUnknownValues(t *testing.T) {
	tests := []map[string]string{
		{"unknown_parameter": "1"},
		{"read_only": "2"},
		{"innodb_buffer_pool_size": "four gigabytes"},
		{"slow_query_log_file": "/data/slow.log\nmax_connections=9999"},
	}
	for _, parameters := range tests {
		if err := ApplyRuntimeParameters(&ConfigVars{}, parameters); err == nil || !strings.Contains(err.Error(), "runtime parameter") {
			t.Fatalf("expected runtime parameter validation error for %#v, got %v", parameters, err)
		}
	}
}

// TestCalculatorProdProfileGeneratesSafeReadableDefaults 测试生产环境配置档案的计算结果，验证缓冲池、Redo 日志、连接数等参数的正确性。
func TestCalculatorProdProfileGeneratesSafeReadableDefaults(t *testing.T) {
	vars, err := NewCalculator().Calculate(collectdomain.MachineInfo{
		MemoryGB: 15,
	}, Profile{
		Name:              "prod",
		BufferPoolRatio:   0.7,
		MaxConnPerGB:      20,
		MaxMaxConnections: 1000,
		RedoLogRatio:      0.4,
		SortBufferSizeMB:  2,
		ReadBufferSizeMB:  1,
		ReadRndBufferMB:   2,
		JoinBufferSizeMB:  2,
		TableOpenCache:    4096,
		ThreadCacheSize:   128,
		SysctlSwappiness:  1,
		OpenFilesLimit:    65535,
	}, ConfigInput{Port: 3306})
	if err != nil {
		t.Fatal(err)
	}

	if vars.BufferPoolSize != "10G" {
		t.Fatalf("BufferPoolSize = %s, want 10G", vars.BufferPoolSize)
	}
	if vars.RedoLogCapacity != "4G" {
		t.Fatalf("RedoLogCapacity = %s, want 4G", vars.RedoLogCapacity)
	}
	if vars.MaxConnections != 300 {
		t.Fatalf("MaxConnections = %d, want 300", vars.MaxConnections)
	}
	if vars.SortBufferSize != "2M" || vars.ReadBufferSize != "1M" || vars.ReadRndBufferSize != "2M" || vars.JoinBufferSize != "2M" {
		t.Fatalf("unexpected per-connection buffers: sort=%s read=%s read_rnd=%s join=%s", vars.SortBufferSize, vars.ReadBufferSize, vars.ReadRndBufferSize, vars.JoinBufferSize)
	}
	if vars.MyCnfPath != "/data/3306/my.cnf" {
		t.Fatalf("MyCnfPath = %s, want /data/3306/my.cnf", vars.MyCnfPath)
	}
}
