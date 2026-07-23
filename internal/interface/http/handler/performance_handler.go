package handler

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
)

const performanceRetention = 7 * 24 * time.Hour

type performanceMetricReader interface {
	MetricSamples(context.Context, heartbeatdomain.MetricSampleQuery) ([]heartbeatdomain.MetricSample, error)
}

type PerformanceHandler struct {
	reader performanceMetricReader
	now    func() time.Time
}

type performancePoint struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Samples   int     `json:"samples"`
}

type performanceStatistics struct {
	Current *float64 `json:"current"`
	Min     *float64 `json:"min"`
	Max     *float64 `json:"max"`
	Average *float64 `json:"average"`
	P95     *float64 `json:"p95"`
}

type performanceQueryView struct {
	ClusterID   string `json:"cluster_id"`
	MachineID   string `json:"machine_id,omitempty"`
	Instance    string `json:"instance,omitempty"`
	EventName   string `json:"event_name,omitempty"`
	StartAt     string `json:"start_at"`
	EndAt       string `json:"end_at"`
	StepSeconds int    `json:"step_seconds"`
}

type performanceFreshness struct {
	LastCollectedAt *string `json:"last_collected_at"`
	AgeSeconds      *int64  `json:"age_seconds"`
	Stale           bool    `json:"stale"`
	Successful      int     `json:"successful_samples"`
	Failed          int     `json:"failed_samples"`
}

type performanceSeriesResponse struct {
	Metric       dynamicdomain.PerformanceMetricDefinition `json:"metric"`
	Query        performanceQueryView                      `json:"query"`
	Statistics   performanceStatistics                     `json:"statistics"`
	Freshness    performanceFreshness                      `json:"freshness"`
	Series       []performancePoint                        `json:"series"`
	LatestValues []heartbeatdomain.MetricSample            `json:"latest_values,omitempty"`
	DataPoints   int                                       `json:"data_points"`
	GeneratedAt  string                                    `json:"generated_at"`
}

func NewPerformanceHandler(reader performanceMetricReader) *PerformanceHandler {
	return &PerformanceHandler{reader: reader, now: func() time.Time { return time.Now().UTC() }}
}

func (h *PerformanceHandler) HandleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	availableOnly := r.URL.Query().Get("available") == "true"
	items := make([]dynamicdomain.PerformanceMetricDefinition, 0)
	for _, item := range dynamicdomain.BuildPerformanceMetricCatalog() {
		if scope != "" && item.Scope != scope {
			continue
		}
		if category != "" && item.Category != category {
			continue
		}
		if availableOnly && !item.Available {
			continue
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": len(items),
		"retention_seconds": int(performanceRetention.Seconds()),
		"generated_at":      h.now().Format(time.RFC3339),
	})
}

func (h *PerformanceHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	query := r.URL.Query()
	clusterID := strings.TrimSpace(query.Get("cluster"))
	metricName := strings.TrimSpace(query.Get("metric"))
	if clusterID == "" || metricName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster and metric are required"})
		return
	}
	definition, found := dynamicdomain.FindPerformanceMetric(metricName)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown performance metric"})
		return
	}
	if !definition.Available {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "metric is not available", "metric": definition,
		})
		return
	}
	now := h.now()
	startAt, endAt, err := parsePerformanceWindow(query.Get("start_at"), query.Get("end_at"), query.Get("range_minutes"), now)
	if err != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err})
		return
	}
	step := performanceStepSeconds(startAt, endAt, query.Get("step_seconds"))
	samples, readErr := h.reader.MetricSamples(r.Context(), heartbeatdomain.MetricSampleQuery{
		ClusterID: clusterID, MachineID: strings.TrimSpace(query.Get("machine_id")),
		Metric: metricName, Instance: strings.TrimSpace(query.Get("instance")),
		StartAt: startAt, EndAt: endAt, Limit: 200000,
	})
	if readErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": readErr.Error()})
		return
	}
	eventName := strings.TrimSpace(query.Get("event_name"))
	if eventName != "" {
		samples = filterPerformanceSamplesByLabel(samples, "event_name", eventName)
	}
	points, stats, freshness := buildPerformanceSeries(samples, definition, startAt, endAt, time.Duration(step)*time.Second, now)
	writeJSON(w, http.StatusOK, performanceSeriesResponse{
		Metric: definition,
		Query: performanceQueryView{
			ClusterID: clusterID, MachineID: strings.TrimSpace(query.Get("machine_id")),
			Instance: strings.TrimSpace(query.Get("instance")), EventName: eventName, StartAt: startAt.Format(time.RFC3339),
			EndAt: endAt.Format(time.RFC3339), StepSeconds: step,
		},
		Statistics: stats, Freshness: freshness, Series: points, LatestValues: latestPerformanceValues(samples),
		DataPoints: len(points), GeneratedAt: now.Format(time.RFC3339),
	})
}

func filterPerformanceSamplesByLabel(samples []heartbeatdomain.MetricSample, label, value string) []heartbeatdomain.MetricSample {
	filtered := make([]heartbeatdomain.MetricSample, 0, len(samples))
	for _, item := range samples {
		if item.Labels[label] == value {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func latestPerformanceValues(samples []heartbeatdomain.MetricSample) []heartbeatdomain.MetricSample {
	latest := make(map[string]heartbeatdomain.MetricSample)
	for _, item := range samples {
		key := performanceSampleSource(item)
		if current, exists := latest[key]; !exists || item.CollectedAt.After(current.CollectedAt) {
			latest[key] = item
		}
	}
	out := make([]heartbeatdomain.MetricSample, 0, len(latest))
	for _, item := range latest {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MachineID != out[j].MachineID {
			return out[i].MachineID < out[j].MachineID
		}
		return out[i].Instance < out[j].Instance
	})
	if len(out) > 1000 {
		out = out[:1000]
	}
	return out
}

func parsePerformanceWindow(startText, endText, rangeText string, now time.Time) (time.Time, time.Time, string) {
	end := now
	if strings.TrimSpace(endText) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(endText))
		if err != nil {
			return time.Time{}, time.Time{}, "end_at must be RFC3339"
		}
		end = parsed.UTC()
	}
	start := time.Time{}
	if strings.TrimSpace(startText) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(startText))
		if err != nil {
			return time.Time{}, time.Time{}, "start_at must be RFC3339"
		}
		start = parsed.UTC()
	} else {
		minutes := 60
		if strings.TrimSpace(rangeText) != "" {
			parsed, err := strconv.Atoi(strings.TrimSpace(rangeText))
			if err != nil || parsed <= 0 {
				return time.Time{}, time.Time{}, "range_minutes must be a positive integer"
			}
			minutes = parsed
		}
		start = end.Add(-time.Duration(minutes) * time.Minute)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, "start_at must be before end_at"
	}
	if end.Sub(start) > performanceRetention {
		return time.Time{}, time.Time{}, "requested range exceeds the 7-day retention window"
	}
	if end.After(now.Add(time.Minute)) {
		return time.Time{}, time.Time{}, "end_at cannot be in the future"
	}
	return start, end, ""
}

func performanceStepSeconds(start, end time.Time, requested string) int {
	if value, err := strconv.Atoi(strings.TrimSpace(requested)); err == nil && value > 0 {
		if value < 5 {
			return 5
		}
		if value > 3600 {
			return 3600
		}
		return value
	}
	seconds := int(math.Ceil(end.Sub(start).Seconds() / 120))
	if seconds < 5 {
		return 5
	}
	// Round automatic steps to friendly boundaries while keeping at most
	// roughly 120 points.
	for _, candidate := range []int{10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600} {
		if seconds <= candidate {
			return candidate
		}
	}
	return 3600
}

type performanceBucket struct {
	at       time.Time
	bySource map[string][]float64
	samples  int
}

type performanceRateSample struct {
	at     time.Time
	value  float64
	source string
}

func buildPerformanceSeries(samples []heartbeatdomain.MetricSample, definition dynamicdomain.PerformanceMetricDefinition, start, end time.Time, step time.Duration, now time.Time) ([]performancePoint, performanceStatistics, performanceFreshness) {
	freshness := performanceFreshness{}
	var latest time.Time
	numeric := make([]heartbeatdomain.MetricSample, 0, len(samples))
	for _, item := range samples {
		if item.Success {
			freshness.Successful++
		} else {
			freshness.Failed++
		}
		if item.CollectedAt.After(latest) {
			latest = item.CollectedAt
		}
		if item.Success && item.NumericValue != nil {
			numeric = append(numeric, item)
		}
	}
	if !latest.IsZero() {
		text := latest.UTC().Format(time.RFC3339)
		age := int64(math.Max(0, now.Sub(latest).Seconds()))
		freshness.LastCollectedAt, freshness.AgeSeconds = &text, &age
		freshness.Stale = age > int64(maxInt(definition.IntervalSeconds*3, 30))
	} else {
		freshness.Stale = true
	}

	values := make([]performanceRateSample, 0, len(numeric))
	if definition.ValueKind == "counter" {
		values = performanceCounterRates(numeric, definition.Unit)
	} else {
		for _, item := range numeric {
			values = append(values, performanceRateSample{at: item.CollectedAt, value: *item.NumericValue, source: performanceSampleSource(item)})
		}
	}
	buckets := make(map[int64]*performanceBucket)
	for _, item := range values {
		if item.at.Before(start) || item.at.After(end) {
			continue
		}
		bucketAt := item.at.Truncate(step)
		key := bucketAt.UnixNano()
		bucket := buckets[key]
		if bucket == nil {
			bucket = &performanceBucket{at: bucketAt, bySource: make(map[string][]float64)}
			buckets[key] = bucket
		}
		bucket.bySource[item.source] = append(bucket.bySource[item.source], item.value)
		bucket.samples++
	}
	ordered := make([]*performanceBucket, 0, len(buckets))
	for _, bucket := range buckets {
		ordered = append(ordered, bucket)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].at.Before(ordered[j].at) })
	points := make([]performancePoint, 0, len(ordered))
	allValues := make([]float64, 0, len(ordered))
	for _, bucket := range ordered {
		// A bucket may contain several collection cycles for the same source.
		// Average within each source first; otherwise a sum metric would grow
		// merely because a smaller chart step collected more samples.
		sourceValues := make([]float64, 0, len(bucket.bySource))
		rawValues := make([]float64, 0, bucket.samples)
		for _, samples := range bucket.bySource {
			_, _, sourceAverage, _ := aggregateNumbers(samples)
			sourceValues = append(sourceValues, sourceAverage)
			rawValues = append(rawValues, samples...)
		}
		minimum, maximum, _, _ := aggregateNumbers(rawValues)
		_, _, average, sum := aggregateNumbers(sourceValues)
		value := sum
		switch definition.Aggregation {
		case "avg":
			value = average
		case "max":
			value = maximum
		case "min":
			value = minimum
		}
		value = performanceRound(value)
		points = append(points, performancePoint{
			Timestamp: bucket.at.UTC().Format(time.RFC3339), Value: value,
			Min: performanceRound(minimum), Max: performanceRound(maximum), Samples: bucket.samples,
		})
		allValues = append(allValues, value)
	}
	return points, statisticsForValues(allValues), freshness
}

func performanceCounterRates(samples []heartbeatdomain.MetricSample, unit string) []performanceRateSample {
	bySource := make(map[string][]heartbeatdomain.MetricSample)
	for _, item := range samples {
		key := item.AgentID + "\x00" + item.MachineID + "\x00" + item.Instance + "\x00" + stableLabels(item.Labels)
		bySource[key] = append(bySource[key], item)
	}
	out := make([]performanceRateSample, 0, len(samples))
	for _, items := range bySource {
		source := performanceSampleSource(items[0])
		sort.Slice(items, func(i, j int) bool { return items[i].CollectedAt.Before(items[j].CollectedAt) })
		for index := 1; index < len(items); index++ {
			previous, current := items[index-1], items[index]
			seconds := current.CollectedAt.Sub(previous.CollectedAt).Seconds()
			if seconds <= 0 || *current.NumericValue < *previous.NumericValue {
				continue
			}
			value := (*current.NumericValue - *previous.NumericValue) / seconds
			if strings.HasSuffix(unit, "/min") || unit == "次/min" {
				value *= 60
			}
			out = append(out, performanceRateSample{at: current.CollectedAt, value: value, source: source})
		}
	}
	return out
}

func performanceSampleSource(item heartbeatdomain.MetricSample) string {
	return item.AgentID + "\x00" + item.MachineID + "\x00" + item.Instance + "\x00" + stableLabels(item.Labels)
}

func stableLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(labels[key])
		out.WriteByte(0)
	}
	return out.String()
}

func aggregateNumbers(values []float64) (float64, float64, float64, float64) {
	if len(values) == 0 {
		return 0, 0, 0, 0
	}
	minimum, maximum, sum := values[0], values[0], 0.0
	for _, value := range values {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
		sum += value
	}
	return minimum, maximum, sum / float64(len(values)), sum
}

func statisticsForValues(values []float64) performanceStatistics {
	if len(values) == 0 {
		return performanceStatistics{}
	}
	minimum, maximum, average, _ := aggregateNumbers(values)
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	p95Index := int(math.Ceil(float64(len(sorted))*0.95)) - 1
	if p95Index < 0 {
		p95Index = 0
	}
	current := values[len(values)-1]
	minimum, maximum, average, p95 := performanceRound(minimum), performanceRound(maximum), performanceRound(average), performanceRound(sorted[p95Index])
	return performanceStatistics{Current: &current, Min: &minimum, Max: &maximum, Average: &average, P95: &p95}
}

func performanceRound(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
