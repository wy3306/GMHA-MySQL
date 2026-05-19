package dynamic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

// CommandCollector 是命令型动态采集器，通过执行 Shell 命令并解析输出来采集指标。
type CommandCollector struct {
	name string
}

// NewCommandCollector 创建一个新的命令型动态采集器实例。
func NewCommandCollector(name string) *CommandCollector {
	return &CommandCollector{name: name}
}

// Name 返回采集器名称。
func (c *CommandCollector) Name() string {
	return c.name
}

// Collect 执行命令采集，解析命令输出并返回指标结果。
func (c *CommandCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	started := time.Now()
	if strings.TrimSpace(spec.Command) == "" {
		return metricError(spec, errors.New("command is required"), time.Since(started).Milliseconds())
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", spec.Command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	value, valueType, parseErr := ParseCommandOutput(spec.Parser, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), exitCode, spec.Params)
	success := err == nil
	if spec.Parser == "bool_by_exit_code" {
		success = true
	}
	if parseErr != nil {
		return metricError(spec, parseErr, time.Since(started).Milliseconds())
	}
	detailErr := ""
	if err != nil && spec.Parser != "bool_by_exit_code" {
		detailErr = fmt.Sprintf("exit_code=%d stderr=%s", exitCode, strings.TrimSpace(stderr.String()))
	}
	return dyndomain.MetricResult{
		Name:        spec.Name,
		Category:    categoryFor(spec),
		Success:     success,
		ValueType:   valueType,
		Value:       map[string]any{"value": value, "exit_code": exitCode, "stdout": strings.TrimSpace(stdout.String()), "stderr": strings.TrimSpace(stderr.String())},
		Labels:      spec.Labels,
		CollectedAt: time.Now().UTC(),
		DurationMS:  time.Since(started).Milliseconds(),
		Error:       detailErr,
	}
}

// ParseCommandOutput 根据指定的解析器类型解析命令输出，支持 raw_string、bool_by_exit_code、int、float、regex_extract、json、key_value 等格式。
func ParseCommandOutput(parser, stdout, stderr string, exitCode int, params map[string]string) (any, string, error) {
	if parser == "" {
		parser = "raw_string"
	}
	switch parser {
	case "raw_string":
		return stdout, dyndomain.ValueTypeString, nil
	case "bool_by_exit_code":
		return exitCode == 0, dyndomain.ValueTypeBool, nil
	case "int":
		v, err := strconv.Atoi(strings.TrimSpace(stdout))
		return v, dyndomain.ValueTypeInt, err
	case "float":
		v, err := strconv.ParseFloat(strings.TrimSpace(stdout), 64)
		return v, dyndomain.ValueTypeFloat, err
	case "regex_extract":
		pattern := params["regex"]
		if pattern == "" {
			return nil, dyndomain.ValueTypeString, errors.New("regex parser requires params.regex")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, dyndomain.ValueTypeString, err
		}
		matches := re.FindStringSubmatch(stdout)
		if len(matches) == 0 {
			return "", dyndomain.ValueTypeString, nil
		}
		if len(matches) > 1 {
			return matches[1], dyndomain.ValueTypeString, nil
		}
		return matches[0], dyndomain.ValueTypeString, nil
	case "json":
		var out any
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			return nil, dyndomain.ValueTypeMap, err
		}
		return out, valueTypeFor(out), nil
	case "key_value":
		out := map[string]string{}
		for _, line := range strings.Split(stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				key, value, ok = strings.Cut(line, ":")
			}
			if ok {
				out[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		return out, dyndomain.ValueTypeMap, nil
	case "df_usage_percent", "inode_usage_percent":
		return parseUsagePercent(stdout), dyndomain.ValueTypeFloat, nil
	default:
		return nil, dyndomain.ValueTypeString, fmt.Errorf("unsupported command parser: %s stderr=%s", parser, stderr)
	}
}

func parseUsagePercent(stdout string) float64 {
	re := regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%`)
	matches := re.FindStringSubmatch(stdout)
	if len(matches) > 1 {
		v, _ := strconv.ParseFloat(matches[1], 64)
		return v
	}
	fields := strings.Fields(stdout)
	for _, field := range fields {
		field = strings.TrimSuffix(field, "%")
		if v, err := strconv.ParseFloat(field, 64); err == nil {
			return v
		}
	}
	return 0
}

func valueTypeFor(v any) string {
	switch v.(type) {
	case bool:
		return dyndomain.ValueTypeBool
	case int, int64, float64:
		return dyndomain.ValueTypeFloat
	case []any:
		return dyndomain.ValueTypeArray
	case map[string]any:
		return dyndomain.ValueTypeMap
	default:
		return dyndomain.ValueTypeRaw
	}
}
