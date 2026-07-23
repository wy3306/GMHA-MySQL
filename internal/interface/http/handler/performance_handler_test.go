package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
)

type fakePerformanceReader struct {
	items []heartbeatdomain.MetricSample
	query heartbeatdomain.MetricSampleQuery
}

func (f *fakePerformanceReader) MetricSamples(_ context.Context, query heartbeatdomain.MetricSampleQuery) ([]heartbeatdomain.MetricSample, error) {
	f.query = query
	return f.items, nil
}

func TestPerformanceMetricsReturnsRatesStatisticsAndFreshness(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	number := func(value float64) *float64 { return &value }
	reader := &fakePerformanceReader{items: []heartbeatdomain.MetricSample{
		{AgentID: "a1", MachineID: "m1", ClusterID: "demo", MetricName: "mysql_qps", Instance: ":3306", Labels: map[string]string{"mysql_port": "3306"}, NumericValue: number(100), Success: true, CollectedAt: now.Add(-20 * time.Second)},
		{AgentID: "a1", MachineID: "m1", ClusterID: "demo", MetricName: "mysql_qps", Instance: ":3306", Labels: map[string]string{"mysql_port": "3306"}, NumericValue: number(300), Success: true, CollectedAt: now.Add(-10 * time.Second)},
	}}
	handler := NewPerformanceHandler(reader)
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/api/v1/performance/metrics?cluster=demo&metric=mysql_qps&range_minutes=60&step_seconds=5", nil)
	response := httptest.NewRecorder()
	handler.HandleMetrics(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload performanceSeriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Series) != 1 || payload.Statistics.Current == nil || *payload.Statistics.Current != 20 {
		t.Fatalf("unexpected counter rate response: %+v", payload)
	}
	if payload.Freshness.Stale || payload.Freshness.Successful != 2 || reader.query.ClusterID != "demo" {
		t.Fatalf("unexpected freshness/query: %+v %+v", payload.Freshness, reader.query)
	}
}

func TestPerformanceMetricsRejectsOverRetentionWindow(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	handler := NewPerformanceHandler(&fakePerformanceReader{})
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/api/v1/performance/metrics?cluster=demo&metric=cpu_usage_percent&range_minutes=10081", nil)
	response := httptest.NewRecorder()
	handler.HandleMetrics(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestPerformanceMetricsFiltersMemoryModuleEventName(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	number := func(value float64) *float64 { return &value }
	reader := &fakePerformanceReader{items: []heartbeatdomain.MetricSample{
		{AgentID: "a1", MachineID: "m1", ClusterID: "demo", MetricName: "mysql_memory_module_bytes", Instance: ":3306", Labels: map[string]string{"event_name": "memory/innodb/buf_buf_pool"}, NumericValue: number(1024), Success: true, CollectedAt: now.Add(-time.Minute)},
		{AgentID: "a1", MachineID: "m1", ClusterID: "demo", MetricName: "mysql_memory_module_bytes", Instance: ":3306", Labels: map[string]string{"event_name": "memory/sql/TABLE"}, NumericValue: number(2048), Success: true, CollectedAt: now.Add(-time.Minute)},
	}}
	handler := NewPerformanceHandler(reader)
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/api/v1/performance/metrics?cluster=demo&metric=mysql_memory_module_bytes&range_minutes=60&event_name=memory%2Finnodb%2Fbuf_buf_pool", nil)
	response := httptest.NewRecorder()

	handler.HandleMetrics(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload performanceSeriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Query.EventName != "memory/innodb/buf_buf_pool" || len(payload.LatestValues) != 1 {
		t.Fatalf("unexpected filtered payload: %+v", payload)
	}
	if payload.Statistics.Current == nil || *payload.Statistics.Current != 1024 {
		t.Fatalf("current = %v, want 1024", payload.Statistics.Current)
	}
}

func TestPerformanceSeriesDoesNotInflateSumWhenBucketContainsRepeatedCycles(t *testing.T) {
	at := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	number := func(value float64) *float64 { return &value }
	samples := []heartbeatdomain.MetricSample{
		{AgentID: "a1", MachineID: "m1", MetricName: "host_network_receive_bytes_sec", NumericValue: number(100), Success: true, CollectedAt: at},
		{AgentID: "a1", MachineID: "m1", MetricName: "host_network_receive_bytes_sec", NumericValue: number(200), Success: true, CollectedAt: at.Add(5 * time.Second)},
		{AgentID: "a2", MachineID: "m2", MetricName: "host_network_receive_bytes_sec", NumericValue: number(300), Success: true, CollectedAt: at},
		{AgentID: "a2", MachineID: "m2", MetricName: "host_network_receive_bytes_sec", NumericValue: number(500), Success: true, CollectedAt: at.Add(5 * time.Second)},
	}
	definition, found := dynamicdomain.FindPerformanceMetric("host_network_receive_bytes_sec")
	if !found {
		t.Fatal("missing network metric definition")
	}
	points, _, _ := buildPerformanceSeries(samples, definition, at, at.Add(time.Minute), 30*time.Second, at.Add(10*time.Second))
	if len(points) != 1 || points[0].Value != 550 {
		t.Fatalf("series = %+v, want source averages 150 + 400 = 550", points)
	}
}

func TestPerformanceCatalogExposesMachineAndDatabaseMetrics(t *testing.T) {
	handler := NewPerformanceHandler(&fakePerformanceReader{})
	response := httptest.NewRecorder()
	handler.HandleCatalog(response, httptest.NewRequest(http.MethodGet, "/api/v1/performance/catalog", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var payload struct {
		Items []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	foundMachine, foundMySQL := false, false
	for _, item := range payload.Items {
		foundMachine = foundMachine || item.Name == "host_network_receive_bytes_sec" && item.Scope == "machine"
		foundMySQL = foundMySQL || item.Name == "mysql_qps" && item.Scope == "mysql"
	}
	if !foundMachine || !foundMySQL {
		t.Fatalf("catalog missing scopes: machine=%v mysql=%v", foundMachine, foundMySQL)
	}
}
