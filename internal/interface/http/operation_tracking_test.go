package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"gmha/internal/app"
	persistence "gmha/internal/infrastructure/persistence/sqlite"
	_ "modernc.org/sqlite"
)

func TestRelatedTaskIDsRecognizesEveryParentTaskPrefix(t *testing.T) {
	got := relatedTaskIDs([]byte(`{"parent":{"Task":{"ID":"batch-task-1"}},"bootstrap":{"task_id":"cluster-bootstrap-2"},"run_id":"arch-run-3","child":{"task_id":"task-4"}}`))
	want := []string{"arch-run-3", "batch-task-1", "cluster-bootstrap-2", "task-4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relatedTaskIDs() = %v, want %v", got, want)
	}
}

func newOperationTrackingTestService(t *testing.T) *app.TaskService {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := persistence.NewTaskRepository(persistence.NewDB(db, persistence.DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	return app.NewTaskService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func TestTrackPlatformOperationsRecordsFailedMutation(t *testing.T) {
	service := newOperationTrackingTestService(t)
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"机器参数无效"}`))
	}), service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/machines/precheck", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	items, err := service.ListTasks(req.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" || items[0].Type != "platform_operation" {
		t.Fatalf("unexpected tracked tasks: %+v", items)
	}
	detail, err := service.GetTaskDetail(req.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Steps) != 2 || len(detail.Events) != 2 || detail.Events[1].Content != "机器参数无效" {
		t.Fatalf("incomplete operation timeline: %+v", detail)
	}
}

func TestTrackPlatformOperationsLinksBatchChildTasks(t *testing.T) {
	service := newOperationTrackingTestService(t)
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"task_id":"task-child-2"},{"task":{"ID":"task-child-1"},"steps":[{"ID":"task-step-child"}],"events":[{"ID":"task-event-child"}]}]}`))
	}), service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/cluster-mysql-install", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	items, err := service.ListTasks(req.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected aggregate submission task, got %+v", items)
	}
	detail, err := service.GetTaskDetail(req.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Steps) != 4 {
		t.Fatalf("expected request, execution and two related task steps, got %+v", detail.Steps)
	}
}

func TestTrackPlatformOperationsRecordsBusinessLevelBatchFailure(t *testing.T) {
	service := newOperationTrackingTestService(t)
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":0,"failed":2,"items":[{"error":"agent offline"},{"error":"package missing"}]}`))
	}), service)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/cluster-mysql-install", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	items, err := service.ListTasks(req.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("batch business failure was not tracked: %+v", items)
	}
	detail, err := service.GetTaskDetail(req.Context(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) != 2 || detail.Events[1].Content != "批量操作有 2 个子任务创建失败" {
		t.Fatalf("batch failure summary missing: %+v", detail.Events)
	}
}

func TestTrackPlatformOperationsSkipsAgentHeartbeat(t *testing.T) {
	service := newOperationTrackingTestService(t)
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), service)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/heartbeat", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	items, err := service.ListTasks(req.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("heartbeat should not flood task center: %+v", items)
	}
}

func TestTrackPlatformOperationsDeduplicatesIdempotentRequest(t *testing.T) {
	service := newOperationTrackingTestService(t)
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cred-production"}`))
	}), service)

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-credentials", nil)
		req.Header.Set("X-Idempotency-Key", "credential-submit-1")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	items, err := service.ListTasks(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("idempotent retry created %d operation tasks: %+v", len(items), items)
	}
}

func TestTrackPlatformOperationsWrapsReturnedWorkflowParent(t *testing.T) {
	service := newOperationTrackingTestService(t)
	parent, err := service.CreateBatchTrackingTask(context.Background(), "vip_scan", "扫描集群 VIP", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeBatchTrackingTask(context.Background(), parent.Task.ID, 0, 0); err != nil {
		t.Fatal(err)
	}
	handler := trackPlatformOperations(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"` + parent.Task.ID + `"}`))
	}), service)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/clusters/demo/vip/scan", nil))

	items, err := service.ListTasks(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Type != "platform_operation" {
		t.Fatalf("workflow parent was not wrapped into one user operation: %+v", items)
	}
	detail, err := service.GetTaskDetail(context.Background(), items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Children) != 1 || detail.Children[0].ID != parent.Task.ID {
		t.Fatalf("workflow parent relationship missing: %+v", detail.Children)
	}
}
