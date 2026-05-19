package mysqldynamic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentdynamic "gmha/internal/agent/dynamic"
	dyndomain "gmha/internal/domain/dynamic"
)

// CommandCollector 是 MySQL 自定义命令采集器，通过执行用户定义的 Shell 命令来采集指标，
// 支持多种输出解析器（JSON、行解析、退出码判断等）。
type CommandCollector struct {
	name string
}

// NewMySQLCommandCollector 创建一个指定名称的自定义命令采集器实例。
func NewMySQLCommandCollector(name string) *CommandCollector {
	return &CommandCollector{name: name}
}

func (c *CommandCollector) Name() string {
	return c.name
}

func (c *CommandCollector) Category() string {
	return "custom"
}

// Collect 执行自定义命令采集，运行 Shell 命令后根据配置的解析器提取指标值。
func (c *CommandCollector) Collect(ctx context.Context, env *CollectEnv, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	started := time.Now()
	if strings.TrimSpace(spec.Command) == "" {
		return metricError(spec, errors.New("command is required"), time.Since(started).Milliseconds())
	}
	if env == nil {
		return metricError(spec, errors.New("mysql collect env is nil"), time.Since(started).Milliseconds())
	}
	executor := env.Executor
	if executor == nil {
		executor = ShellCommandExecutor{}
	}
	result := executor.Run(ctx, spec.Command)
	value, valueType, parseErr := agentdynamic.ParseCommandOutput(spec.Parser, result.Stdout, result.Stderr, result.ExitCode, spec.Params)
	if parseErr != nil {
		return metricError(spec, parseErr, time.Since(started).Milliseconds())
	}
	success := result.Err == nil
	if spec.Parser == "bool_by_exit_code" {
		success = true
	}
	detailErr := ""
	if result.Err != nil && spec.Parser != "bool_by_exit_code" {
		detailErr = fmt.Sprintf("exit_code=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	category := spec.Category
	if category == "" {
		category = c.Category()
	}
	return dyndomain.MetricResult{
		Name:        spec.Name,
		Category:    category,
		Success:     success,
		ValueType:   valueType,
		Value:       map[string]any{"value": value, "exit_code": result.ExitCode, "stdout": result.Stdout, "stderr": result.Stderr},
		Labels:      spec.Labels,
		CollectedAt: time.Now().UTC(),
		DurationMS:  time.Since(started).Milliseconds(),
		Error:       detailErr,
	}
}
