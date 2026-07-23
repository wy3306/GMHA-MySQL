package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestManagerRuntimeService(t *testing.T, cfg ManagerRuntimeConfig) *ManagerRuntimeService {
	t.Helper()
	return &ManagerRuntimeService{
		statePath:     filepath.Join(t.TempDir(), "manager-runtime.json"),
		defaultConfig: normalizeManagerRuntimeConfig(cfg),
		healthClient:  &http.Client{Timeout: time.Second},
	}
}

func TestManagerRuntimeDiscoversHealthyServerWithoutStateFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	service := newTestManagerRuntimeService(t, ManagerRuntimeConfig{ManagerHTTPAddr: server.URL})
	status, err := service.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Discovery != "health" || status.PID != 0 {
		t.Fatalf("unexpected discovered status: %+v", status)
	}
}

func TestRegisterCurrentProcessMakesDirectServeDiscoverable(t *testing.T) {
	service := newTestManagerRuntimeService(t, ManagerRuntimeConfig{})
	cfg := normalizeManagerRuntimeConfig(ManagerRuntimeConfig{ManagerHTTPAddr: "http://192.0.2.10:18080"})
	if err := service.RegisterCurrentProcess(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.PID != os.Getpid() || status.Discovery != "state" {
		t.Fatalf("unexpected registered status: %+v", status)
	}
}

func TestAdoptCurrentProcessRepairsStaleRuntimeState(t *testing.T) {
	service := newTestManagerRuntimeService(t, ManagerRuntimeConfig{})
	stale := managerRuntimeState{
		PID:       999999,
		StartedAt: time.Now().Add(-time.Hour),
		Config:    normalizeManagerRuntimeConfig(ManagerRuntimeConfig{ManagerHTTPAddr: "http://192.0.2.20:8080"}),
	}
	if err := service.persistState(stale); err != nil {
		t.Fatal(err)
	}
	status, err := service.AdoptCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.PID != os.Getpid() || status.Discovery != "current" {
		t.Fatalf("unexpected adopted status: %+v", status)
	}
	persisted, ok, err := service.loadState()
	if err != nil || !ok {
		t.Fatalf("load state: ok=%v err=%v", ok, err)
	}
	if persisted.PID != os.Getpid() {
		t.Fatalf("persisted pid = %d, want %d", persisted.PID, os.Getpid())
	}
}

func TestManagerDatabaseConfigValidationAndAliases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ManagerRuntimeConfig
		wantErr bool
		driver  string
	}{
		{name: "sqlite path", cfg: ManagerRuntimeConfig{DatabaseDriver: "sqlite", DBPath: "manager.db"}, driver: "sqlite"},
		{name: "mysql dsn required", cfg: ManagerRuntimeConfig{DatabaseDriver: "mysql"}, wantErr: true, driver: "mysql"},
		{name: "mysql", cfg: ManagerRuntimeConfig{DatabaseDriver: "MYSQL", DatabaseDSN: "user:pass@tcp(localhost:3306)/gmha"}, driver: "mysql"},
		{name: "postgres alias", cfg: ManagerRuntimeConfig{DatabaseDriver: "postgresql", DatabaseDSN: "postgres://gmha:secret@localhost/gmha"}, driver: "postgres"},
		{name: "unsupported", cfg: ManagerRuntimeConfig{DatabaseDriver: "oracle", DatabaseDSN: "unused"}, wantErr: true, driver: "oracle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := normalizeManagerRuntimeConfig(tt.cfg)
			if cfg.DatabaseDriver != tt.driver {
				t.Fatalf("driver = %q, want %q", cfg.DatabaseDriver, tt.driver)
			}
			err := validateManagerDatabaseConfig(cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManagerDatabaseDSNIsBuiltFromRegularFieldsAndRedacted(t *testing.T) {
	cfg := normalizeManagerRuntimeConfig(ManagerRuntimeConfig{
		DatabaseDriver: "mysql", DatabaseHost: "db.internal", DatabasePort: 3306,
		DatabaseName: "gmha", DatabaseUsername: "manager", DatabasePassword: "p@ss word",
	})
	if cfg.DatabaseDSN == "" || !strings.Contains(cfg.DatabaseDSN, "db.internal:3306") {
		t.Fatalf("mysql DSN was not generated: %q", cfg.DatabaseDSN)
	}
	public := cfg.Redacted()
	if public.DatabaseDSN != "" || public.DatabasePassword != "" || !public.DatabasePasswordSet {
		t.Fatalf("database secret was not redacted: %+v", public)
	}
}

func TestSaveConfigRequiresSuccessfulDatabaseTestWhenDatabaseChanges(t *testing.T) {
	service := newTestManagerRuntimeService(t, ManagerRuntimeConfig{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "old.db"),
		ManagerHTTPAddr: "http://192.0.2.10:8080", ManagerGRPCAddr: "192.0.2.10:9100",
	})
	next := normalizeManagerRuntimeConfig(ManagerRuntimeConfig{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "new.db"),
		ManagerHTTPAddr: "http://192.0.2.10:8080", ManagerGRPCAddr: "192.0.2.10:9100",
	})
	if err := service.SaveConfigVerified(context.Background(), next, ""); err == nil {
		t.Fatal("expected untested database change to be rejected")
	}
	result, err := service.TestDatabase(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveConfigVerified(context.Background(), next, result.TestToken); err != nil {
		t.Fatalf("verified database config was rejected: %v", err)
	}
}

func TestSaveConfigRejectsDirectDatabaseSwitchAfterPlatformUse(t *testing.T) {
	service := newTestManagerRuntimeService(t, ManagerRuntimeConfig{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "old.db"),
		ManagerHTTPAddr: "http://192.0.2.10:8080", ManagerGRPCAddr: "192.0.2.10:9100",
	})
	service.SetPlatformUsageChecker(func(context.Context) (bool, error) { return true, nil })
	next := normalizeManagerRuntimeConfig(ManagerRuntimeConfig{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "new.db"),
		ManagerHTTPAddr: "http://192.0.2.10:8080", ManagerGRPCAddr: "192.0.2.10:9100",
	})
	result, err := service.TestDatabase(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	err = service.SaveConfigVerified(context.Background(), next, result.TestToken)
	if err == nil || !strings.Contains(err.Error(), "平台已产生业务数据") {
		t.Fatalf("expected platform-use guard, got %v", err)
	}
}
