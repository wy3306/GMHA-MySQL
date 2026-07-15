package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
)

const maxTrackedResponseBytes = 1 << 20

type trackingResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < maxTrackedResponseBytes {
		remaining := maxTrackedResponseBytes - w.body.Len()
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = w.body.Write(data[:remaining])
	}
	return w.ResponseWriter.Write(data)
}

func trackPlatformOperations(next http.Handler, tasks *app.TaskService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tasks == nil || !isMutatingMethod(r.Method) || isSystemMutation(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		startedAt := time.Now().UTC()
		recorder := &trackingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		finishedAt := time.Now().UTC()
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		related := relatedTaskIDs(recorder.body.Bytes())
		if status < http.StatusBadRequest && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") && len(related) <= 1 && !isBatchTaskEndpoint(r.URL.Path) {
			return
		}
		operation, displayName, target := platformOperationMetadata(r.Method, r.URL.Path)
		spec := taskdomain.PlatformOperationSpec{
			Operation: operation, DisplayName: displayName, Method: r.Method, Path: r.URL.Path,
			Target: target, HTTPStatus: status, DurationMillis: finishedAt.Sub(startedAt).Milliseconds(), RelatedTaskIDs: related,
		}
		errMessage := ""
		if status >= http.StatusBadRequest {
			errMessage = responseError(recorder.body.Bytes(), status)
		} else if isBatchTaskEndpoint(r.URL.Path) {
			errMessage = batchResponseError(recorder.body.Bytes())
		}
		if _, err := tasks.RecordPlatformOperation(context.WithoutCancel(r.Context()), spec, startedAt, finishedAt, errMessage); err != nil {
			log.Printf("record platform operation %s %s: %v", r.Method, r.URL.Path, err)
		}
	})
}

func isBatchTaskEndpoint(path string) bool {
	return path == "/api/v1/tasks/cluster-automation" || path == "/api/v1/tasks/cluster-mysql-install" || path == "/api/v1/tasks/cluster-mysql-uninstall" || path == "/api/v1/tasks/mysql-topology"
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isSystemMutation(path string) bool {
	return path == "/api/v1/agents/register" || path == "/api/v1/agents/heartbeat" || path == "/api/v1/tasks/cluster-automation/report"
}

func platformOperationMetadata(method, path string) (string, string, string) {
	operation := strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/")
	target := "平台"
	labels := []struct{ fragment, label string }{
		{"retry-install", "重试安装 Agent"}, {"repair-mysql-config", "修复 Agent MySQL 配置"}, {"agents/upgrade", "升级 Agent"}, {"agents/uninstall", "卸载 Agent"}, {"agents/recover", "恢复 Agent"},
		{"mysql-install", "部署 MySQL"}, {"mysql-uninstall", "卸载 MySQL"}, {"mysql-upgrade", "升级 MySQL"}, {"mysql-parameters", "维护 MySQL 参数"}, {"mysql-topology", "调整 MySQL 拓扑"},
		{"backup", "备份与恢复操作"}, {"architecture", "调整集群架构"}, {"failover", "集群故障切换"}, {"/vip/", "维护集群 VIP"},
		{"machines", "维护机器资源"}, {"ssh-credentials", "维护 SSH 凭证"}, {"clusters", "维护集群"}, {"packages", "维护安装包"},
		{"manager", "维护 Manager"}, {"dynamic-collect", "维护动态采集配置"}, {"account-presets", "维护 MySQL 账号预设"}, {"mysql/instances", "维护 MySQL 实例"},
	}
	displayName := method + " " + operation
	for _, item := range labels {
		if strings.Contains(path, item.fragment) {
			displayName = item.label
			break
		}
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/"), "/")
	if len(parts) > 1 {
		switch parts[0] {
		case "machines", "clusters", "ssh-credentials":
			if parts[1] != "precheck" && parts[1] != "cleanup" {
				target = parts[1]
			}
		case "backup":
			if len(parts) > 2 {
				target = parts[2]
			}
		case "packages":
			target = parts[len(parts)-1]
		}
	}
	return operation, displayName, target
}

func relatedTaskIDs(data []byte) []string {
	var payload any
	if len(data) == 0 || json.Unmarshal(data, &payload) != nil {
		return nil
	}
	set := make(map[string]struct{})
	var walk func(any, string)
	walk = func(value any, key string) {
		switch typed := value.(type) {
		case map[string]any:
			for childKey, child := range typed {
				walk(child, strings.ToLower(childKey))
			}
		case []any:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			if (key == "task_id" || key == "run_id" || key == "id") && isTrackableTaskID(typed) {
				set[typed] = struct{}{}
			}
		}
	}
	walk(payload, "")
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func isTrackableTaskID(value string) bool {
	if strings.HasPrefix(value, "platform-task-") {
		return true
	}
	if strings.HasPrefix(value, "arch-run-") {
		return true
	}
	return strings.HasPrefix(value, "task-") && !strings.HasPrefix(value, "task-step-") && !strings.HasPrefix(value, "task-event-")
}

func responseError(data []byte, status int) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return payload.Error
	}
	return http.StatusText(status)
}

func batchResponseError(data []byte) string {
	var payload struct {
		Failed int `json:"failed"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Failed > 0 {
		return fmt.Sprintf("批量操作有 %d 个子任务创建失败", payload.Failed)
	}
	return ""
}
