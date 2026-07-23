package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	hbdomain "gmha/internal/domain/heartbeat"
)

type performanceLeaf struct {
	name      string
	category  string
	value     float64
	labels    map[string]string
	valueType string
}

func normalizePerformanceSamples(status hbdomain.LatestStatus, metrics []dynamicdomain.MetricResult, receivedAt time.Time) []hbdomain.MetricSample {
	out := make([]hbdomain.MetricSample, 0, len(metrics)*2)
	for _, metric := range metrics {
		at := metric.CollectedAt.UTC()
		if at.IsZero() {
			at = receivedAt.UTC()
		}
		scope := "machine"
		if strings.HasPrefix(metric.Name, "mysql_") {
			scope = "mysql"
		}
		instance := mysqlMetricInstance(metric.Labels)
		base := hbdomain.MetricSample{
			AgentID: status.AgentID, MachineID: status.MachineID, ClusterID: status.ClusterID,
			Scope: scope, Category: metric.Category, MetricName: metric.Name,
			Instance: instance, Labels: performanceCloneLabels(metric.Labels), ValueType: metric.ValueType,
			Value: metric.Value, Success: metric.Success, Error: metric.Error, CollectedAt: at,
		}
		if number, ok := performanceNumber(metric.Value); ok {
			base.NumericValue = numberPointer(number)
		}
		// Preserve the exact raw collector payload even when it is structured.
		// This is useful for audit/debugging; the flattened leaves below are
		// what chart queries use.
		out = append(out, base)
		leaves := flattenPerformanceMetric(metric)
		if base.NumericValue == nil && len(leaves) == 0 && strings.HasPrefix(metric.Name, "mysql_") {
			if number, ok := structuredMetricNumber(metric.Value); ok {
				leaves = append(leaves, performanceLeaf{name: metric.Name, category: metric.Category, value: number, valueType: dynamicdomain.ValueTypeFloat})
			}
		}
		for _, leaf := range leaves {
			item := base
			item.MetricName = leaf.name
			item.Category = leaf.category
			item.Labels = mergeLabels(metric.Labels, leaf.labels)
			item.ValueType = leaf.valueType
			item.NumericValue = numberPointer(leaf.value)
			item.Value = leaf.value
			out = append(out, item)
		}
	}
	return out
}

func structuredMetricNumber(value any) (float64, bool) {
	if item, ok := stringAnyMap(value); ok {
		for _, key := range []string{"final_ok", "ok", "used_percent", "lag_seconds", "count", "count(*)", "executions", "operations"} {
			if number, found := performanceNumber(item[key]); found {
				return number, true
			}
		}
		sum, found := 0.0, false
		for _, raw := range item {
			if number, ok := performanceNumber(raw); ok {
				sum += number
				found = true
			}
		}
		return sum, found
	}
	if items, ok := anySlice(value); ok {
		sum, found := 0.0, false
		for _, rawItem := range items {
			item, itemOK := stringAnyMap(rawItem)
			if !itemOK {
				continue
			}
			for _, key := range []string{"count", "count(*)", "executions", "operations"} {
				if number, numberOK := performanceNumber(item[key]); numberOK {
					sum += number
					found = true
					break
				}
			}
		}
		return sum, found
	}
	return 0, false
}

func flattenPerformanceMetric(metric dynamicdomain.MetricResult) []performanceLeaf {
	switch metric.Name {
	case "io_status":
		return flattenNestedMap(metric.Value, "device", "disk_io", map[string]string{
			"busy_ratio": "host_disk_busy_percent", "read_iops": "host_disk_read_iops",
			"write_iops": "host_disk_write_iops", "read_bytes_sec": "host_disk_read_bytes_sec",
			"write_bytes_sec": "host_disk_write_bytes_sec",
		}, func(key string, value float64) float64 {
			if key == "busy_ratio" {
				return value * 100
			}
			return value
		})
	case "network_throughput":
		return flattenNestedMap(metric.Value, "interface", "network", map[string]string{
			"receive_bytes_sec":  "host_network_receive_bytes_sec",
			"transmit_bytes_sec": "host_network_transmit_bytes_sec",
		}, nil)
	case "load_average":
		return flattenFlatMap(metric.Value, "load", map[string]string{
			"load1": "host_load_1m", "load5": "host_load_5m", "load15": "host_load_15m",
		})
	case "ntp_offset_ms":
		return flattenFlatMap(metric.Value, "system", map[string]string{"offset_ms": "ntp_offset_ms"})
	case "filesystem_usage":
		return flattenArray(metric.Value, "mount", "filesystem", map[string]string{
			"used_percent": "host_filesystem_used_percent", "used_bytes": "host_filesystem_used_bytes",
			"available_bytes": "host_filesystem_available_bytes",
		})
	case "swap_usage":
		return flattenArray(metric.Value, "mount", "memory", map[string]string{
			"used_percent": "host_swap_used_percent", "used_bytes": "host_swap_used_bytes",
			"available_bytes": "host_swap_available_bytes",
		})
	case "host_memory_detail":
		return flattenFlatMap(metric.Value, "memory", map[string]string{
			"total_bytes": "host_memory_total_bytes", "used_bytes": "host_memory_used_bytes",
			"available_bytes": "host_memory_available_bytes", "free_bytes": "host_memory_free_bytes",
			"buffers_bytes": "host_memory_buffers_bytes", "cached_bytes": "host_memory_cached_bytes",
			"anon_bytes": "host_memory_anon_bytes", "slab_bytes": "host_memory_slab_bytes",
			"slab_reclaimable_bytes": "host_memory_slab_reclaimable_bytes",
			"page_tables_bytes":      "host_memory_page_tables_bytes",
			"kernel_stack_bytes":     "host_memory_kernel_stack_bytes", "shared_bytes": "host_memory_shared_bytes",
			"active_bytes": "host_memory_active_bytes", "inactive_bytes": "host_memory_inactive_bytes",
			"swap_total_bytes": "host_memory_swap_total_bytes", "swap_free_bytes": "host_memory_swap_free_bytes",
			"swap_used_bytes":          "host_memory_swap_used_bytes",
			"mysql_process_rss_bytes":  "host_mysql_process_rss_bytes",
			"mysql_processes_detected": "host_mysql_processes_detected",
		})
	case "mysql_memory_modules":
		return flattenMySQLMemoryModules(metric.Value)
	case "inode_usage":
		return flattenArray(metric.Value, "mount", "filesystem", map[string]string{
			"used_percent": "host_inode_used_percent",
		})
	case "ssh_probe":
		return flattenFlatMap(metric.Value, "system", map[string]string{
			"ok": "host_ssh_probe_ok", "tcp_ok": "host_ssh_tcp_ok", "service_ok": "host_ssh_service_ok",
		})
	default:
		if strings.HasPrefix(metric.Name, "mysql_") && strings.HasSuffix(metric.Name, "_disk_usage") {
			return flattenFlatMap(metric.Value, metric.Category, map[string]string{"used_percent": metric.Name})
		}
		return nil
	}
}

func flattenMySQLMemoryModules(value any) []performanceLeaf {
	items, ok := anySlice(value)
	if !ok {
		return nil
	}
	out := make([]performanceLeaf, 0, len(items)*5+3)
	totalCurrent, totalHigh := 0.0, 0.0
	for _, rawItem := range items {
		item, ok := stringAnyMap(rawItem)
		if !ok {
			continue
		}
		eventName := strings.TrimSpace(fmt.Sprint(item["event_name"]))
		if eventName == "" || eventName == "<nil>" {
			continue
		}
		labels := map[string]string{
			"event_name": eventName,
			"module":     strings.TrimSpace(fmt.Sprint(item["module"])),
			"group":      strings.TrimSpace(fmt.Sprint(item["group"])),
		}
		fields := map[string]string{
			"current_bytes": "mysql_memory_module_bytes", "high_bytes": "mysql_memory_module_high_bytes",
			"current_count_used": "mysql_memory_module_allocations",
			"alloc_count":        "mysql_memory_module_alloc_count", "free_count": "mysql_memory_module_free_count",
		}
		for source, target := range fields {
			number, found := performanceNumber(item[source])
			if !found {
				continue
			}
			out = append(out, performanceLeaf{name: target, category: "memory", value: number, valueType: dynamicdomain.ValueTypeFloat, labels: labels})
			if source == "current_bytes" {
				totalCurrent += number
			}
			if source == "high_bytes" {
				totalHigh += number
			}
		}
	}
	out = append(out,
		performanceLeaf{name: "mysql_memory_tracked_bytes", category: "memory", value: totalCurrent, valueType: dynamicdomain.ValueTypeFloat},
		performanceLeaf{name: "mysql_memory_high_water_bytes", category: "memory", value: totalHigh, valueType: dynamicdomain.ValueTypeFloat},
		performanceLeaf{name: "mysql_memory_module_count", category: "memory", value: float64(len(items)), valueType: dynamicdomain.ValueTypeFloat},
	)
	return out
}

func flattenNestedMap(value any, labelName, category string, fields map[string]string, transform func(string, float64) float64) []performanceLeaf {
	root, ok := stringAnyMap(value)
	if !ok {
		return nil
	}
	out := make([]performanceLeaf, 0)
	for labelValue, rawItem := range root {
		item, ok := stringAnyMap(rawItem)
		if !ok {
			continue
		}
		for source, target := range fields {
			number, ok := performanceNumber(item[source])
			if !ok {
				continue
			}
			if transform != nil {
				number = transform(source, number)
			}
			out = append(out, performanceLeaf{name: target, category: category, value: number, valueType: dynamicdomain.ValueTypeFloat, labels: map[string]string{labelName: labelValue}})
		}
	}
	return out
}

func flattenFlatMap(value any, category string, fields map[string]string) []performanceLeaf {
	root, ok := stringAnyMap(value)
	if !ok {
		return nil
	}
	out := make([]performanceLeaf, 0, len(fields))
	for source, target := range fields {
		if number, ok := performanceNumber(root[source]); ok {
			out = append(out, performanceLeaf{name: target, category: category, value: number, valueType: dynamicdomain.ValueTypeFloat})
		}
	}
	return out
}

func flattenArray(value any, labelField, category string, fields map[string]string) []performanceLeaf {
	items, ok := anySlice(value)
	if !ok {
		return nil
	}
	out := make([]performanceLeaf, 0)
	for _, rawItem := range items {
		item, ok := stringAnyMap(rawItem)
		if !ok {
			continue
		}
		label := strings.TrimSpace(fmt.Sprint(item[labelField]))
		if label == "" || label == "<nil>" {
			continue
		}
		for source, target := range fields {
			if number, ok := performanceNumber(item[source]); ok {
				out = append(out, performanceLeaf{name: target, category: category, value: number, valueType: dynamicdomain.ValueTypeFloat, labels: map[string]string{labelField: label}})
			}
		}
	}
	return out
}

func stringAnyMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out, true
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, false
		}
		return out, true
	}
}

func anySlice(value any) ([]any, bool) {
	if items, ok := value.([]any); ok {
		return items, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out []any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func performanceNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	case bool:
		if typed {
			number = 1
		}
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func mysqlMetricInstance(labels map[string]string) string {
	for _, key := range []string{"mysql_instance", "instance", "endpoint"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	host := strings.TrimSpace(labels["mysql_host"])
	port := strings.TrimSpace(labels["mysql_port"])
	if host != "" && port != "" {
		return host + ":" + port
	}
	if port != "" {
		return ":" + port
	}
	return ""
}

func performanceCloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

func mergeLabels(base, extra map[string]string) map[string]string {
	out := performanceCloneLabels(base)
	if out == nil && len(extra) > 0 {
		out = make(map[string]string, len(extra))
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func numberPointer(value float64) *float64 {
	return &value
}
