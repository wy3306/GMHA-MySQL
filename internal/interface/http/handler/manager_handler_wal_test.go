package handler

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gmha/internal/app"
	sqliteinfra "gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func newWALHandlerTest(t *testing.T) *ManagerHandler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`pragma journal_mode=wal; pragma wal_autocheckpoint=0; create table wal_test(id integer primary key, value text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into wal_test(value) values (?)`, strings.Repeat("x", 64*1024)); err != nil {
		t.Fatal(err)
	}
	maintenance := app.NewDatabaseMaintenanceService(db, sqliteinfra.DialectSQLite, app.Config{DBPath: path, DatabaseDriver: "sqlite"})
	return NewManagerHandler(nil, maintenance)
}

func TestManagerHandlerDatabaseWALStatusAndCleanup(t *testing.T) {
	handler := newWALHandlerTest(t)

	statusRecorder := httptest.NewRecorder()
	handler.HandleDatabaseWAL(statusRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/manager/database/wal", nil))
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"supported":true`) {
		t.Fatalf("unexpected status response: code=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}

	cleanupRecorder := httptest.NewRecorder()
	cleanupRequest := httptest.NewRequest(http.MethodPost, "/api/v1/manager/database/wal", strings.NewReader(`{"confirm":true}`))
	handler.HandleDatabaseWAL(cleanupRecorder, cleanupRequest)
	if cleanupRecorder.Code != http.StatusOK || !strings.Contains(cleanupRecorder.Body.String(), `"completed":true`) {
		t.Fatalf("unexpected cleanup response: code=%d body=%s", cleanupRecorder.Code, cleanupRecorder.Body.String())
	}
}

func TestManagerHandlerDatabaseWALRequiresConfirmation(t *testing.T) {
	handler := newWALHandlerTest(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/manager/database/wal", strings.NewReader(`{"confirm":false}`))
	handler.HandleDatabaseWAL(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
