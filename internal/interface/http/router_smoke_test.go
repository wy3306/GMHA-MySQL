package http

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gmha/internal/app"
)

type smokeMutation struct {
	method string
	path   string
}

func newRouterSmokeApp(t *testing.T) *app.App {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	packageDir := filepath.Join(root, "software")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(`{"storage_path":%q}`, packageDir)
	if err := os.WriteFile(filepath.Join(stateDir, "package-store.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	core, err := app.New(app.Config{
		DBPath:           filepath.Join(root, "manager.db"),
		DatabaseDriver:   "sqlite",
		StateDir:         stateDir,
		AgentBinaryPath:  filepath.Join(root, "agentd"),
		ManagerHTTPAddr:  "http://127.0.0.1:18080",
		ManagerGRPCAddr:  "127.0.0.1:19100",
		ManagerPublicKey: filepath.Join(root, "id_ed25519.pub"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return core
}

func TestRouterReadOnlyModuleSmoke(t *testing.T) {
	core := newRouterSmokeApp(t)
	router := NewRouter(core)
	strictOK := []string{
		"/api/v1/healthz",
		"/api/v1/machines?page=1&page_size=20&cluster=all",
		"/api/v1/clusters?page=1&page_size=20",
		"/api/v1/agents?page=1&page_size=50&status=all&version=all",
		"/api/v1/agents/recovery-tasks",
		"/api/v1/mysql/instances",
		"/api/v1/mysql/account-presets",
		"/api/v1/sql-diagnostics/config",
		"/api/v1/performance/catalog",
		"/api/v1/performance/flamegraphs?limit=10",
		"/api/v1/performance/flamegraphs/schedules",
		"/api/v1/tasks?page=1&page_size=20&status=all&type=all",
		"/api/v1/tasks?stats=true",
		"/api/v1/backup/targets",
		"/api/v1/backup/policies",
		"/api/v1/backup/runs?limit=10",
		"/api/v1/packages",
		"/api/v1/package-settings",
		"/api/v1/ai",
		"/api/v1/ai/capabilities",
		"/api/v1/ai/sessions?include_archived=true",
		"/api/v1/alerts/rules",
		"/api/v1/alerts/filters",
		"/api/v1/alerts/events?limit=10&offset=0",
		"/api/v1/alerts/channels",
		"/api/v1/alerts/deliveries?limit=10",
		"/api/v1/alerts/metrics",
		"/api/v1/alerts/summary",
		"/api/v1/alerts/export/prometheus",
		"/api/v1/alerts/export/zabbix",
		"/api/v1/manager/status",
		"/api/v1/manager/config",
		"/api/v1/manager/database/wal",
		"/api/v1/manager/ha",
		"/api/v1/upgrades/overview",
		"/api/v1/upgrades/jobs",
		"/api/v1/dynamic-collect/config",
		"/api/v1/mysql-dynamic-collect/config",
	}
	for _, path := range strictOK {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s returned %d: %s", path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRouterMissingResourceReadsNeverReturnServerErrors(t *testing.T) {
	core := newRouterSmokeApp(t)
	router := NewRouter(core)
	paths := []string{
		"/api/v1/machines/missing",
		"/api/v1/machines/missing/delete-precheck",
		"/api/v1/machines/missing/static-info",
		"/api/v1/machines/missing/dynamic-metrics",
		"/api/v1/clusters/missing",
		"/api/v1/clusters/missing/machines",
		"/api/v1/clusters/missing/topology",
		"/api/v1/clusters/missing/vip/config",
		"/api/v1/clusters/missing/vip/status",
		"/api/v1/clusters/missing/mgr",
		"/api/v1/performance/flamegraphs/missing",
		"/api/v1/backup/policies/missing",
		"/api/v1/backup/runs/missing",
		"/api/v1/upgrades/missing",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code >= http.StatusInternalServerError {
				t.Fatalf("GET %s returned %d: %s", path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRouterInvalidMutationsFailWithoutServerErrors(t *testing.T) {
	core := newRouterSmokeApp(t)
	router := NewRouter(core)
	tests := []smokeMutation{
		{http.MethodPost, "/api/v1/machines"},
		{http.MethodPost, "/api/v1/machines/precheck"},
		{http.MethodPost, "/api/v1/machines/cleanup"},
		{http.MethodPost, "/api/v1/machines/batch-delete"},
		{http.MethodPut, "/api/v1/machines/missing"},
		{http.MethodDelete, "/api/v1/machines/missing"},
		{http.MethodPost, "/api/v1/ssh-credentials"},
		{http.MethodPost, "/api/v1/clusters"},
		{http.MethodPut, "/api/v1/clusters/missing"},
		{http.MethodDelete, "/api/v1/clusters/missing"},
		{http.MethodPost, "/api/v1/clusters/missing/members"},
		{http.MethodPost, "/api/v1/clusters/missing/cleanup"},
		{http.MethodPost, "/api/v1/agents/register"},
		{http.MethodPost, "/api/v1/agents/heartbeat"},
		{http.MethodPost, "/api/v1/agents/upgrade"},
		{http.MethodPost, "/api/v1/agents/recover"},
		{http.MethodDelete, "/api/v1/mysql/instances"},
		{http.MethodPost, "/api/v1/mysql/histograms"},
		{http.MethodPost, "/api/v1/mysql/binlog-analysis"},
		{http.MethodPut, "/api/v1/mysql/account-presets"},
		{http.MethodPut, "/api/v1/sql-diagnostics/config"},
		{http.MethodPost, "/api/v1/sql-diagnostics/explain"},
		{http.MethodPost, "/api/v1/sql-diagnostics/kill"},
		{http.MethodPost, "/api/v1/performance/flamegraphs"},
		{http.MethodPost, "/api/v1/performance/flamegraphs/schedules"},
		{http.MethodPost, "/api/v1/tasks/control"},
		{http.MethodPost, "/api/v1/tasks/exec"},
		{http.MethodPost, "/api/v1/tasks/cluster-automation"},
		{http.MethodPost, "/api/v1/tasks/mysql-install"},
		{http.MethodPost, "/api/v1/tasks/mysql-lifecycle"},
		{http.MethodPost, "/api/v1/tasks/mysql-online-ddl"},
		{http.MethodPost, "/api/v1/tasks/mysql-archive"},
		{http.MethodPost, "/api/v1/tasks/mysql-cluster-upgrade/plan"},
		{http.MethodPost, "/api/v1/clusters/missing/bootstrap"},
		{http.MethodPost, "/api/v1/clusters/missing/vip/config"},
		{http.MethodPost, "/api/v1/clusters/missing/failover/plan"},
		{http.MethodPost, "/api/v1/clusters/missing/architecture/plan"},
		{http.MethodPost, "/api/v1/clusters/missing/mgr/actions"},
		{http.MethodPost, "/api/v1/backup/policies"},
		{http.MethodPost, "/api/v1/backup/cluster-runs"},
		{http.MethodPost, "/api/v1/backup/runs/missing/restore"},
		{http.MethodPost, "/api/v1/packages/fetch"},
		{http.MethodPost, "/api/v1/packages/fetch-bundle"},
		{http.MethodPost, "/api/v1/packages/verify"},
		{http.MethodDelete, "/api/v1/packages/mysql/missing.tar.xz"},
		{http.MethodPut, "/api/v1/package-settings"},
		{http.MethodPost, "/api/v1/ai/providers"},
		{http.MethodPost, "/api/v1/ai/providers/test"},
		{http.MethodPost, "/api/v1/ai/chat"},
		{http.MethodPost, "/api/v1/ai/plans/execute"},
		{http.MethodPost, "/api/v1/alerts/rules"},
		{http.MethodPost, "/api/v1/alerts/filters"},
		{http.MethodPost, "/api/v1/alerts/events/action"},
		{http.MethodPost, "/api/v1/alerts/channels"},
		{http.MethodPost, "/api/v1/alerts/channels/test"},
		{http.MethodPut, "/api/v1/manager/config"},
		{http.MethodPost, "/api/v1/manager/database/test"},
		{http.MethodPost, "/api/v1/manager/database/wal"},
		{http.MethodPut, "/api/v1/manager/ha/config"},
		{http.MethodPost, "/api/v1/manager/ha/nodes"},
		{http.MethodPost, "/api/v1/manager/ha/nodes/action"},
		{http.MethodPost, "/api/v1/manager/ha/vip/switch"},
		{http.MethodPost, "/api/v1/upgrades/agent"},
		{http.MethodPost, "/api/v1/upgrades/manager"},
		{http.MethodPost, "/api/v1/upgrades/manager/rebuild"},
		{http.MethodPut, "/api/v1/dynamic-collect/config"},
		{http.MethodPut, "/api/v1/mysql-dynamic-collect/config"},
	}
	for _, tt := range tests {
		name := tt.method + " " + tt.path
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(`{}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code >= http.StatusInternalServerError {
				t.Fatalf("%s returned %d: %s", name, recorder.Code, recorder.Body.String())
			}
		})
	}
}
