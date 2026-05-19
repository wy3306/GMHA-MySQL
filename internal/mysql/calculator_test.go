// calculator_test.go 包含配置计算器的单元测试。
package mysql

import (
	"testing"

	collectdomain "gmha/internal/collect"
)

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
