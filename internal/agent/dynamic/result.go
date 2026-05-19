package dynamic

import (
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

// metricOK 构造一个成功的指标采集结果。
func metricOK(spec dyndomain.CollectTaskSpec, category, valueType string, value any, started time.Time) dyndomain.MetricResult {
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

// metricError 构造一个失败的指标采集结果。
func metricError(spec dyndomain.CollectTaskSpec, err error, durationMS int64) dyndomain.MetricResult {
	if durationMS == 0 {
		durationMS = 0
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return dyndomain.MetricResult{
		Name:        spec.Name,
		Category:    categoryFor(spec),
		Success:     false,
		ValueType:   dyndomain.ValueTypeString,
		Value:       nil,
		Labels:      spec.Labels,
		CollectedAt: time.Now().UTC(),
		DurationMS:  durationMS,
		Error:       msg,
	}
}

func categoryFor(spec dyndomain.CollectTaskSpec) string {
	if spec.Category != "" {
		return spec.Category
	}
	if v := spec.Params["category"]; v != "" {
		return v
	}
	switch {
	case len(spec.Name) >= 6 && spec.Name[:6] == "mysql_":
		return "mysql"
	default:
		return "host"
	}
}
