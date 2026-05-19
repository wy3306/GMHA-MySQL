package mysqldynamic

import (
	"context"
	"testing"

	dyndomain "gmha/internal/domain/dynamic"
)

type fakeExecutor struct {
	result CommandResult
}

func (f fakeExecutor) Run(context.Context, string) CommandResult {
	return f.result
}

func TestMySQLCommandCollectorParsesInt(t *testing.T) {
	collector := NewMySQLCommandCollector("custom_threads_running")
	env := &CollectEnv{Executor: fakeExecutor{result: CommandResult{Stdout: "12", ExitCode: 0}}}
	result := collector.Collect(context.Background(), env, dyndomain.CollectTaskSpec{
		Name:            "custom_threads_running",
		Enabled:         true,
		Type:            dyndomain.TaskTypeCommand,
		Category:        "custom",
		IntervalSeconds: 1,
		TimeoutSeconds:  1,
		Command:         "echo 12",
		Parser:          "int",
	})
	if !result.Success {
		t.Fatalf("expected success, got %s", result.Error)
	}
	if result.ValueType != dyndomain.ValueTypeInt {
		t.Fatalf("expected int value type, got %s", result.ValueType)
	}
	value, ok := result.Value.(map[string]any)["value"].(int)
	if !ok || value != 12 {
		t.Fatalf("expected parsed value 12, got %#v", result.Value)
	}
}
