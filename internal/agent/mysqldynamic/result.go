package mysqldynamic

import (
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

func metricOK(spec dyndomain.CollectTaskSpec, category, valueType string, value any, started time.Time) dyndomain.MetricResult {
	if category == "" {
		category = spec.Category
	}
	if category == "" {
		category = "mysql"
	}
	return dyndomain.MetricResult{
		Name:        spec.Name,
		Category:    category,
		Success:     true,
		ValueType:   valueType,
		Value:       value,
		Labels:      spec.Labels,
		CollectedAt: time.Now().UTC(),
		DurationMS:  time.Since(started).Milliseconds(),
	}
}

func metricError(spec dyndomain.CollectTaskSpec, err error, durationMS int64) dyndomain.MetricResult {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	category := spec.Category
	if category == "" {
		category = "mysql"
	}
	return dyndomain.MetricResult{
		Name:        spec.Name,
		Category:    category,
		Success:     false,
		ValueType:   dyndomain.ValueTypeString,
		Labels:      spec.Labels,
		CollectedAt: time.Now().UTC(),
		DurationMS:  durationMS,
		Error:       msg,
	}
}

func valueTypeFor(v any) string {
	switch v.(type) {
	case bool:
		return dyndomain.ValueTypeBool
	case int, int64, uint64:
		return dyndomain.ValueTypeInt
	case float32, float64:
		return dyndomain.ValueTypeFloat
	case []any, []map[string]any:
		return dyndomain.ValueTypeArray
	case map[string]any, map[string]string:
		return dyndomain.ValueTypeMap
	case string:
		return dyndomain.ValueTypeString
	default:
		return dyndomain.ValueTypeRaw
	}
}
