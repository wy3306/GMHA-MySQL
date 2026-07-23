package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	backupdomain "gmha/internal/domain/backup"
)

type BackupHandler struct{ service *app.BackupService }

func NewBackupHandler(service *app.BackupService) *BackupHandler {
	return &BackupHandler{service: service}
}

type backupPolicyRequest struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Cluster              string            `json:"cluster"`
	MachineID            string            `json:"machine_id"`
	Port                 int               `json:"port"`
	BackupType           string            `json:"backup_type"`
	DiskUsageThreshold   int               `json:"disk_usage_threshold"`
	ScheduleType         string            `json:"schedule_type"`
	Weekdays             []int             `json:"weekdays"`
	WeekdayBackupTypes   map[string]string `json:"weekday_backup_types"`
	IntervalMinutes      int               `json:"interval_minutes"`
	StartAt              time.Time         `json:"start_at"`
	RetryCount           int               `json:"retry_count"`
	RetryIntervalSeconds int               `json:"retry_interval_seconds"`
	IncludeBinlog        bool              `json:"include_binlog"`
	BackupLocation       string            `json:"backup_location"`
	MySQLUser            string            `json:"mysql_user"`
	MySQLPassword        string            `json:"mysql_password"`
	Enabled              *bool             `json:"enabled"`
}

func (h *BackupHandler) HandlePolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListPolicies(r.Context(), strings.TrimSpace(r.URL.Query().Get("cluster")))
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case http.MethodPost:
		h.savePolicy(w, r, "", http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *BackupHandler) HandleTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	items, err := h.service.ListTargets(r.Context(), r.URL.Query().Get("cluster"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *BackupHandler) HandlePolicyByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/backup/policies/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, 400, errors.New("invalid backup policy path"))
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		policy, err := h.service.GetPolicy(r.Context(), id)
		if err != nil {
			writeBackupLookupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		h.savePolicy(w, r, id, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		run, err := h.service.RunPolicy(r.Context(), id)
		if err != nil {
			writeBackupError(w, err)
			return
		}
		writeJSON(w, 201, run)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := h.service.DeletePolicy(r.Context(), id); err != nil {
			writeBackupError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (h *BackupHandler) savePolicy(w http.ResponseWriter, r *http.Request, id string, status int) {
	if id != "" {
		if _, err := h.service.GetPolicy(r.Context(), id); err != nil {
			writeBackupLookupError(w, err)
			return
		}
	}
	var req backupPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if id != "" {
		if req.ID != "" && req.ID != id {
			writeError(w, http.StatusBadRequest, errors.New("请求体策略 ID 与路径不一致"))
			return
		}
		req.ID = id
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item, err := h.service.SavePolicy(r.Context(), backupdomain.Policy{
		ID: req.ID, Name: req.Name, Cluster: req.Cluster, MachineID: req.MachineID,
		Port: req.Port, BackupType: req.BackupType, DiskUsageThreshold: req.DiskUsageThreshold,
		ScheduleType: req.ScheduleType, Weekdays: req.Weekdays, WeekdayBackupTypes: req.WeekdayBackupTypes,
		IntervalMinutes: req.IntervalMinutes, StartAt: req.StartAt, RetryCount: req.RetryCount,
		RetryIntervalSeconds: req.RetryIntervalSeconds, IncludeBinlog: req.IncludeBinlog,
		BackupLocation: req.BackupLocation, MySQLUser: req.MySQLUser,
		MySQLPassword: req.MySQLPassword, Enabled: enabled,
	})
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, status, item)
}

func (h *BackupHandler) HandleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListRuns(r.Context(), strings.TrimSpace(r.URL.Query().Get("cluster")), limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, items)
}

// HandleClusterRuns triggers every enabled backup policy in one or more
// clusters. Credentials and storage settings stay in the existing policies.
func (h *BackupHandler) HandleClusterRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Clusters []string `json:"clusters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.RunClusters(r.Context(), req.Clusters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *BackupHandler) HandleRunByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/backup/runs/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodGet {
		run, err := h.service.GetRun(r.Context(), parts[0])
		if err != nil {
			writeBackupLookupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
		return
	}
	if len(parts) != 2 || parts[1] != "restore" || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Confirmation      string    `json:"confirmation"`
		Mode              string    `json:"mode"`
		BackupPath        string    `json:"backup_path"`
		RestoreTime       time.Time `json:"restore_time"`
		MySQLUser         string    `json:"mysql_user"`
		MySQLPassword     string    `json:"mysql_password"`
		RepairReplication bool      `json:"repair_replication"`
		ApplyFlashback    bool      `json:"apply_flashback"`
		Database          string    `json:"database"`
		Tables            []string  `json:"tables"`
		OutputDir         string    `json:"output_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	task, err := h.service.Restore(r.Context(), parts[0], app.RestoreOptions{
		Confirmation: req.Confirmation, Mode: req.Mode, BackupPath: req.BackupPath,
		RestoreTime: req.RestoreTime, MySQLUser: req.MySQLUser, MySQLPassword: req.MySQLPassword,
		RepairReplication: req.RepairReplication, ApplyFlashback: req.ApplyFlashback,
		Database: req.Database, Tables: req.Tables, OutputDir: req.OutputDir,
	})
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, 201, task)
}

func writeBackupError(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrBackupPolicyNotFound) || errors.Is(err, app.ErrBackupRunNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

func writeBackupLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrBackupPolicyNotFound) || errors.Is(err, app.ErrBackupRunNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}
