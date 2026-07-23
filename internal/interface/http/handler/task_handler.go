package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
	taskusecase "gmha/internal/usecase/task"
	"golang.org/x/net/websocket"
)

const mysqlDefaultsFilePlaceholder = "__GMHA_MYSQL_DEFAULTS_FILE__"

// TaskHandler 是任务管理 HTTP API 的请求处理器。
type TaskHandler struct {
	service *app.TaskService
}

// NewTaskHandler 创建一个新的 TaskHandler 实例。
func NewTaskHandler(service *app.TaskService) *TaskHandler {
	return &TaskHandler{service: service}
}

// HandleTasks 处理任务列表查询（GET）和按 ID 查询任务详情请求。
func (h *TaskHandler) HandleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		taskID := strings.TrimSpace(r.URL.Query().Get("id"))
		if taskID != "" {
			item, err := h.service.GetTaskDetail(r.Context(), taskID)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
		if r.URL.Query().Get("stats") == "true" {
			stats := map[string]int{}
			for _, status := range []string{"all", "running", "success", "failed"} {
				result, err := h.service.ListTaskPage(r.Context(), app.TaskListQuery{Limit: 1, Statuses: taskStatusFilter(status)})
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				stats[status] = result.Total
			}
			writeJSON(w, http.StatusOK, stats)
			return
		}
		if r.URL.Query().Has("page") || r.URL.Query().Has("page_size") || r.URL.Query().Has("keyword") || r.URL.Query().Has("status") || r.URL.Query().Has("type") {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
			if page <= 0 {
				page = 1
			}
			if pageSize <= 0 {
				pageSize = 50
			}
			result, err := h.service.ListTaskPage(r.Context(), app.TaskListQuery{
				Offset: (page - 1) * pageSize, Limit: pageSize, Keyword: r.URL.Query().Get("keyword"),
				Statuses: taskStatusFilter(r.URL.Query().Get("status")), Types: taskTypeFilter(r.URL.Query().Get("type")),
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 200
		}
		if limit > 2000 {
			limit = 2000
		}
		items, err := h.service.ListTasks(r.Context(), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if items == nil {
			items = []taskdomain.Task{}
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodDelete:
		taskID := strings.TrimSpace(r.URL.Query().Get("id"))
		if taskID != "" {
			if err := h.service.DeleteTask(r.Context(), taskID); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			TaskIDs     []string `json:"task_ids"`
			AllFiltered bool     `json:"all_filtered"`
			Keyword     string   `json:"keyword"`
			Status      string   `json:"status"`
			Type        string   `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("invalid delete tasks request"))
			return
		}
		result, err := h.service.DeleteTasks(r.Context(), app.DeleteTasksRequest{
			TaskIDs: req.TaskIDs, AllFiltered: req.AllFiltered,
			Query: app.TaskListQuery{Keyword: req.Keyword, Statuses: taskStatusFilter(req.Status), Types: taskTypeFilter(req.Type)},
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func taskStatusFilter(value string) []taskdomain.Status {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "running":
		return []taskdomain.Status{taskdomain.StatusPending, taskdomain.StatusSent, taskdomain.StatusRunning}
	case "success":
		return []taskdomain.Status{taskdomain.StatusSuccess}
	case "failed":
		return []taskdomain.Status{taskdomain.StatusFailed}
	default:
		return nil
	}
}

func taskTypeFilter(value string) []taskdomain.Type {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "all" {
		return nil
	}
	return []taskdomain.Type{taskdomain.Type(value)}
}

type createExecTaskRequest struct {
	Machine string `json:"machine"`
	Command string `json:"command"`
}

type createCollectMachineInfoRequest struct {
	Machine string `json:"machine"`
}

// clusterAutomationRequest creates one Agent task per machine in the selected
// clusters. "collect_machine" uses the built-in collector; "shell" dispatches
// the supplied script as an existing exec task.
type clusterAutomationRequest struct {
	Clusters        []string `json:"clusters"`
	TargetMachineID string   `json:"target_machine_id"`
	Operation       string   `json:"operation"`
	Script          string   `json:"script"`
	Port            int      `json:"port"`
	UserAction      string   `json:"user_action"`
	TargetUsername  string   `json:"target_username"`
	TargetPassword  string   `json:"target_password"`
	TargetHost      string   `json:"target_host"`
	Privileges      []string `json:"privileges"`
	ParameterName   string   `json:"parameter_name"`
	ParameterValue  string   `json:"parameter_value"`
	ApplyMode       string   `json:"apply_mode"`
	ConfigPath      string   `json:"config_path"`
	SystemdUnit     string   `json:"systemd_unit"`
}

type clusterAutomationItem struct {
	Cluster   string `json:"cluster"`
	MachineID string `json:"machine_id"`
	Machine   string `json:"machine"`
	IP        string `json:"ip"`
	TaskID    string `json:"task_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type clusterAutomationResponse struct {
	Operation    string                  `json:"operation"`
	ParentTaskID string                  `json:"parent_task_id"`
	Created      int                     `json:"created"`
	Failed       int                     `json:"failed"`
	Items        []clusterAutomationItem `json:"items"`
}

type clusterAutomationCollectionRow struct {
	TaskID           string `json:"task_id"`
	MachineID        string `json:"machine_id"`
	Machine          string `json:"machine"`
	IP               string `json:"ip"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	OS               string `json:"os,omitempty"`
	Architecture     string `json:"architecture,omitempty"`
	CPUCores         int    `json:"cpu_cores,omitempty"`
	MemoryGB         int    `json:"memory_gb,omitempty"`
	DiskFreeGB       int    `json:"disk_free_gb,omitempty"`
	GlibcVersion     string `json:"glibc_version,omitempty"`
	SELinux          string `json:"selinux,omitempty"`
	Firewall         string `json:"firewall,omitempty"`
	NTPEnabled       bool   `json:"ntp_enabled"`
	TimeOffsetMS     int64  `json:"time_offset_ms,omitempty"`
	MySQLVersion     string `json:"mysql_version,omitempty"`
	MySQLPort        int    `json:"mysql_port,omitempty"`
	ThreadsConnected string `json:"threads_connected,omitempty"`
	ThreadsRunning   string `json:"threads_running,omitempty"`
	Questions        string `json:"questions,omitempty"`
	Queries          string `json:"queries,omitempty"`
	QPS              string `json:"qps,omitempty"`
	TPS              string `json:"tps,omitempty"`
	SlowQueries      string `json:"slow_queries,omitempty"`
	Uptime           string `json:"uptime,omitempty"`
}

type clusterAutomationCollectionResponse struct {
	Operation string                           `json:"operation"`
	Ready     bool                             `json:"ready"`
	Pending   int                              `json:"pending"`
	Failed    int                              `json:"failed"`
	Rows      []clusterAutomationCollectionRow `json:"rows"`
}

type mysqlUserTaskRequest struct {
	Machine        string   `json:"machine"`
	Port           int      `json:"port"`
	Action         string   `json:"action"`
	TargetUsername string   `json:"target_username"`
	TargetPassword string   `json:"target_password"`
	TargetHost     string   `json:"target_host"`
	Privileges     []string `json:"privileges"`
}

type mysqlIndexTaskRequest struct {
	Machine          string                    `json:"machine"`
	Port             int                       `json:"port"`
	Action           string                    `json:"action"`
	Schema           string                    `json:"schema"`
	Table            string                    `json:"table"`
	Name             string                    `json:"name"`
	NewName          string                    `json:"new_name"`
	Kind             string                    `json:"kind"`
	Columns          []mysqlIndexColumnRequest `json:"columns"`
	LockMode         string                    `json:"lock_mode"`
	Purpose          string                    `json:"purpose"`
	Impact           string                    `json:"impact"`
	LockAcknowledged bool                      `json:"lock_acknowledged"`
	OnlineWithPT     bool                      `json:"online_with_pt"`
	Confirmation     string                    `json:"confirmation"`
}

type mysqlIndexColumnRequest struct {
	Name      string `json:"name"`
	PrefixLen int    `json:"prefix_length"`
	Direction string `json:"direction"`
}

// mysqlOnlineDDLTaskRequest deliberately accepts an ALTER clause instead of
// arbitrary SQL. The Manager always executes it through pt-online-schema-change
// and keeps connection credentials inside the Agent.
type mysqlOnlineDDLTaskRequest struct {
	Machine                string  `json:"machine"`
	Port                   int     `json:"port"`
	Action                 string  `json:"action"`
	Schema                 string  `json:"schema"`
	Table                  string  `json:"table"`
	Alter                  string  `json:"alter"`
	Purpose                string  `json:"purpose"`
	Impact                 string  `json:"impact"`
	MaxLoadThreadsRunning  int     `json:"max_load_threads_running"`
	CriticalThreadsRunning int     `json:"critical_threads_running"`
	MaxLagSeconds          int     `json:"max_lag_seconds"`
	ChunkTimeSeconds       float64 `json:"chunk_time_seconds"`
	CheckIntervalSeconds   int     `json:"check_interval_seconds"`
	AlterForeignKeysMethod string  `json:"alter_foreign_keys_method"`
	RiskAcknowledged       bool    `json:"risk_acknowledged"`
	Confirmation           string  `json:"confirmation"`
}

// mysqlArchiveTaskRequest describes a same-instance pt-archiver workflow.
// Credentials are intentionally absent: the Agent injects its registered MHA
// account through a short-lived defaults file immediately before execution.
type mysqlArchiveTaskRequest struct {
	Machine           string `json:"machine"`
	Port              int    `json:"port"`
	Action            string `json:"action"`
	SourceSchema      string `json:"source_schema"`
	SourceTable       string `json:"source_table"`
	DestinationSchema string `json:"destination_schema"`
	DestinationTable  string `json:"destination_table"`
	Where             string `json:"where"`
	Index             string `json:"index,omitempty"`
	BatchSize         int    `json:"batch_size"`
	SleepSeconds      int    `json:"sleep_seconds"`
	RunTimeSeconds    int    `json:"run_time_seconds,omitempty"`
	DeleteSource      bool   `json:"delete_source"`
	RiskAcknowledged  bool   `json:"risk_acknowledged"`
	Confirmation      string `json:"confirmation"`
}

type createMySQLInstallTaskRequest struct {
	Machine           string                      `json:"machine"`
	Port              int                         `json:"port"`
	ServerID          int                         `json:"server_id"`
	MySQLUser         string                      `json:"mysql_user"`
	InstanceDir       string                      `json:"instance_dir"`
	DataDir           string                      `json:"data_dir"`
	BinlogDir         string                      `json:"binlog_dir"`
	RedoDir           string                      `json:"redo_dir"`
	UndoDir           string                      `json:"undo_dir"`
	TmpDir            string                      `json:"tmp_dir"`
	BaseDir           string                      `json:"base_dir"`
	MyCnfPath         string                      `json:"my_cnf_path"`
	SocketPath        string                      `json:"socket_path"`
	ErrorLog          string                      `json:"error_log"`
	PIDFile           string                      `json:"pid_file"`
	CharacterSetsDir  string                      `json:"character_sets_dir"`
	PluginDir         string                      `json:"plugin_dir"`
	RootPassword      string                      `json:"root_password"`
	Profile           string                      `json:"profile"`
	PackageName       string                      `json:"package_name"`
	Version           string                      `json:"version"`
	Architecture      string                      `json:"architecture"`
	InstallPTTools    bool                        `json:"install_pt_tools"`
	InstallXtraBackup bool                        `json:"install_xtrabackup"`
	MemoryAllocator   string                      `json:"memory_allocator"`
	RuntimeParameters map[string]string           `json:"runtime_parameters"`
	Accounts          []createMySQLAccountRequest `json:"accounts"`
}

type createMySQLUninstallTaskRequest struct {
	Machine string `json:"machine"`
	Port    int    `json:"port"`
}

type mysqlParameterTaskRequest struct {
	Machine          string                        `json:"machine"`
	Port             int                           `json:"port"`
	Action           string                        `json:"action"`
	Name             string                        `json:"name"`
	Value            string                        `json:"value"`
	ApplyMode        string                        `json:"apply_mode"`
	ConfigPath       string                        `json:"config_path"`
	SystemdUnit      string                        `json:"systemd_unit"`
	Restart          bool                          `json:"restart"`
	RestartConfirmed bool                          `json:"restart_confirmed"`
	Targets          []mysqlParameterTargetRequest `json:"targets"`
	RestartTargets   []mysqlParameterTargetRequest `json:"restart_targets"`
	Changes          []mysqlParameterChangeRequest `json:"changes"`
	MySQLDPath       string                        `json:"-"`
	Version          string                        `json:"-"`
}

type mysqlParameterTargetRequest struct {
	Machine     string `json:"machine"`
	Port        int    `json:"port"`
	ConfigPath  string `json:"config_path"`
	SystemdUnit string `json:"systemd_unit"`
	MySQLDPath  string `json:"-"`
	Version     string `json:"-"`
}

type mysqlParameterChangeRequest struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type mysqlUpgradeTaskRequest struct {
	Machine          string `json:"machine"`
	Port             int    `json:"port"`
	PackageName      string `json:"package_name"`
	PrecheckTaskID   string `json:"precheck_task_id"`
	Force            bool   `json:"force"`
	RiskAcknowledged bool   `json:"risk_acknowledged"`
}

type createMySQLTopologyTaskRequest struct {
	Topology            string                           `json:"topology"`
	Port                int                              `json:"port"`
	RootPassword        string                           `json:"root_password"`
	ReplicationUser     string                           `json:"replication_user"`
	ReplicationPassword string                           `json:"replication_password"`
	CloneUser           string                           `json:"clone_user"`
	ClonePassword       string                           `json:"clone_password"`
	UseClone            bool                             `json:"use_clone"`
	PrimaryMachine      string                           `json:"primary_machine"`
	CloneSeedMachine    string                           `json:"clone_seed_machine"`
	CloneTargetMachines []string                         `json:"clone_target_machines"`
	ParallelType        string                           `json:"parallel_type"`
	ParallelWorkers     int                              `json:"parallel_workers"`
	Nodes               []createMySQLTopologyNodeRequest `json:"nodes"`
}

type createMySQLTopologyNodeRequest struct {
	Machine       string `json:"machine"`
	Port          int    `json:"port"`
	Role          string `json:"role"`
	SourceMachine string `json:"source_machine,omitempty"`
	DelaySeconds  int    `json:"delay_seconds,omitempty"`
}

type createClusterMySQLInstallTaskRequest struct {
	Cluster           string                      `json:"cluster"`
	Port              int                         `json:"port"`
	ServerIDStart     int                         `json:"server_id_start"`
	MySQLUser         string                      `json:"mysql_user"`
	InstanceDir       string                      `json:"instance_dir"`
	DataDir           string                      `json:"data_dir"`
	BinlogDir         string                      `json:"binlog_dir"`
	RedoDir           string                      `json:"redo_dir"`
	UndoDir           string                      `json:"undo_dir"`
	TmpDir            string                      `json:"tmp_dir"`
	BaseDir           string                      `json:"base_dir"`
	MyCnfPath         string                      `json:"my_cnf_path"`
	SocketPath        string                      `json:"socket_path"`
	ErrorLog          string                      `json:"error_log"`
	PIDFile           string                      `json:"pid_file"`
	CharacterSetsDir  string                      `json:"character_sets_dir"`
	PluginDir         string                      `json:"plugin_dir"`
	RootPassword      string                      `json:"root_password"`
	Profile           string                      `json:"profile"`
	Version           string                      `json:"version"`
	Architecture      string                      `json:"architecture"`
	InstallPTTools    bool                        `json:"install_pt_tools"`
	InstallXtraBackup bool                        `json:"install_xtrabackup"`
	MemoryAllocator   string                      `json:"memory_allocator"`
	RuntimeParameters map[string]string           `json:"runtime_parameters"`
	Accounts          []createMySQLAccountRequest `json:"accounts"`
}

type createMySQLAccountRequest struct {
	Role           string   `json:"role"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	Host           string   `json:"host"`
	Enabled        *bool    `json:"enabled"`
	ExtendedBackup bool     `json:"extended_backup,omitempty"`
	Privileges     []string `json:"privileges,omitempty"`
}

// HandleCreateExecTask 处理创建 exec 命令执行任务请求。
func (h *TaskHandler) HandleCreateExecTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createExecTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateExecTask(r.Context(), req.Machine, req.Command)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// HandleCreateCollectMachineInfoTask 处理创建机器信息采集任务请求。
func (h *TaskHandler) HandleCreateCollectMachineInfoTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createCollectMachineInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateCollectMachineInfoTask(r.Context(), req.Machine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// HandleClusterAutomation creates one task per target machine for a selected
// set of clusters. The task output remains in the normal task event stream and
// can subsequently be exported through HandleClusterAutomationReport.
func (h *TaskHandler) HandleClusterAutomation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req clusterAutomationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Operation = strings.TrimSpace(req.Operation)
	if err := validateClusterAutomationRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Clusters = normalizeAutomationClusters(req.Clusters)
	machines, err := h.service.ListClusterMachines(r.Context(), req.Clusters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if target := strings.TrimSpace(req.TargetMachineID); target != "" {
		filtered := machines[:0]
		for _, machine := range machines {
			if machine.ID == target {
				filtered = append(filtered, machine)
			}
		}
		machines = filtered
		if len(machines) == 0 {
			writeError(w, http.StatusBadRequest, errors.New("target machine is not a member of the selected cluster"))
			return
		}
	}
	parent, err := h.service.CreateBatchTrackingTask(r.Context(), "cluster_automation", "集群批量操作", strings.Join(req.Clusters, ", "))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result := clusterAutomationResponse{Operation: req.Operation, ParentTaskID: parent.Task.ID, Items: make([]clusterAutomationItem, 0, len(machines))}
	for _, machine := range machines {
		item := clusterAutomationItem{Cluster: machine.Cluster, MachineID: machine.ID, Machine: machine.Name, IP: machine.IP}
		var detail app.TaskDetail
		if req.Operation == "collect_machine" {
			detail, err = h.service.CreateCollectMachineInfoTask(r.Context(), machine.IP)
		} else {
			var instance mysqlapp.Instance
			if isDatabaseAutomationOperation(req.Operation) {
				_, instance, err = h.service.ResolveMySQLInstance(r.Context(), machine.ID, req.Port)
				if err != nil {
					item.Error = fmt.Sprintf("端口 %d 未登记可管理的 MySQL 实例: %v", req.Port, err)
					result.Failed++
					result.Items = append(result.Items, item)
					continue
				}
				if compatible, reason := h.service.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
					item.Error = reason
					result.Failed++
					result.Items = append(result.Items, item)
					continue
				}
			}
			command, commandErr := clusterAutomationCommand(req, instance)
			if commandErr != nil {
				item.Error = commandErr.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			if opts, ok := clusterAutomationTaskOptions(req); ok {
				opts.ParentTaskID = parent.Task.ID
				detail, err = h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, command, opts)
			} else {
				detail, err = h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, command, app.ExecTaskOptions{ParentTaskID: parent.Task.ID})
			}
		}
		if err != nil {
			item.Error = err.Error()
			result.Failed++
		} else {
			item.TaskID = detail.Task.ID
			if detail.Task.ParentTaskID == "" {
				if attachErr := h.service.AttachChildTasks(r.Context(), parent.Task.ID, []string{detail.Task.ID}); attachErr != nil {
					item.Error = attachErr.Error()
					result.Failed++
					result.Items = append(result.Items, item)
					continue
				}
			}
			if req.Operation == "mysql_user" {
				go redactAutomationCommandAfterCompletion(h.service, detail.Task.ID)
			}
			result.Created++
		}
		result.Items = append(result.Items, item)
	}
	if err := h.service.FinalizeBatchTrackingTask(r.Context(), parent.Task.ID, result.Created, result.Failed); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func redactAutomationCommandAfterCompletion(service *app.TaskService, taskID string) {
	finished, err := service.WaitForTask(context.Background(), taskID, 5*time.Minute)
	if err == nil && (finished.Task.Status == taskdomain.StatusSuccess || finished.Task.Status == taskdomain.StatusFailed) {
		_ = service.RedactExecTaskCommand(context.Background(), taskID)
	}
}

// HandleClusterAutomationResults returns structured collection data for the
// current automation page. Agent tasks remain an internal transport detail;
// callers poll this endpoint and render the resulting rows directly.
func (h *TaskHandler) HandleClusterAutomationResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	operation := strings.TrimSpace(r.URL.Query().Get("operation"))
	if operation != "collect_machine" && operation != "collect_mysql" {
		writeError(w, http.StatusBadRequest, errors.New("collection operation must be collect_machine or collect_mysql"))
		return
	}
	rawIDs := strings.Split(strings.TrimSpace(r.URL.Query().Get("task_ids")), ",")
	if len(rawIDs) == 0 || strings.TrimSpace(rawIDs[0]) == "" || len(rawIDs) > 1000 {
		writeError(w, http.StatusBadRequest, errors.New("between 1 and 1000 task_ids are required"))
		return
	}
	result := clusterAutomationCollectionResponse{Operation: operation, Ready: true, Rows: make([]clusterAutomationCollectionRow, 0, len(rawIDs))}
	for _, rawID := range rawIDs {
		taskID := strings.TrimSpace(rawID)
		if taskID == "" {
			continue
		}
		detail, err := h.service.GetTaskDetail(r.Context(), taskID)
		if err != nil {
			result.Failed++
			result.Rows = append(result.Rows, clusterAutomationCollectionRow{TaskID: taskID, Status: "failed", Error: err.Error()})
			continue
		}
		row := clusterAutomationCollectionRow{
			TaskID: taskID, MachineID: detail.Task.MachineID, Machine: detail.MachineName, IP: detail.MachineIP,
			Status: string(detail.Task.Status),
		}
		if !automationCollectionTaskMatches(operation, detail.Task) {
			row.Status, row.Error = "failed", "task does not belong to the requested collection operation"
			result.Failed++
			result.Rows = append(result.Rows, row)
			continue
		}
		if detail.Task.Status != taskdomain.StatusSuccess && detail.Task.Status != taskdomain.StatusFailed {
			result.Ready = false
			result.Pending++
			result.Rows = append(result.Rows, row)
			continue
		}
		if detail.Task.Status == taskdomain.StatusFailed {
			row.Error = automationTaskFailure(detail)
			result.Failed++
			result.Rows = append(result.Rows, row)
			continue
		}
		if operation == "collect_machine" {
			info, ok, infoErr := h.service.GetCollectedMachineInfo(r.Context(), detail.Task.MachineID)
			if infoErr != nil || !ok {
				row.Status = "failed"
				if infoErr != nil {
					row.Error = infoErr.Error()
				} else {
					row.Error = "machine collection completed without persisted data"
				}
				result.Failed++
			} else {
				row.Hostname, row.OS, row.Architecture = info.Hostname, info.OS, info.Arch
				row.CPUCores, row.MemoryGB, row.DiskFreeGB = info.CPUCores, info.MemoryGB, info.DiskFreeGB
				row.GlibcVersion, row.SELinux, row.Firewall = info.GlibcVersion, info.SELinux, info.Firewall
				row.NTPEnabled, row.TimeOffsetMS = info.NTPEnabled, info.TimeOffsetMS
			}
		} else {
			parseMySQLAutomationCollection(&row, detail.Events)
			if row.MySQLVersion == "" {
				row.Status = "failed"
				row.Error = "MySQL collection completed without structured output"
				result.Failed++
			}
		}
		result.Rows = append(result.Rows, row)
	}
	writeJSON(w, http.StatusOK, result)
}

func automationCollectionTaskMatches(operation string, task taskdomain.Task) bool {
	if operation == "collect_machine" {
		return task.Type == taskdomain.TypeCollectMachineInfo
	}
	if operation != "collect_mysql" || task.Type != taskdomain.TypeExec {
		return false
	}
	var spec taskdomain.ExecSpec
	return json.Unmarshal(task.SpecJSON, &spec) == nil && spec.Operation == "mysql_collect"
}

func automationTaskFailure(detail app.TaskDetail) string {
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if detail.Events[i].EventType == taskdomain.EventError && strings.TrimSpace(detail.Events[i].Content) != "" {
			return strings.TrimSpace(detail.Events[i].Content)
		}
	}
	for i := len(detail.Steps) - 1; i >= 0; i-- {
		if detail.Steps[i].Status == taskdomain.StepFailed && strings.TrimSpace(detail.Steps[i].Message) != "" {
			return strings.TrimSpace(detail.Steps[i].Message)
		}
	}
	return "collection failed"
}

func parseMySQLAutomationCollection(row *clusterAutomationCollectionRow, events []taskdomain.Event) {
	statusValues := map[string]*string{
		"Threads_connected": &row.ThreadsConnected,
		"Threads_running":   &row.ThreadsRunning,
		"Questions":         &row.Questions,
		"Queries":           &row.Queries,
		"Slow_queries":      &row.SlowQueries,
		"Uptime":            &row.Uptime,
	}
	rateValues := map[string]*string{"QPS": &row.QPS, "TPS": &row.TPS}
	for _, event := range events {
		for _, line := range strings.Split(strings.ReplaceAll(event.Content, "\r\n", "\n"), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "\t")
			if len(parts) >= 4 && parts[0] == "GMHA_MYSQL_INSTANCE" {
				row.Hostname, row.MySQLVersion = parts[1], parts[2]
				row.MySQLPort, _ = strconv.Atoi(parts[3])
			}
			if len(parts) >= 3 && parts[0] == "GMHA_MYSQL_STATUS" {
				if target := statusValues[parts[1]]; target != nil {
					*target = parts[2]
				}
			}
			if len(parts) >= 3 && parts[0] == "GMHA_MYSQL_RATE" {
				if target := rateValues[parts[1]]; target != nil {
					*target = parts[2]
				}
			}
		}
	}
}

func databaseAutomationTaskOptions(req clusterAutomationRequest) (app.ExecTaskOptions, bool) {
	port := req.Port
	switch req.Operation {
	case "collect_mysql":
		return app.ExecTaskOptions{Operation: "mysql_collect", DisplayName: "采集 MySQL 运行数据", StepName: "查询数据库运行状态", Port: port}, true
	case "database_inspection":
		return app.ExecTaskOptions{Operation: "database_inspection", DisplayName: "数据库巡检", StepName: "执行数据库巡检", Port: port}, true
	case "database_deep_inspection":
		return app.ExecTaskOptions{Operation: "database_deep_inspection", DisplayName: "数据库深度巡检", StepName: "执行数据库深度巡检", Port: port}, true
	case "mysql_parameter":
		return app.ExecTaskOptions{Operation: "mysql_parameter", DisplayName: "修改 MySQL 参数 " + strings.TrimSpace(req.ParameterName), StepName: "应用数据库参数", Port: port}, true
	case "mysql_user":
		action := map[string]string{"create": "创建数据库用户", "update": "修改数据库用户密码", "delete": "删除数据库用户", "grant": "授予数据库权限", "revoke": "回收数据库权限", "query": "查询数据库授权", "list": "查询数据库用户", "lock": "锁定数据库用户", "unlock": "解锁数据库用户"}[req.UserAction]
		return app.ExecTaskOptions{Operation: "mysql_user_" + req.UserAction, DisplayName: action, StepName: action, Port: port}, true
	default:
		return app.ExecTaskOptions{}, false
	}
}

func clusterAutomationTaskOptions(req clusterAutomationRequest) (app.ExecTaskOptions, bool) {
	if req.Operation == "shell" {
		return app.ExecTaskOptions{Operation: "cluster_shell", DisplayName: "执行集群 Shell 脚本", StepName: "执行 Shell 脚本"}, true
	}
	return databaseAutomationTaskOptions(req)
}

func normalizeAutomationClusters(clusters []string) []string {
	seen := make(map[string]bool, len(clusters))
	result := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		cluster = strings.TrimSpace(cluster)
		if cluster == "" || seen[cluster] {
			continue
		}
		seen[cluster] = true
		result = append(result, cluster)
	}
	return result
}

func isDatabaseAutomationOperation(operation string) bool {
	switch operation {
	case "collect_mysql", "mysql_user", "mysql_parameter", "database_inspection", "database_deep_inspection":
		return true
	default:
		return false
	}
}

var mysqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$.-]{0,63}$`)
var mysqlHostPattern = regexp.MustCompile(`^[A-Za-z0-9%_.*:.-]{1,255}$`)
var mysqlParameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var mysqlIndexIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_$]{1,64}$`)
var mysqlOnlineDDLStartPattern = regexp.MustCompile(`(?i)^(ADD|DROP|MODIFY|CHANGE|ALTER|CONVERT|ENGINE|ROW_FORMAT|DEFAULT|ORDER|RENAME[[:space:]]+(COLUMN|INDEX))[[:space:]=]`)
var mysqlOnlineDDLUnsafePattern = regexp.MustCompile(`(?i)(;|--|#[^\n]*|/\*|\*/|\b(ALGORITHM|LOCK)[[:space:]]*=|\bRENAME[[:space:]]+(TO|AS)\b|\b(DIScard|IMPORT)[[:space:]]+TABLESPACE\b)`)
var mysqlArchiveUnsafeWherePattern = regexp.MustCompile(`(?i)(;|--|#[^\n]*|/\*|\*/|\b(INSERT|UPDATE|DELETE|REPLACE|DROP|ALTER|CREATE|TRUNCATE|RENAME|GRANT|REVOKE|CALL|LOAD|OUTFILE|DUMPFILE|HANDLER|DO|SET|SLEEP|BENCHMARK|GET_LOCK|RELEASE_LOCK)[[:space:](])`)

var mysqlPrivilegeSet = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true, "CREATE USER": true, "ALTER": true, "DROP": true,
	"SHOW VIEW": true, "TRIGGER": true, "EVENT": true, "PROCESS": true, "RELOAD": true, "LOCK TABLES": true,
	"REPLICATION CLIENT": true, "REPLICATION SLAVE": true, "SUPER": true, "CONNECTION_ADMIN": true, "SYSTEM_VARIABLES_ADMIN": true, "REPLICATION_SLAVE_ADMIN": true, "BACKUP_ADMIN": true, "CLONE_ADMIN": true,
}

var mysqlDynamicPrivileges = map[string]bool{
	"CONNECTION_ADMIN":        true,
	"SYSTEM_VARIABLES_ADMIN":  true,
	"REPLICATION_SLAVE_ADMIN": true,
	"BACKUP_ADMIN":            true,
	"CLONE_ADMIN":             true,
}

func validateClusterAutomationRequest(req clusterAutomationRequest) error {
	clusterNames := make(map[string]struct{}, len(req.Clusters))
	for _, cluster := range req.Clusters {
		if cluster = strings.TrimSpace(cluster); cluster != "" {
			clusterNames[cluster] = struct{}{}
		}
	}
	if len(clusterNames) == 0 {
		return errors.New("at least one target cluster is required")
	}
	switch req.Operation {
	case "collect_machine", "shell", "collect_mysql", "mysql_user", "mysql_parameter", "database_inspection", "database_deep_inspection":
	default:
		return fmt.Errorf("unsupported automation operation %q", req.Operation)
	}
	if req.Operation == "shell" && strings.TrimSpace(req.Script) == "" {
		return errors.New("shell script is required")
	}
	if req.Operation == "shell" && (len(req.Script) > 256*1024 || strings.ContainsRune(req.Script, '\x00')) {
		return errors.New("shell script must be at most 256 KiB and contain no NUL bytes")
	}
	if req.Operation == "collect_mysql" || req.Operation == "mysql_user" || req.Operation == "mysql_parameter" || req.Operation == "database_inspection" || req.Operation == "database_deep_inspection" {
		if req.Port <= 0 || req.Port > 65535 {
			return errors.New("a valid MySQL port is required")
		}
	}
	if req.Operation == "mysql_user" {
		return validateMySQLUserAction(req.UserAction, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges)
	}
	if req.Operation == "mysql_parameter" {
		if !mysqlParameterPattern.MatchString(strings.TrimSpace(req.ParameterName)) || strings.TrimSpace(req.ParameterValue) == "" || strings.ContainsAny(req.ParameterValue, "\r\n\x00") {
			return errors.New("valid MySQL parameter name and value are required")
		}
		if req.ApplyMode != "dynamic" && req.ApplyMode != "restart" && req.ApplyMode != "both" {
			return errors.New("apply_mode must be dynamic, restart, or both")
		}
		if req.ApplyMode == "restart" || req.ApplyMode == "both" {
			if req.ConfigPath != "" && !filepath.IsAbs(req.ConfigPath) {
				return errors.New("MySQL config path must be absolute")
			}
			if req.SystemdUnit != "" && !regexp.MustCompile(`^[A-Za-z0-9@_.-]+$`).MatchString(req.SystemdUnit) {
				return errors.New("invalid systemd unit")
			}
		}
	}
	return nil
}

func validateMySQLUserAction(action, username, host, password string, privileges []string) error {
	switch action {
	case "create", "update", "delete", "grant", "revoke", "query", "list", "lock", "unlock":
	default:
		return errors.New("invalid database user action")
	}
	if action != "list" && (!mysqlIdentifierPattern.MatchString(strings.TrimSpace(username)) || !mysqlHostPattern.MatchString(strings.TrimSpace(host))) {
		return errors.New("invalid database username or host")
	}
	if (action == "create" || action == "update") && password == "" {
		return errors.New("database user password is required")
	}
	if (action == "create" || action == "grant" || action == "revoke") && len(privileges) == 0 {
		return errors.New("at least one privilege is required")
	}
	for _, privilege := range privileges {
		if !mysqlPrivilegeSet[strings.ToUpper(strings.TrimSpace(privilege))] {
			return fmt.Errorf("unsupported MySQL privilege %q", privilege)
		}
	}
	return nil
}

func validateMySQLUserPrivilegesForVersion(privileges []string, version string) error {
	for _, privilege := range privileges {
		privilege = strings.ToUpper(strings.TrimSpace(privilege))
		if mysqlDynamicPrivileges[privilege] && !mysqlapp.SupportsDynamicPrivilegeForVersion(version, privilege) {
			if mysqlapp.IsMySQL57(version) {
				return fmt.Errorf("MySQL 5.7 does not support dynamic privilege %s; use a compatible static privilege such as SUPER where appropriate", privilege)
			}
			return fmt.Errorf("MySQL %s does not support platform-managed dynamic privilege %s; use a compatible static privilege such as SUPER where appropriate", version, privilege)
		}
	}
	return nil
}

func mysqlUserSQL(action, username, host, password string, items []string) (string, error) {
	if err := validateMySQLUserAction(action, username, host, password, items); err != nil {
		return "", err
	}
	user, source := sqlString(username), sqlString(host)
	privileges := strings.Join(normalizePrivileges(items), ", ")
	switch action {
	case "create":
		return fmt.Sprintf("CREATE USER IF NOT EXISTS %s@%s IDENTIFIED BY %s; ALTER USER %s@%s IDENTIFIED BY %s; GRANT %s ON *.* TO %s@%s; FLUSH PRIVILEGES;", user, source, sqlString(password), user, source, sqlString(password), privileges, user, source), nil
	case "update":
		return fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s; FLUSH PRIVILEGES;", user, source, sqlString(password)), nil
	case "delete":
		return fmt.Sprintf("DROP USER IF EXISTS %s@%s;", user, source), nil
	case "grant":
		return fmt.Sprintf("GRANT %s ON *.* TO %s@%s; FLUSH PRIVILEGES;", privileges, user, source), nil
	case "revoke":
		return fmt.Sprintf("REVOKE %s ON *.* FROM %s@%s; FLUSH PRIVILEGES;", privileges, user, source), nil
	case "query":
		return fmt.Sprintf("SHOW GRANTS FOR %s@%s;", user, source), nil
	case "list":
		return "SELECT 'GMHA_MYSQL_USER', u.user, u.host, u.account_locked, COALESCE(GROUP_CONCAT(DISTINCT p.PRIVILEGE_TYPE ORDER BY p.PRIVILEGE_TYPE SEPARATOR ','), '') AS privileges, IF(CONCAT(u.user, '@', u.host) = CURRENT_USER(), 'Y', 'N') AS management_account FROM mysql.user u LEFT JOIN information_schema.user_privileges p ON p.GRANTEE = CONCAT(QUOTE(u.user), '@', QUOTE(u.host)) GROUP BY u.user, u.host, u.account_locked ORDER BY u.user, u.host;", nil
	case "lock":
		return fmt.Sprintf("ALTER USER %s@%s ACCOUNT LOCK;", user, source), nil
	case "unlock":
		return fmt.Sprintf("ALTER USER %s@%s ACCOUNT UNLOCK;", user, source), nil
	default:
		return "", errors.New("invalid database user action")
	}
}

func mysqlUserTaskCommand(baseDir string, req mysqlUserTaskRequest) (string, error) {
	sql, err := mysqlUserSQL(req.Action, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges)
	if err != nil {
		return "", err
	}
	client := fmt.Sprintf("%s --defaults-extra-file=__GMHA_MYSQL_DEFAULTS_FILE__ --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", shellQuote(filepath.Join(baseDir, "bin", "mysql")), req.Port)
	return client + " --execute=" + shellQuote(sql), nil
}

func validateMySQLIndexRequest(req mysqlIndexTaskRequest) error {
	switch req.Action {
	case "list":
		return nil
	case "create":
		if !mysqlIndexIdentifierPattern.MatchString(req.Schema) || !mysqlIndexIdentifierPattern.MatchString(req.Table) || !mysqlIndexIdentifierPattern.MatchString(req.Name) {
			return errors.New("schema, table and index name must be valid MySQL identifiers")
		}
		if strings.EqualFold(req.Name, "PRIMARY") {
			return errors.New("primary key management is not supported by this workflow")
		}
		if req.Purpose == "" || req.Impact == "" {
			return errors.New("index purpose and expected impact are required")
		}
		if len(req.Purpose) > 500 || len(req.Impact) > 500 || strings.ContainsRune(req.Purpose, '\x00') || strings.ContainsRune(req.Impact, '\x00') {
			return errors.New("index purpose and expected impact must be at most 500 characters and contain no NUL bytes")
		}
		if !req.LockAcknowledged {
			return errors.New("the metadata-lock and DDL impact acknowledgement is required")
		}
		if len(req.Columns) == 0 || len(req.Columns) > 16 {
			return errors.New("an index requires between 1 and 16 columns")
		}
		switch req.Kind {
		case "btree", "unique", "fulltext", "spatial":
		default:
			return errors.New("index kind must be btree, unique, fulltext, or spatial")
		}
		switch req.LockMode {
		case "", "none", "shared", "exclusive", "default":
		default:
			return errors.New("lock_mode must be none, shared, exclusive, or default")
		}
		for _, column := range req.Columns {
			if !mysqlIndexIdentifierPattern.MatchString(strings.TrimSpace(column.Name)) {
				return fmt.Errorf("invalid index column %q", column.Name)
			}
			if column.PrefixLen < 0 || column.PrefixLen > 3072 {
				return fmt.Errorf("invalid prefix length for column %s", column.Name)
			}
			direction := strings.ToUpper(strings.TrimSpace(column.Direction))
			if direction != "" && direction != "ASC" && direction != "DESC" {
				return fmt.Errorf("invalid direction for column %s", column.Name)
			}
			if (req.Kind == "fulltext" || req.Kind == "spatial") && direction != "" {
				return fmt.Errorf("%s indexes do not accept a column direction", req.Kind)
			}
		}
		return nil
	case "rename":
		if !mysqlIndexIdentifierPattern.MatchString(req.Schema) || !mysqlIndexIdentifierPattern.MatchString(req.Table) ||
			!mysqlIndexIdentifierPattern.MatchString(req.Name) || !mysqlIndexIdentifierPattern.MatchString(req.NewName) {
			return errors.New("schema, table, current name and new name must be valid MySQL identifiers")
		}
		if strings.EqualFold(req.Name, "PRIMARY") {
			return errors.New("the primary key cannot be renamed")
		}
		if req.Name == req.NewName {
			return errors.New("the new index name must be different")
		}
		return nil
	case "delete":
		if !mysqlIndexIdentifierPattern.MatchString(req.Schema) || !mysqlIndexIdentifierPattern.MatchString(req.Table) || !mysqlIndexIdentifierPattern.MatchString(req.Name) {
			return errors.New("schema, table and index name must be valid MySQL identifiers")
		}
		if strings.EqualFold(req.Name, "PRIMARY") {
			return errors.New("primary key deletion is not supported by this workflow")
		}
		if req.Confirmation != req.Schema+"."+req.Table+"."+req.Name {
			return errors.New("the exact schema.table.index confirmation is required")
		}
		return nil
	default:
		return errors.New("index action must be list, create, rename, or delete")
	}
}

func mysqlIndexTaskCommands(baseDir string, req mysqlIndexTaskRequest) ([]taskdomain.ExecCommandStep, string, error) {
	if err := validateMySQLIndexRequest(req); err != nil {
		return nil, "", err
	}
	client := fmt.Sprintf("%s --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names",
		shellQuote(filepath.Join(baseDir, "bin", "mysql")), mysqlDefaultsFilePlaceholder, req.Port)
	execute := func(sql string) string { return client + " --execute=" + shellQuote(sql) }
	listCommand := mysqlIndexListCommand(client)
	switch req.Action {
	case "list":
		return []taskdomain.ExecCommandStep{{Name: "读取索引与空间信息", Command: listCommand}}, "读取 MySQL 索引清单", nil
	case "create":
		definition := mysqlIndexDefinition(req)
		if req.OnlineWithPT {
			impactSQL := fmt.Sprintf(
				"SELECT CONCAT('GMHA_MYSQL_INDEX_IMPACT\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',ENGINE,'\\t',COALESCE(TABLE_ROWS,0),'\\t',COALESCE(DATA_LENGTH,0),'\\t',COALESCE(INDEX_LENGTH,0),'\\t',%s,'\\t',%s,'\\tPT_ONLINE') FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s; SELECT CONCAT('GMHA_MYSQL_INDEX_PT_GATES\\t',SUM(INDEX_NAME='PRIMARY'),'\\t',(SELECT COUNT(*) FROM information_schema.triggers WHERE EVENT_OBJECT_SCHEMA=%s AND EVENT_OBJECT_TABLE=%s),'\\t',(SELECT COUNT(*) FROM information_schema.referential_constraints WHERE CONSTRAINT_SCHEMA=%s AND REFERENCED_TABLE_NAME=%s)) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s;",
				sqlString(req.Purpose), sqlString(req.Impact), sqlString(req.Schema), sqlString(req.Table),
				sqlString(req.Schema), sqlString(req.Table), sqlString(req.Schema), sqlString(req.Table), sqlString(req.Schema), sqlString(req.Table))
			ptBase := "pt-online-schema-change --defaults-file=" + mysqlDefaultsFilePlaceholder +
				" --host=127.0.0.1" + fmt.Sprintf(" --port=%d", req.Port) +
				" --alter=" + shellQuote("ADD "+definition) +
				" --alter-foreign-keys-method=auto --max-load=Threads_running=25 --critical-load=Threads_running=50" +
				" --max-lag=10 --chunk-time=0.5 --set-vars=lock_wait_timeout=10,innodb_lock_wait_timeout=1" +
				" --progress=percentage,1 --statistics --print " +
				shellQuote("D="+req.Schema+",t="+req.Table)
			verify := fmt.Sprintf("SELECT CONCAT('GMHA_MYSQL_INDEX_VERIFIED\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',INDEX_NAME,'\\t',COUNT(*)) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s GROUP BY TABLE_SCHEMA,TABLE_NAME,INDEX_NAME;",
				sqlString(req.Schema), sqlString(req.Table), sqlString(req.Name))
			return []taskdomain.ExecCommandStep{
				{Name: "检查 PT 工具与在线变更条件", Command: "command -v pt-online-schema-change >/dev/null && pt-online-schema-change --version && " + execute(impactSQL)},
				{Name: "PT 在线变更预演", Command: ptBase + " --dry-run"},
				{Name: "PT 在线复制并切换", Command: ptBase + " --execute"},
				{Name: "核验索引并刷新空间", Command: execute(verify) + " && " + listCommand},
			}, fmt.Sprintf("PT 在线创建索引 %s.%s.%s", req.Schema, req.Table, req.Name), nil
		}
		lockMode := strings.ToUpper(req.LockMode)
		if lockMode == "" {
			lockMode = "NONE"
		}
		algorithm := "INPLACE"
		if req.Kind == "fulltext" || req.Kind == "spatial" {
			algorithm = "DEFAULT"
		}
		impactSQL := fmt.Sprintf(
			"SELECT CONCAT('GMHA_MYSQL_INDEX_IMPACT\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',ENGINE,'\\t',COALESCE(TABLE_ROWS,0),'\\t',COALESCE(DATA_LENGTH,0),'\\t',COALESCE(INDEX_LENGTH,0),'\\t',%s,'\\t',%s,'\\t',%s) FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s; SELECT CONCAT('GMHA_MYSQL_INDEX_LOCK_WAITERS\\t',COUNT(*)) FROM performance_schema.metadata_locks WHERE OBJECT_SCHEMA=%s AND OBJECT_NAME=%s AND LOCK_STATUS='PENDING';",
			sqlString(req.Purpose), sqlString(req.Impact), sqlString(lockMode), sqlString(req.Schema), sqlString(req.Table), sqlString(req.Schema), sqlString(req.Table))
		ddl := fmt.Sprintf("SET SESSION lock_wait_timeout=10; ALTER TABLE %s.%s ADD %s, ALGORITHM=%s, LOCK=%s;",
			sqlIdentifier(req.Schema), sqlIdentifier(req.Table), definition, algorithm, lockMode)
		verify := fmt.Sprintf("SELECT CONCAT('GMHA_MYSQL_INDEX_VERIFIED\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',INDEX_NAME,'\\t',COUNT(*)) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s GROUP BY TABLE_SCHEMA,TABLE_NAME,INDEX_NAME;",
			sqlString(req.Schema), sqlString(req.Table), sqlString(req.Name))
		return []taskdomain.ExecCommandStep{
			{Name: "评估表规模与锁影响", Command: execute(impactSQL)},
			{Name: "创建索引", Command: execute(ddl)},
			{Name: "核验索引并刷新空间", Command: execute(verify) + " && " + listCommand},
		}, fmt.Sprintf("创建索引 %s.%s.%s", req.Schema, req.Table, req.Name), nil
	case "rename":
		ddl := fmt.Sprintf("SET SESSION lock_wait_timeout=10; ALTER TABLE %s.%s RENAME INDEX %s TO %s;",
			sqlIdentifier(req.Schema), sqlIdentifier(req.Table), sqlIdentifier(req.Name), sqlIdentifier(req.NewName))
		verify := fmt.Sprintf("SELECT CONCAT('GMHA_MYSQL_INDEX_VERIFIED\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',INDEX_NAME) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s LIMIT 1;",
			sqlString(req.Schema), sqlString(req.Table), sqlString(req.NewName))
		return []taskdomain.ExecCommandStep{
			{Name: "检查索引目标", Command: execute(mysqlIndexExistsSQL(req.Schema, req.Table, req.Name))},
			{Name: "重命名索引", Command: execute(ddl)},
			{Name: "核验重命名结果", Command: execute(verify)},
		}, fmt.Sprintf("重命名索引 %s.%s.%s", req.Schema, req.Table, req.Name), nil
	case "delete":
		ddl := fmt.Sprintf("SET SESSION lock_wait_timeout=10; ALTER TABLE %s.%s DROP INDEX %s, ALGORITHM=INPLACE, LOCK=NONE;",
			sqlIdentifier(req.Schema), sqlIdentifier(req.Table), sqlIdentifier(req.Name))
		verify := fmt.Sprintf("SELECT CONCAT('GMHA_MYSQL_INDEX_REMOVED\\t',%s,'\\t',%s,'\\t',%s,'\\t',IF(COUNT(*)=0,'YES','NO')) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s;",
			sqlString(req.Schema), sqlString(req.Table), sqlString(req.Name), sqlString(req.Schema), sqlString(req.Table), sqlString(req.Name))
		return []taskdomain.ExecCommandStep{
			{Name: "检查索引与表影响", Command: execute(mysqlIndexExistsSQL(req.Schema, req.Table, req.Name))},
			{Name: "删除索引", Command: execute(ddl)},
			{Name: "核验删除结果", Command: execute(verify)},
		}, fmt.Sprintf("删除索引 %s.%s.%s", req.Schema, req.Table, req.Name), nil
	}
	return nil, "", errors.New("unsupported index action")
}

func normalizeMySQLOnlineDDLRequest(req mysqlOnlineDDLTaskRequest) mysqlOnlineDDLTaskRequest {
	req.Machine = strings.TrimSpace(req.Machine)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Schema = strings.TrimSpace(req.Schema)
	req.Table = strings.TrimSpace(req.Table)
	req.Alter = strings.TrimSpace(req.Alter)
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.Impact = strings.TrimSpace(req.Impact)
	req.AlterForeignKeysMethod = strings.ToLower(strings.TrimSpace(req.AlterForeignKeysMethod))
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	if req.MaxLoadThreadsRunning == 0 {
		req.MaxLoadThreadsRunning = 25
	}
	if req.CriticalThreadsRunning == 0 {
		req.CriticalThreadsRunning = 50
	}
	if req.MaxLagSeconds == 0 {
		req.MaxLagSeconds = 10
	}
	if req.ChunkTimeSeconds == 0 {
		req.ChunkTimeSeconds = 0.5
	}
	if req.CheckIntervalSeconds == 0 {
		req.CheckIntervalSeconds = 1
	}
	if req.AlterForeignKeysMethod == "" {
		req.AlterForeignKeysMethod = "auto"
	}
	return req
}

func validateMySQLOnlineDDLRequest(req mysqlOnlineDDLTaskRequest) error {
	switch req.Action {
	case "dry_run", "execute":
	default:
		return errors.New("online DDL action must be dry_run or execute")
	}
	if !mysqlIndexIdentifierPattern.MatchString(req.Schema) || !mysqlIndexIdentifierPattern.MatchString(req.Table) {
		return errors.New("schema and table must be valid MySQL identifiers")
	}
	if req.Alter == "" || len(req.Alter) > 8*1024 || strings.ContainsRune(req.Alter, '\x00') {
		return errors.New("ALTER clause is required, must be at most 8 KiB, and contain no NUL bytes")
	}
	if strings.HasPrefix(strings.ToUpper(req.Alter), "ALTER TABLE") {
		return errors.New("provide only the ALTER clause, without ALTER TABLE")
	}
	if !mysqlOnlineDDLStartPattern.MatchString(req.Alter) {
		return errors.New("ALTER clause must start with a supported ALTER TABLE operation")
	}
	if mysqlOnlineDDLUnsafePattern.MatchString(req.Alter) {
		return errors.New("ALTER clause contains an unsupported statement separator, comment, lock directive, or table rename")
	}
	if req.Purpose == "" || req.Impact == "" {
		return errors.New("change purpose and expected impact are required")
	}
	if len(req.Purpose) > 500 || len(req.Impact) > 500 || strings.ContainsRune(req.Purpose, '\x00') || strings.ContainsRune(req.Impact, '\x00') {
		return errors.New("change purpose and expected impact must be at most 500 characters and contain no NUL bytes")
	}
	if req.MaxLoadThreadsRunning < 1 || req.MaxLoadThreadsRunning > 10000 {
		return errors.New("max_load_threads_running must be between 1 and 10000")
	}
	if req.CriticalThreadsRunning <= req.MaxLoadThreadsRunning || req.CriticalThreadsRunning > 20000 {
		return errors.New("critical_threads_running must be greater than max_load_threads_running and at most 20000")
	}
	if req.MaxLagSeconds < 1 || req.MaxLagSeconds > 3600 {
		return errors.New("max_lag_seconds must be between 1 and 3600")
	}
	if req.ChunkTimeSeconds < 0.1 || req.ChunkTimeSeconds > 10 {
		return errors.New("chunk_time_seconds must be between 0.1 and 10")
	}
	if req.CheckIntervalSeconds < 1 || req.CheckIntervalSeconds > 60 {
		return errors.New("check_interval_seconds must be between 1 and 60")
	}
	switch req.AlterForeignKeysMethod {
	case "auto", "rebuild_constraints", "drop_swap", "none":
	default:
		return errors.New("alter_foreign_keys_method must be auto, rebuild_constraints, drop_swap, or none")
	}
	if req.Action == "execute" {
		if !req.RiskAcknowledged {
			return errors.New("online DDL risk acknowledgement is required")
		}
		if req.Confirmation != req.Schema+"."+req.Table {
			return errors.New("the exact schema.table confirmation is required")
		}
	}
	return nil
}

func mysqlOnlineDDLTaskCommands(baseDir string, input mysqlOnlineDDLTaskRequest) ([]taskdomain.ExecCommandStep, string, error) {
	req := normalizeMySQLOnlineDDLRequest(input)
	if err := validateMySQLOnlineDDLRequest(req); err != nil {
		return nil, "", err
	}
	client := fmt.Sprintf("%s --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names",
		shellQuote(filepath.Join(baseDir, "bin", "mysql")), mysqlDefaultsFilePlaceholder, req.Port)
	executeSQL := func(sql string) string { return client + " --execute=" + shellQuote(sql) }
	targetSQL := fmt.Sprintf(
		"SELECT CONCAT('GMHA_ONLINE_DDL_TARGET\\t',t.TABLE_SCHEMA,'\\t',t.TABLE_NAME,'\\t',t.ENGINE,'\\t',COALESCE(t.TABLE_ROWS,0),'\\t',COALESCE(t.DATA_LENGTH,0),'\\t',COALESCE(t.INDEX_LENGTH,0),'\\t',(SELECT COUNT(*) FROM information_schema.statistics s WHERE s.TABLE_SCHEMA=t.TABLE_SCHEMA AND s.TABLE_NAME=t.TABLE_NAME AND s.NON_UNIQUE=0),'\\t',(SELECT COUNT(*) FROM information_schema.triggers tr WHERE tr.EVENT_OBJECT_SCHEMA=t.TABLE_SCHEMA AND tr.EVENT_OBJECT_TABLE=t.TABLE_NAME),'\\t',(SELECT COUNT(*) FROM information_schema.referential_constraints rc WHERE rc.CONSTRAINT_SCHEMA=t.TABLE_SCHEMA AND (rc.TABLE_NAME=t.TABLE_NAME OR rc.REFERENCED_TABLE_NAME=t.TABLE_NAME)),'\\t',%s,'\\t',%s) FROM information_schema.tables t WHERE t.TABLE_SCHEMA=%s AND t.TABLE_NAME=%s AND t.TABLE_TYPE='BASE TABLE'; SELECT CONCAT('GMHA_ONLINE_DDL_LOAD\\t',@@version,'\\t',@@global.binlog_format,'\\t',@@global.read_only,'\\t',(SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_running'),'\\t',(SELECT COUNT(*) FROM information_schema.innodb_trx));",
		sqlString(req.Purpose), sqlString(req.Impact), sqlString(req.Schema), sqlString(req.Table))
	ptBase := "pt-online-schema-change --defaults-file=" + mysqlDefaultsFilePlaceholder +
		" --host=127.0.0.1" + fmt.Sprintf(" --port=%d", req.Port) +
		" --alter=" + shellQuote(req.Alter) +
		fmt.Sprintf(" --max-load=Threads_running=%d --critical-load=Threads_running=%d", req.MaxLoadThreadsRunning, req.CriticalThreadsRunning) +
		fmt.Sprintf(" --max-lag=%d --chunk-time=%s --check-interval=%d", req.MaxLagSeconds, strconv.FormatFloat(req.ChunkTimeSeconds, 'f', -1, 64), req.CheckIntervalSeconds) +
		" --alter-foreign-keys-method=" + req.AlterForeignKeysMethod +
		" --set-vars=lock_wait_timeout=10,innodb_lock_wait_timeout=1" +
		" --progress=percentage,1 --statistics --print " +
		shellQuote("D="+req.Schema+",t="+req.Table)
	precheck := taskdomain.ExecCommandStep{
		Name:    "检查 PT 工具、目标表与运行负载",
		Command: "command -v pt-online-schema-change >/dev/null && pt-online-schema-change --version && " + executeSQL(targetSQL),
	}
	dryRun := taskdomain.ExecCommandStep{Name: "PT 在线 DDL 预演", Command: ptBase + " --dry-run"}
	if req.Action == "dry_run" {
		return []taskdomain.ExecCommandStep{precheck, dryRun}, fmt.Sprintf("PT 在线 DDL 预检 %s.%s", req.Schema, req.Table), nil
	}
	verifySQL := fmt.Sprintf(
		"SELECT CONCAT('GMHA_ONLINE_DDL_VERIFIED\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',ENGINE,'\\t',COALESCE(TABLE_ROWS,0),'\\t',COALESCE(DATA_LENGTH,0),'\\t',COALESCE(INDEX_LENGTH,0)) FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND TABLE_TYPE='BASE TABLE'; SHOW CREATE TABLE %s.%s;",
		sqlString(req.Schema), sqlString(req.Table), sqlIdentifier(req.Schema), sqlIdentifier(req.Table))
	return []taskdomain.ExecCommandStep{
		precheck,
		dryRun,
		{Name: "PT 在线复制与原子切换", Command: ptBase + " --execute"},
		{Name: "核验变更后表结构", Command: executeSQL(verifySQL)},
	}, fmt.Sprintf("PT 在线 DDL %s.%s", req.Schema, req.Table), nil
}

func normalizeMySQLArchiveRequest(req mysqlArchiveTaskRequest) mysqlArchiveTaskRequest {
	req.Machine = strings.TrimSpace(req.Machine)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.SourceSchema = strings.TrimSpace(req.SourceSchema)
	req.SourceTable = strings.TrimSpace(req.SourceTable)
	req.DestinationSchema = strings.TrimSpace(req.DestinationSchema)
	req.DestinationTable = strings.TrimSpace(req.DestinationTable)
	req.Where = strings.TrimSpace(req.Where)
	req.Index = strings.TrimSpace(req.Index)
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	if req.BatchSize == 0 {
		req.BatchSize = 1000
	}
	return req
}

func mysqlArchiveConfirmation(req mysqlArchiveTaskRequest) string {
	return req.SourceSchema + "." + req.SourceTable + "->" + req.DestinationSchema + "." + req.DestinationTable
}

func validateMySQLArchiveRequest(req mysqlArchiveTaskRequest) error {
	switch req.Action {
	case "dry_run", "execute":
	default:
		return errors.New("archive action must be dry_run or execute")
	}
	for name, identifier := range map[string]string{
		"source_schema": req.SourceSchema, "source_table": req.SourceTable,
		"destination_schema": req.DestinationSchema, "destination_table": req.DestinationTable,
	} {
		if !mysqlIndexIdentifierPattern.MatchString(identifier) {
			return fmt.Errorf("%s must be a valid MySQL identifier", name)
		}
	}
	if req.SourceSchema == req.DestinationSchema && req.SourceTable == req.DestinationTable {
		return errors.New("archive destination must be different from the source table")
	}
	if req.Index != "" && !mysqlIndexIdentifierPattern.MatchString(req.Index) {
		return errors.New("index must be a valid MySQL identifier")
	}
	if req.Where == "" || len(req.Where) > 2000 || strings.ContainsAny(req.Where, "\r\n\x00") {
		return errors.New("where is required, must be at most 2000 characters, and contain no line breaks or NUL bytes")
	}
	if strings.EqualFold(strings.ReplaceAll(req.Where, " ", ""), "1=1") {
		return errors.New("where must select a bounded archive set; 1=1 is not allowed")
	}
	if mysqlArchiveUnsafeWherePattern.MatchString(req.Where) {
		return errors.New("where contains a statement separator, comment, or data-changing SQL keyword")
	}
	if req.BatchSize < 1 || req.BatchSize > 100000 {
		return errors.New("batch_size must be between 1 and 100000")
	}
	if req.SleepSeconds < 0 || req.SleepSeconds > 60 {
		return errors.New("sleep_seconds must be between 0 and 60")
	}
	if req.RunTimeSeconds < 0 || req.RunTimeSeconds > 86400 {
		return errors.New("run_time_seconds must be between 0 and 86400")
	}
	if req.Action == "execute" {
		if !req.RiskAcknowledged {
			return errors.New("archive risk acknowledgement is required")
		}
		if req.Confirmation != mysqlArchiveConfirmation(req) {
			return errors.New("the exact source->destination confirmation is required")
		}
	}
	return nil
}

func mysqlArchiveTaskCommands(baseDir string, input mysqlArchiveTaskRequest) ([]taskdomain.ExecCommandStep, string, error) {
	req := normalizeMySQLArchiveRequest(input)
	if err := validateMySQLArchiveRequest(req); err != nil {
		return nil, "", err
	}
	client := fmt.Sprintf("%s --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names",
		shellQuote(filepath.Join(baseDir, "bin", "mysql")), mysqlDefaultsFilePlaceholder, req.Port)
	executeSQL := func(sql string) string { return client + " --execute=" + shellQuote(sql) }
	sourceTable := sqlIdentifier(req.SourceSchema) + "." + sqlIdentifier(req.SourceTable)
	destinationTable := sqlIdentifier(req.DestinationSchema) + "." + sqlIdentifier(req.DestinationTable)
	sourceIndexHint := ""
	if req.Index != "" {
		sourceIndexHint = " FORCE INDEX (" + sqlIdentifier(req.Index) + ")"
	}
	sourceDSN := fmt.Sprintf("F=%s,h=127.0.0.1,P=%d,D=%s,t=%s", mysqlDefaultsFilePlaceholder, req.Port, req.SourceSchema, req.SourceTable)
	if req.Index != "" {
		sourceDSN += ",i=" + req.Index
	}
	destinationDSN := fmt.Sprintf("F=%s,h=127.0.0.1,P=%d,D=%s,t=%s", mysqlDefaultsFilePlaceholder, req.Port, req.DestinationSchema, req.DestinationTable)
	ptBase := "pt-archiver --source " + sourceDSN +
		" --dest " + destinationDSN +
		" --where " + shellQuote(req.Where) +
		fmt.Sprintf(" --limit=%d --commit-each --sleep=%d --progress=%d", req.BatchSize, req.SleepSeconds, req.BatchSize) +
		" --retries=3 --statistics --why-quit --set-vars=lock_wait_timeout=10,innodb_lock_wait_timeout=1"
	if req.RunTimeSeconds > 0 {
		ptBase += fmt.Sprintf(" --run-time=%ds", req.RunTimeSeconds)
	}
	if !req.DeleteSource {
		ptBase += " --no-delete"
	}
	precheckSQL := fmt.Sprintf(
		"SELECT CONCAT('GMHA_ARCHIVE_SOURCE\\t',%s,'\\t',%s,'\\t',COUNT(*),'\\t',IF(COUNT(*)>=100000,'YES','NO')) FROM (SELECT 1 AS candidate FROM %s%s WHERE %s LIMIT 100000) gmha_archive_candidates; "+
			"SELECT CONCAT('GMHA_ARCHIVE_TABLE\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',ENGINE,'\\t',COALESCE(TABLE_ROWS,0),'\\t',COALESCE(DATA_LENGTH,0),'\\t',COALESCE(INDEX_LENGTH,0),'\\t',(SELECT COUNT(*) FROM information_schema.statistics s WHERE s.TABLE_SCHEMA=t.TABLE_SCHEMA AND s.TABLE_NAME=t.TABLE_NAME AND s.INDEX_NAME='PRIMARY')) FROM information_schema.tables t WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND TABLE_TYPE='BASE TABLE'; "+
			"SELECT CONCAT('GMHA_ARCHIVE_DESTINATION\\t',%s,'\\t',%s,'\\t',COUNT(*)) FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND TABLE_TYPE='BASE TABLE'; "+
			"EXPLAIN SELECT * FROM %s%s WHERE %s LIMIT %d;",
		sqlString(req.SourceSchema), sqlString(req.SourceTable), sourceTable, sourceIndexHint, req.Where,
		sqlString(req.SourceSchema), sqlString(req.SourceTable),
		sqlString(req.DestinationSchema), sqlString(req.DestinationTable), sqlString(req.DestinationSchema), sqlString(req.DestinationTable),
		sourceTable, sourceIndexHint, req.Where, req.BatchSize)
	precheck := taskdomain.ExecCommandStep{
		Name:    "检查 PT 工具、源数据与归档目标",
		Command: "command -v pt-archiver >/dev/null && pt-archiver --version && " + executeSQL(precheckSQL),
	}
	destinationExistsSQL := fmt.Sprintf(
		"SELECT COUNT(*) FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND TABLE_TYPE='BASE TABLE';",
		sqlString(req.DestinationSchema), sqlString(req.DestinationTable))
	dryRunCommand := "if [ \"$(" + executeSQL(destinationExistsSQL) + ")\" = \"1\" ]; then " + ptBase + " --dry-run; else echo " +
		shellQuote("GMHA_ARCHIVE_DESTINATION_MISSING\t"+req.DestinationSchema+"\t"+req.DestinationTable+"\t正式执行时将按源表结构自动创建") + "; fi"
	dryRun := taskdomain.ExecCommandStep{Name: "PT 归档预演", Command: dryRunCommand}
	if req.Action == "dry_run" {
		return []taskdomain.ExecCommandStep{precheck, dryRun},
			fmt.Sprintf("PT 归档预检 %s.%s", req.SourceSchema, req.SourceTable), nil
	}
	prepareSQL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s LIKE %s; SELECT CONCAT('GMHA_ARCHIVE_DESTINATION_READY\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',ENGINE) FROM information_schema.tables WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND TABLE_TYPE='BASE TABLE';",
		destinationTable, sourceTable, sqlString(req.DestinationSchema), sqlString(req.DestinationTable))
	verifySQL := fmt.Sprintf(
		"SELECT CONCAT('GMHA_ARCHIVE_REMAINING\\t',%s,'\\t',%s,'\\t',COUNT(*)) FROM %s WHERE %s; "+
			"SELECT CONCAT('GMHA_ARCHIVE_DESTINATION_ROWS\\t',%s,'\\t',%s,'\\t',COUNT(*)) FROM %s;",
		sqlString(req.SourceSchema), sqlString(req.SourceTable), sourceTable, req.Where,
		sqlString(req.DestinationSchema), sqlString(req.DestinationTable), destinationTable)
	mode := "复制"
	if req.DeleteSource {
		mode = "迁移"
	}
	return []taskdomain.ExecCommandStep{
		precheck,
		{Name: "准备并核验归档表", Command: executeSQL(prepareSQL)},
		{Name: "PT 归档预演", Command: ptBase + " --dry-run"},
		{Name: "PT 分批" + mode + "归档数据", Command: ptBase},
		{Name: "核验源表与归档表数据", Command: executeSQL(verifySQL)},
	}, fmt.Sprintf("PT 数据归档 %s.%s → %s.%s", req.SourceSchema, req.SourceTable, req.DestinationSchema, req.DestinationTable), nil
}

func mysqlIndexDefinition(req mysqlIndexTaskRequest) string {
	columns := make([]string, 0, len(req.Columns))
	for _, item := range req.Columns {
		column := sqlIdentifier(strings.TrimSpace(item.Name))
		if item.PrefixLen > 0 {
			column += fmt.Sprintf("(%d)", item.PrefixLen)
		}
		if direction := strings.ToUpper(strings.TrimSpace(item.Direction)); direction != "" {
			column += " " + direction
		}
		columns = append(columns, column)
	}
	prefix, suffix := "INDEX ", " USING BTREE"
	switch req.Kind {
	case "unique":
		prefix = "UNIQUE INDEX "
	case "fulltext":
		prefix, suffix = "FULLTEXT INDEX ", ""
	case "spatial":
		prefix, suffix = "SPATIAL INDEX ", ""
	}
	return prefix + sqlIdentifier(req.Name) + " (" + strings.Join(columns, ", ") + ")" + suffix
}

func mysqlIndexExistsSQL(schema, table, name string) string {
	return fmt.Sprintf("SELECT CONCAT('GMHA_MYSQL_INDEX_TARGET\\t',TABLE_SCHEMA,'\\t',TABLE_NAME,'\\t',INDEX_NAME,'\\t',INDEX_TYPE,'\\t',IF(NON_UNIQUE=0,'UNIQUE','NON_UNIQUE'),'\\t',GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX)) FROM information_schema.statistics WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s GROUP BY TABLE_SCHEMA,TABLE_NAME,INDEX_NAME,INDEX_TYPE,NON_UNIQUE;",
		sqlString(schema), sqlString(table), sqlString(name))
}

func mysqlIndexListCommand(client string) string {
	// mysql.innodb_index_stats reports index pages. The LEFT JOIN deliberately
	// keeps non-InnoDB indexes visible, with an unknown (zero) byte estimate.
	query := "SELECT CONCAT('GMHA_MYSQL_INDEX\\t',s.TABLE_SCHEMA,'\\t',s.TABLE_NAME,'\\t',s.INDEX_NAME,'\\t',s.INDEX_TYPE,'\\t',IF(s.NON_UNIQUE=0,'YES','NO'),'\\t',GROUP_CONCAT(CONCAT(COALESCE(s.COLUMN_NAME,'(expression)'),IF(s.SUB_PART IS NULL,'',CONCAT('(',s.SUB_PART,')'))) ORDER BY s.SEQ_IN_INDEX SEPARATOR ','),'\\t',COALESCE(MAX(p.bytes),0),'\\t',COALESCE(MAX(t.TABLE_ROWS),0),'\\t',COALESCE(MAX(t.DATA_LENGTH),0),'\\t',COALESCE(MAX(t.INDEX_LENGTH),0)) FROM information_schema.statistics s JOIN information_schema.tables t ON t.TABLE_SCHEMA=s.TABLE_SCHEMA AND t.TABLE_NAME=s.TABLE_NAME LEFT JOIN (SELECT database_name,table_name,index_name,MAX(CASE WHEN stat_name='size' THEN stat_value END)*@@innodb_page_size AS bytes FROM mysql.innodb_index_stats GROUP BY database_name,table_name,index_name) p ON p.database_name=s.TABLE_SCHEMA AND p.table_name=s.TABLE_NAME AND p.index_name=s.INDEX_NAME WHERE s.TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') GROUP BY s.TABLE_SCHEMA,s.TABLE_NAME,s.INDEX_NAME,s.INDEX_TYPE,s.NON_UNIQUE ORDER BY s.TABLE_SCHEMA,s.TABLE_NAME,s.INDEX_NAME;"
	return client + " --execute=" + shellQuote(query)
}

func sqlIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func clusterAutomationCommand(req clusterAutomationRequest, instances ...mysqlapp.Instance) (string, error) {
	if req.Operation == "shell" {
		return req.Script, nil
	}
	var instance mysqlapp.Instance
	if len(instances) > 0 {
		instance = instances[0]
	}
	client := mysqlAutomationClient(instance, req.Port)
	version := strings.TrimSpace(instance.Version)
	if version == "" && strings.TrimSpace(instance.PackageName) != "" {
		version, _ = mysqlapp.PackageVersion(instance.PackageName)
	}
	if req.Operation == "collect_mysql" {
		sql := "SET @gmha_questions_before=(SELECT VARIABLE_VALUE+0 FROM performance_schema.global_status WHERE VARIABLE_NAME='Questions'); " +
			"SET @gmha_commit_before=(SELECT VARIABLE_VALUE+0 FROM performance_schema.global_status WHERE VARIABLE_NAME='Com_commit'); " +
			"SET @gmha_rollback_before=(SELECT VARIABLE_VALUE+0 FROM performance_schema.global_status WHERE VARIABLE_NAME='Com_rollback'); DO SLEEP(1); " +
			"SELECT CONCAT('GMHA_MYSQL_INSTANCE\\t', @@hostname, '\\t', @@version, '\\t', @@port); " +
			"SELECT CONCAT('GMHA_MYSQL_RATE\\tQPS\\t', GREATEST(VARIABLE_VALUE+0-@gmha_questions_before,0)) FROM performance_schema.global_status WHERE VARIABLE_NAME='Questions'; " +
			"SELECT CONCAT('GMHA_MYSQL_RATE\\tTPS\\t', GREATEST(SUM(CASE WHEN VARIABLE_NAME='Com_commit' THEN VARIABLE_VALUE+0 ELSE 0 END)-@gmha_commit_before+SUM(CASE WHEN VARIABLE_NAME='Com_rollback' THEN VARIABLE_VALUE+0 ELSE 0 END)-@gmha_rollback_before,0)) FROM performance_schema.global_status WHERE VARIABLE_NAME IN ('Com_commit','Com_rollback'); " +
			"SELECT CONCAT('GMHA_MYSQL_STATUS\\t', Variable_name, '\\t', Variable_value) FROM performance_schema.global_status " +
			"WHERE Variable_name IN ('Threads_connected','Threads_running','Questions','Queries','Com_select','Com_insert','Com_update','Com_delete','Slow_queries','Uptime') ORDER BY Variable_name;"
		return client + " --execute=" + shellQuote(sql), nil
	}
	if req.Operation == "database_inspection" || req.Operation == "database_deep_inspection" {
		return databaseInspectionCommand(client, req.Operation == "database_deep_inspection"), nil
	}
	if req.Operation == "mysql_user" {
		if version != "" {
			if err := validateMySQLUserPrivilegesForVersion(req.Privileges, version); err != nil {
				return "", err
			}
		}
		sql, err := mysqlUserSQL(req.UserAction, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges)
		if err != nil {
			return "", err
		}
		return client + " --execute=" + shellQuote(sql), nil
	}
	if req.Operation == "mysql_parameter" {
		configPath := strings.TrimSpace(req.ConfigPath)
		if configPath == "" {
			configPath = strings.TrimSpace(instance.MyCnfPath)
		}
		unit := strings.TrimSpace(req.SystemdUnit)
		if unit == "" {
			unit = strings.TrimSuffix(strings.TrimSpace(instance.SystemdUnit), ".service")
		}
		applyMode, restart := req.ApplyMode, false
		if applyMode == "restart" {
			applyMode, restart = "config", true
		}
		mysqldPath := ""
		if strings.TrimSpace(instance.BaseDir) != "" {
			mysqldPath = filepath.Join(instance.BaseDir, "bin", "mysqld")
		}
		command, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{
			Action: "update", Name: req.ParameterName, Value: req.ParameterValue, ApplyMode: applyMode,
			ConfigPath: configPath, SystemdUnit: unit, Port: req.Port,
			MySQLDPath: mysqldPath, Version: version, Restart: restart,
		})
		return command, err
	}
	return "", errors.New("unsupported automation operation")
}

func clusterAutomationArtifactDir() string {
	return filepath.Join(os.TempDir(), "gmha", "cluster-reports")
}

func mysqlAutomationClient(instance mysqlapp.Instance, port int) string {
	binary := "mysql"
	if strings.TrimSpace(instance.BaseDir) != "" {
		binary = filepath.Join(instance.BaseDir, "bin", "mysql")
	}
	return fmt.Sprintf("%s --defaults-extra-file=%s --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", shellQuote(binary), mysqlDefaultsFilePlaceholder, port)
}

func sqlString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func shellQuote(value string) string {
	// Close the single-quoted string, emit one literal quote from a
	// double-quoted string, then reopen the single-quoted string. Do not add
	// backslashes here: they would become part of the SQL passed to mysql.
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func normalizePrivileges(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.ToUpper(strings.TrimSpace(item)); item != "" {
			result = append(result, item)
		}
	}
	return result
}

// HandleClusterAutomationReport renders task events into a downloadable text
// report. Reports include stdout/stderr persisted by Agent exec tasks, so an
// operator can archive a run after it completes.
func (h *TaskHandler) HandleClusterAutomationReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ids := strings.Split(strings.TrimSpace(r.URL.Query().Get("task_ids")), ",")
	if len(ids) == 0 || strings.TrimSpace(ids[0]) == "" {
		writeError(w, http.StatusBadRequest, os.ErrInvalid)
		return
	}
	var report strings.Builder
	report.WriteString("GMHA cluster automation report\n")
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		detail, err := h.service.GetTaskDetail(r.Context(), id)
		if err != nil {
			report.WriteString("\nTASK " + id + "\nERROR: " + err.Error() + "\n")
			continue
		}
		report.WriteString("\nTASK " + detail.Task.ID + "\n")
		report.WriteString("machine=" + detail.MachineName + " ip=" + detail.MachineIP + " status=" + string(detail.Task.Status) + "\n")
		for _, event := range detail.Events {
			report.WriteString("[" + string(event.EventType) + "] " + event.Content + "\n")
		}
	}
	contents := []byte(report.String())
	artifactName := fmt.Sprintf("cluster-automation-%d.txt", time.Now().UTC().UnixNano())
	artifactDir := clusterAutomationArtifactDir()
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(filepath.Join(artifactDir, artifactName), contents, 0o640); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+artifactName)
	w.Header().Set("X-GMHA-Report-Artifact", artifactName)
	_, _ = w.Write(contents)
}

// HandleClusterAutomationArtifact serves a previously generated automation
// report. The file name is deliberately restricted to a base name so report
// retrieval cannot escape the dedicated artifact directory.
func (h *TaskHandler) HandleClusterAutomationArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/cluster-automation/artifacts/"))
	if name == "" || filepath.Base(name) != name || !strings.HasPrefix(name, "cluster-automation-") || !strings.HasSuffix(name, ".txt") {
		writeError(w, http.StatusBadRequest, os.ErrInvalid)
		return
	}
	contents, err := os.ReadFile(filepath.Join(clusterAutomationArtifactDir(), name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	_, _ = w.Write(contents)
}

// HandleCreateMySQLInstallTask 处理创建 MySQL 安装任务请求。
func (h *TaskHandler) HandleCreateMySQLInstallTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createMySQLInstallTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateMySQLInstallTask(r.Context(), taskusecase.CreateMySQLInstallTaskRequest{
		Machine:           req.Machine,
		Port:              req.Port,
		ServerID:          req.ServerID,
		MySQLUser:         req.MySQLUser,
		InstanceDir:       req.InstanceDir,
		DataDir:           req.DataDir,
		BinlogDir:         req.BinlogDir,
		RedoDir:           req.RedoDir,
		UndoDir:           req.UndoDir,
		TmpDir:            req.TmpDir,
		BaseDir:           req.BaseDir,
		MyCnfPath:         req.MyCnfPath,
		SocketPath:        req.SocketPath,
		ErrorLog:          req.ErrorLog,
		PIDFile:           req.PIDFile,
		CharacterSetsDir:  req.CharacterSetsDir,
		PluginDir:         req.PluginDir,
		RootPassword:      req.RootPassword,
		Profile:           req.Profile,
		PackageName:       req.PackageName,
		Version:           req.Version,
		Architecture:      req.Architecture,
		InstallPTTools:    req.InstallPTTools,
		InstallXtraBackup: req.InstallXtraBackup,
		MemoryAllocator:   req.MemoryAllocator,
		RuntimeParameters: req.RuntimeParameters,
		Accounts:          mysqlAccountRequests(req.Accounts),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// HandleMySQLUsers creates an auditable single-instance task that uses the MHA
// credential already stored by the Agent for the selected MySQL port.
func (h *TaskHandler) HandleMySQLUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlUserTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Machine = strings.TrimSpace(req.Machine)
	req.Action = strings.TrimSpace(req.Action)
	if req.Machine == "" || req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("machine and valid port are required"))
		return
	}
	if err := validateMySQLUserAction(req.Action, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	machine, instance, err := h.service.ResolveMySQLInstance(r.Context(), req.Machine, req.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	version := strings.TrimSpace(instance.Version)
	if version == "" {
		version, _ = mysqlapp.PackageVersion(instance.PackageName)
	}
	if err := validateMySQLUserPrivilegesForVersion(req.Privileges, version); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	command, err := mysqlUserTaskCommand(instance.BaseDir, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	opts, _ := databaseAutomationTaskOptions(clusterAutomationRequest{Operation: "mysql_user", UserAction: req.Action, Port: req.Port})
	detail, err := h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, command, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	go func(taskID string) {
		finished, waitErr := h.service.WaitForTask(context.Background(), taskID, 5*time.Minute)
		if waitErr == nil && (finished.Task.Status == taskdomain.StatusSuccess || finished.Task.Status == taskdomain.StatusFailed) {
			_ = h.service.RedactExecTaskCommand(context.Background(), taskID)
		}
	}(detail.Task.ID)
	writeJSON(w, http.StatusOK, detail)
}

// HandleMySQLIndexes exposes index discovery and DDL as auditable Agent tasks.
// The Agent injects the registered MHA credential into a temporary 0600
// defaults file, so database credentials never cross the browser API.
func (h *TaskHandler) HandleMySQLIndexes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlIndexTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Machine = strings.TrimSpace(req.Machine)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Schema = strings.TrimSpace(req.Schema)
	req.Table = strings.TrimSpace(req.Table)
	req.Name = strings.TrimSpace(req.Name)
	req.NewName = strings.TrimSpace(req.NewName)
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	req.LockMode = strings.ToLower(strings.TrimSpace(req.LockMode))
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.Impact = strings.TrimSpace(req.Impact)
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	if req.Machine == "" || req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("machine and valid port are required"))
		return
	}
	if err := validateMySQLIndexRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	machine, instance, err := h.service.ResolveMySQLInstance(r.Context(), req.Machine, req.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	commands, displayName, err := mysqlIndexTaskCommands(instance.BaseDir, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	detail, err := h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, "", app.ExecTaskOptions{
		Operation:   "mysql_index_" + req.Action,
		DisplayName: displayName,
		Port:        req.Port,
		Commands:    commands,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// HandleMySQLOnlineDDL creates a controlled pt-online-schema-change workflow.
// A dry-run and a real execution are separate auditable tasks; real execution
// additionally requires an explicit risk acknowledgement and exact target.
func (h *TaskHandler) HandleMySQLOnlineDDL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlOnlineDDLTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req = normalizeMySQLOnlineDDLRequest(req)
	if req.Machine == "" || req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("machine and valid port are required"))
		return
	}
	if err := validateMySQLOnlineDDLRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	machine, instance, err := h.service.ResolveMySQLInstance(r.Context(), req.Machine, req.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	commands, displayName, err := mysqlOnlineDDLTaskCommands(instance.BaseDir, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	detail, err := h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, "", app.ExecTaskOptions{
		Operation:   "mysql_online_ddl_" + req.Action,
		DisplayName: displayName,
		Port:        req.Port,
		Commands:    commands,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// HandleMySQLArchive creates a controlled pt-archiver workflow. Preview and
// execution are separate tasks, and source-row deletion requires an exact
// source-to-destination confirmation in addition to the risk acknowledgement.
func (h *TaskHandler) HandleMySQLArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlArchiveTaskRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req = normalizeMySQLArchiveRequest(req)
	if req.Machine == "" || req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("machine and valid port are required"))
		return
	}
	if err := validateMySQLArchiveRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	machine, instance, err := h.service.ResolveMySQLInstance(r.Context(), req.Machine, req.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if compatible, reason := h.service.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
		writeError(w, http.StatusConflict, fmt.Errorf("Agent does not support secure MySQL credential injection: %s", reason))
		return
	}
	commands, displayName, err := mysqlArchiveTaskCommands(instance.BaseDir, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	detail, err := h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, "", app.ExecTaskOptions{
		Operation:   "mysql_archive_" + req.Action,
		DisplayName: displayName,
		Port:        req.Port,
		Commands:    commands,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// HandleMySQLParameters creates auditable tasks for collecting every runtime
// variable and for applying or deleting an instance-specific override.
func (h *TaskHandler) HandleMySQLParameters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlParameterTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	if req.Action == "collect" {
		target := mysqlParameterTargetRequest{Machine: req.Machine, Port: req.Port, ConfigPath: req.ConfigPath, SystemdUnit: req.SystemdUnit}
		detail, err := h.createMySQLParameterTask(r.Context(), target, nil, false, "collect")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}

	changes := req.Changes
	if len(changes) == 0 && (req.Action == "update" || req.Action == "delete") {
		changes = []mysqlParameterChangeRequest{{Action: req.Action, Name: req.Name, Value: req.Value}}
	}
	if len(changes) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one parameter change is required"))
		return
	}
	targets := req.Targets
	if len(targets) == 0 {
		targets = []mysqlParameterTargetRequest{{Machine: req.Machine, Port: req.Port, ConfigPath: req.ConfigPath, SystemdUnit: req.SystemdUnit}}
	}
	for i := range changes {
		changes[i].Action = strings.TrimSpace(changes[i].Action)
		changes[i].Name = strings.ToLower(strings.TrimSpace(changes[i].Name))
		if changes[i].Action != "update" && changes[i].Action != "delete" {
			writeError(w, http.StatusBadRequest, errors.New("parameter action must be update or delete"))
			return
		}
		if !mysqlParameterPattern.MatchString(changes[i].Name) {
			writeError(w, http.StatusBadRequest, errors.New("invalid MySQL parameter name"))
			return
		}
		if changes[i].Action == "update" && (strings.TrimSpace(changes[i].Value) == "" || strings.ContainsAny(changes[i].Value, "\r\n\x00")) {
			writeError(w, http.StatusBadRequest, errors.New("parameter value is required and must be a single line"))
			return
		}
	}
	requiresRestart := false
	for _, change := range changes {
		if !mysqlParameterIsDynamic(change.Name) {
			requiresRestart = true
			break
		}
	}
	if requiresRestart && len(req.RestartTargets) == 0 {
		writeError(w, http.StatusConflict, errors.New("restart-required parameters need an explicit restart scope"))
		return
	}
	if len(req.RestartTargets) > 0 && !req.RestartConfirmed {
		writeError(w, http.StatusConflict, errors.New("restart confirmation is required"))
		return
	}

	type plannedTarget struct {
		target  mysqlParameterTargetRequest
		restart bool
	}
	plans := make([]plannedTarget, 0, len(targets)+len(req.RestartTargets))
	byKey := map[string]int{}
	for _, target := range targets {
		key := strings.TrimSpace(target.Machine) + ":" + strconv.Itoa(target.Port)
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = len(plans)
		plans = append(plans, plannedTarget{target: target})
	}
	for _, target := range req.RestartTargets {
		key := strings.TrimSpace(target.Machine) + ":" + strconv.Itoa(target.Port)
		if index, exists := byKey[key]; exists {
			plans[index].restart = true
			continue
		}
		byKey[key] = len(plans)
		plans = append(plans, plannedTarget{target: target, restart: true})
	}
	if len(plans) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("at least one target instance is required"))
		return
	}

	var parent app.TaskDetail
	var err error
	if len(plans) > 1 {
		parent, err = h.service.CreateBatchTrackingTask(r.Context(), "mysql_parameters_batch", fmt.Sprintf("批量修改 %d 项 MySQL 参数", len(changes)), fmt.Sprintf("%d 个实例", len(plans)))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	tasks := make([]app.TaskDetail, 0, len(plans))
	taskIDs := make([]string, 0, len(plans))
	if len(req.RestartTargets) > 1 && parent.Task.ID != "" {
		parentID := parent.Task.ID
		go func() {
			created, failed := 0, 0
			for index, plan := range plans {
				planChanges := changes
				key := strings.TrimSpace(plan.target.Machine) + ":" + strconv.Itoa(plan.target.Port)
				isChangeTarget := false
				for _, target := range targets {
					if key == strings.TrimSpace(target.Machine)+":"+strconv.Itoa(target.Port) {
						isChangeTarget = true
						break
					}
				}
				if !isChangeTarget {
					planChanges = nil
				}
				detail, createErr := h.createMySQLParameterTask(context.Background(), plan.target, planChanges, plan.restart, "apply")
				if createErr != nil {
					failed += len(plans) - index
					break
				}
				created++
				_ = h.service.AttachChildTasks(context.Background(), parentID, []string{detail.Task.ID})
				if plan.restart {
					finished, waitErr := h.service.WaitForTask(context.Background(), detail.Task.ID, 10*time.Minute)
					if waitErr != nil || finished.Task.Status != taskdomain.StatusSuccess {
						failed += len(plans) - index - 1
						break
					}
				}
			}
			_ = h.service.FinalizeBatchTrackingTask(context.Background(), parentID, created, failed)
		}()
		dynamicCount := 0
		for _, change := range changes {
			if mysqlParameterIsDynamic(change.Name) {
				dynamicCount++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"parent": parent, "tasks": tasks, "requires_restart": requiresRestart, "dynamic_count": dynamicCount, "restart_count": len(changes) - dynamicCount, "restart_mode": "rolling"})
		return
	}
	for _, plan := range plans {
		planChanges := changes
		key := strings.TrimSpace(plan.target.Machine) + ":" + strconv.Itoa(plan.target.Port)
		isChangeTarget := false
		for _, target := range targets {
			if key == strings.TrimSpace(target.Machine)+":"+strconv.Itoa(target.Port) {
				isChangeTarget = true
				break
			}
		}
		if !isChangeTarget {
			planChanges = nil
		}
		detail, createErr := h.createMySQLParameterTask(r.Context(), plan.target, planChanges, plan.restart, "apply")
		if createErr != nil {
			if parent.Task.ID != "" {
				_ = h.service.FinalizeBatchTrackingTask(r.Context(), parent.Task.ID, len(tasks), len(plans)-len(tasks))
			}
			writeError(w, http.StatusBadRequest, createErr)
			return
		}
		if parent.Task.ID != "" {
			detail.Task.ParentTaskID = parent.Task.ID
		}
		tasks = append(tasks, detail)
		taskIDs = append(taskIDs, detail.Task.ID)
	}
	if parent.Task.ID != "" {
		if err := h.service.AttachChildTasks(r.Context(), parent.Task.ID, taskIDs); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		_ = h.service.FinalizeBatchTrackingTask(r.Context(), parent.Task.ID, len(tasks), 0)
		parent, _ = h.service.GetTaskDetail(r.Context(), parent.Task.ID)
	}
	dynamicCount := 0
	for _, change := range changes {
		if mysqlParameterIsDynamic(change.Name) {
			dynamicCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"parent": parent, "tasks": tasks, "requires_restart": requiresRestart, "dynamic_count": dynamicCount, "restart_count": len(changes) - dynamicCount})
}

func (h *TaskHandler) createMySQLParameterTask(ctx context.Context, target mysqlParameterTargetRequest, changes []mysqlParameterChangeRequest, restart bool, mode string) (app.TaskDetail, error) {
	target.Machine = strings.TrimSpace(target.Machine)
	if target.Machine == "" || target.Port <= 0 || target.Port > 65535 {
		return app.TaskDetail{}, errors.New("machine and valid port are required")
	}
	machine, instance, err := h.service.ResolveMySQLInstance(ctx, target.Machine, target.Port)
	if err != nil {
		return app.TaskDetail{}, err
	}
	if strings.TrimSpace(target.ConfigPath) == "" {
		target.ConfigPath = instance.MyCnfPath
	}
	if strings.TrimSpace(target.SystemdUnit) == "" {
		target.SystemdUnit = instance.SystemdUnit
	}
	target.MySQLDPath = filepath.Join(instance.BaseDir, "bin", "mysqld")
	target.Version = instance.Version
	if strings.TrimSpace(target.Version) == "" {
		target.Version, _ = mysqlapp.PackageVersion(instance.PackageName)
	}
	client := fmt.Sprintf("%s --defaults-extra-file=__GMHA_MYSQL_DEFAULTS_FILE__ --protocol=tcp --host=127.0.0.1 --port=%d --batch --raw --skip-column-names", shellQuote(filepath.Join(instance.BaseDir, "bin", "mysql")), target.Port)
	var command, operation, displayName, stepName string
	if mode == "collect" {
		command, operation, displayName, stepName, err = mysqlParameterCommand(client, mysqlParameterTaskRequest{Action: "collect", Port: target.Port})
	} else {
		command, err = mysqlParameterBatchCommand(client, target, changes, restart)
		operation, displayName, stepName = "mysql_parameters_apply", fmt.Sprintf("应用 %d 项 MySQL 参数", len(changes)), "应用运行参数"
		if len(changes) == 0 && restart {
			displayName, stepName = "重启 MySQL 实例", "重启并验证实例"
		}
	}
	if err != nil {
		return app.TaskDetail{}, err
	}
	detail, err := h.service.CreateExecTaskWithOptions(ctx, machine.IP, command, app.ExecTaskOptions{Operation: operation, DisplayName: displayName, StepName: stepName, Port: target.Port})
	if err != nil {
		return app.TaskDetail{}, err
	}
	go func(taskID string) {
		finished, waitErr := h.service.WaitForTask(context.Background(), taskID, 5*time.Minute)
		if waitErr == nil && (finished.Task.Status == taskdomain.StatusSuccess || finished.Task.Status == taskdomain.StatusFailed) {
			_ = h.service.RedactExecTaskCommand(context.Background(), taskID)
		}
	}(detail.Task.ID)
	return detail, nil
}

// HandleMySQLUpgrade performs server-side compatibility validation before
// creating the soft-link replacement and data-upgrade workflow.
func (h *TaskHandler) HandleMySQLUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlUpgradeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, err := h.service.CreateMySQLUpgradeTask(r.Context(), app.MySQLUpgradeRequest{Machine: req.Machine, Port: req.Port, PackageName: req.PackageName, PrecheckTaskID: req.PrecheckTaskID, Force: req.Force, RiskAcknowledged: req.RiskAcknowledged})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	go func(taskID string) {
		finished, waitErr := h.service.WaitForTask(context.Background(), taskID, 2*time.Hour)
		if waitErr == nil && (finished.Task.Status == taskdomain.StatusSuccess || finished.Task.Status == taskdomain.StatusFailed) {
			_ = h.service.RedactExecTaskCommand(context.Background(), taskID)
		}
	}(plan.Task.Task.ID)
	writeJSON(w, http.StatusOK, plan)
}

// HandleMySQLUpgradePrecheck creates the independent report that gates an
// upgrade. It deliberately accepts no database username or password.
func (h *TaskHandler) HandleMySQLUpgradePrecheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlUpgradeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, err := h.service.CreateMySQLUpgradePrecheck(r.Context(), app.MySQLUpgradeRequest{Machine: req.Machine, Port: req.Port, PackageName: req.PackageName})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, plan)
}

func mysqlParameterCommand(client string, req mysqlParameterTaskRequest) (string, string, string, string, error) {
	switch req.Action {
	case "collect":
		sql := mysqlParameterCollectionSQL()
		return client + " --execute=" + shellQuote(sql), "mysql_parameters_collect", "采集 MySQL 全部运行参数", "动态采集运行参数", nil
	case "update", "delete":
	default:
		return "", "", "", "", errors.New("action must be collect, update, or delete")
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !mysqlParameterPattern.MatchString(name) {
		return "", "", "", "", errors.New("invalid MySQL parameter name")
	}
	configName, configValue, err := mysqlParameterForVersion(name, req.Value, req.Version)
	if err != nil {
		return "", "", "", "", err
	}
	if req.Action == "update" && (strings.TrimSpace(req.Value) == "" || strings.ContainsAny(req.Value, "\r\n\x00")) {
		return "", "", "", "", errors.New("parameter value is required and must be a single line")
	}
	configPath := strings.TrimSpace(req.ConfigPath)
	if configPath == "" {
		configPath = fmt.Sprintf("/data/%d/my.cnf", req.Port)
	}
	if !filepath.IsAbs(configPath) {
		return "", "", "", "", errors.New("config_path must be absolute")
	}
	unit := strings.TrimSpace(req.SystemdUnit)
	if unit == "" {
		unit = fmt.Sprintf("mysqld-%d", req.Port)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9@_.-]+$`).MatchString(unit) {
		return "", "", "", "", errors.New("invalid systemd unit")
	}
	parts := make([]string, 0, 4)
	configChanged := false
	if req.Action == "update" && (req.ApplyMode == "config" || req.ApplyMode == "both") {
		parts = append(parts, mysqlParameterConfigCommand(configPath, configName, configValue, false))
		configChanged = true
	}
	if req.Action == "delete" {
		parts = append(parts, mysqlParameterConfigCommand(configPath, configName, "", true))
		configChanged = true
	}
	if configChanged && strings.TrimSpace(req.MySQLDPath) != "" {
		parts = append(parts, mysqlParameterValidateConfigCommand(req.MySQLDPath, configPath, req.Version))
	}
	if req.Action == "update" && (req.ApplyMode == "dynamic" || req.ApplyMode == "both") {
		dynamicName := mysqlDynamicParameterNameForVersion(configName, req.Version)
		sql := fmt.Sprintf("SET GLOBAL %s = %s; SELECT CONCAT('GMHA_EFFECTIVE_VALUE\\t', @@GLOBAL.%s);", dynamicName, sqlString(configValue), dynamicName)
		parts = append(parts, client+" --execute="+shellQuote(sql))
	}
	if req.Action == "delete" {
		dynamicName := mysqlDynamicParameterNameForVersion(configName, req.Version)
		resetSQL := "RESET PERSIST IF EXISTS " + configName
		if req.ApplyMode == "dynamic" || req.ApplyMode == "both" {
			resetSQL = "SET GLOBAL " + dynamicName + " = DEFAULT; " + resetSQL
		}
		capabilities, capabilityErr := mysqlapp.CapabilitiesForVersion(req.Version)
		if capabilityErr == nil && !capabilities.SupportsSetPersist {
			resetSQL = "SET GLOBAL " + dynamicName + " = DEFAULT"
		}
		parts = append(parts, client+" --execute="+shellQuote(resetSQL))
	}
	if req.Restart {
		parts = append(parts, fmt.Sprintf("systemctl restart %s && systemctl is-active %s && %s --execute=%s", shellQuote(unit), shellQuote(unit), client, shellQuote("SELECT CONCAT('GMHA_RESTARTED_VERSION\\t', @@version)")))
	}
	if len(parts) == 0 {
		return "", "", "", "", errors.New("static parameter update requires config apply mode")
	}
	actionText := map[string]string{"update": "修改", "delete": "删除"}[req.Action]
	return strings.Join(parts, " && "), "mysql_parameter_" + req.Action, actionText + " MySQL 参数 " + name, actionText + "运行参数", nil
}

func mysqlParameterValidateConfigCommand(mysqldPath, configPath string, version ...string) string {
	mysqld := shellQuote(strings.TrimSpace(mysqldPath))
	base := mysqld + " --defaults-file=" + shellQuote(configPath)
	validate := "if " + mysqld + " --no-defaults --verbose --help 2>/dev/null | grep -q -- '--validate-config'; then " + base + " --validate-config; else " + base + " --verbose --help >/dev/null; fi"
	return "if ! " + validate + "; then cp -a \"$backup\" \"$config\"; echo 'GMHA_CONFIG_VALIDATION_FAILED: restored previous my.cnf' >&2; exit 1; fi"
}

func mysqlParameterForVersion(name, value, version string) (string, string, error) {
	if strings.TrimSpace(version) == "" {
		version = "8.0.35"
	}
	capabilities, err := mysqlapp.CapabilitiesForVersion(version)
	if err != nil {
		return "", "", err
	}
	switch name {
	case "collation_server":
		if capabilities.Legacy57 && strings.EqualFold(strings.TrimSpace(value), "utf8mb4_0900_ai_ci") {
			return name, "utf8mb4_unicode_ci", nil
		}
		return name, value, nil
	case "binlog_expire_logs_seconds":
		if !capabilities.Legacy57 {
			return name, value, nil
		}
		seconds, err := strconv.Atoi(strings.TrimSpace(value))
		if value != "" && (err != nil || seconds < 0) {
			return "", "", errors.New("binlog_expire_logs_seconds must be a non-negative integer")
		}
		days := seconds / 86400
		if seconds%86400 != 0 {
			days++
		}
		return "expire_logs_days", strconv.Itoa(days), nil
	case "log_replica_updates":
		if capabilities.LegacyReplicationNames {
			return "log_slave_updates", value, nil
		}
		return name, value, nil
	case "log_slow_replica_statements":
		if capabilities.LegacyReplicationNames {
			return "log_slow_slave_statements", value, nil
		}
		return name, value, nil
	case "transaction_isolation":
		return name, value, nil
	case "innodb_redo_log_capacity":
		if !capabilities.LegacyRedoLog {
			return name, value, nil
		}
		if value == "" {
			return "innodb_log_file_size", value, nil
		}
		match := regexp.MustCompile(`^([0-9]+)([KMGTP]?)$`).FindStringSubmatch(strings.ToUpper(strings.TrimSpace(value)))
		if len(match) != 3 {
			return "", "", errors.New("innodb_redo_log_capacity must be a MySQL size such as 512M or 4G")
		}
		total, _ := strconv.ParseInt(match[1], 10, 64)
		if total < 2 {
			return "", "", errors.New("innodb_redo_log_capacity is too small for two legacy redo log files")
		}
		return "innodb_log_file_size", strconv.FormatInt(total/2, 10) + match[2], nil
	default:
		return name, value, nil
	}
}

func mysqlDynamicParameterNameForVersion(name, version string) string {
	capabilities, err := mysqlapp.CapabilitiesForVersion(version)
	if err == nil && capabilities.LegacyTransactionVariable && name == "transaction_isolation" {
		return "tx_isolation"
	}
	return name
}

func mysqlParameterCollectionSQL() string {
	names := make([]string, 0, len(dynamicMySQLParameterNames))
	for name := range dynamicMySQLParameterNames {
		names = append(names, sqlString(name))
	}
	sort.Strings(names)
	return "SELECT CONCAT('GMHA_MYSQL_PARAMETER\\t', IF(LOWER(VARIABLE_NAME)='tx_isolation','transaction_isolation',VARIABLE_NAME), '\\t', " +
		"REPLACE(REPLACE(VARIABLE_VALUE, CHAR(10), '\\\\n'), CHAR(9), ' '), '\\t', " +
		"IF(LOWER(VARIABLE_NAME) IN (" + strings.Join(names, ",") + "), 'dynamic', 'restart')) " +
		"FROM performance_schema.global_variables ORDER BY VARIABLE_NAME"
}

// mysqlParameterConfigCommand updates only the [mysqld] section and writes the
// result atomically. sed replacement strings are deliberately avoided because
// valid parameter values may contain '&', '|', quotes, paths, or shell tokens.
func mysqlParameterConfigCommand(configPath, name, value string, remove bool) string {
	action := "update"
	line := name + "=" + value
	if remove {
		action = "delete"
		line = ""
	}
	program := `
BEGIN { in_mysqld=0; saw_mysqld=0; changed=0 }
function section(line) { return line ~ /^[[:space:]]*\[[^]]+\][[:space:]]*$/ }
{
  if (section($0)) {
    if (in_mysqld && !changed && ENVIRON["GMHA_PARAMETER_ACTION"] == "update") {
      print ENVIRON["GMHA_PARAMETER_LINE"]
      changed=1
    }
    in_mysqld = ($0 ~ /^[[:space:]]*\[mysqld\][[:space:]]*$/)
    if (in_mysqld) saw_mysqld=1
  }
  if (in_mysqld && $0 ~ "^[[:space:]]*" ENVIRON["GMHA_PARAMETER_NAME"] "[[:space:]]*=") {
    if (ENVIRON["GMHA_PARAMETER_ACTION"] == "update" && !changed) print ENVIRON["GMHA_PARAMETER_LINE"]
    changed=1
    next
  }
  print
}
END {
  if (ENVIRON["GMHA_PARAMETER_ACTION"] == "update" && !changed) {
    if (!saw_mysqld) { print ""; print "[mysqld]" }
    print ENVIRON["GMHA_PARAMETER_LINE"]
  }
}`
	return fmt.Sprintf(
		"config=%s; test -f \"$config\"; backup=\"${config}.gmha.$(date +%%Y%%m%%d%%H%%M%%S).$$.bak\"; tmp=\"${config}.gmha.$$\"; content=\"${tmp}.content\"; cp -a \"$config\" \"$backup\"; cp -a \"$config\" \"$tmp\"; trap 'rm -f \"$tmp\" \"$content\"' EXIT HUP INT TERM; GMHA_PARAMETER_ACTION=%s GMHA_PARAMETER_NAME=%s GMHA_PARAMETER_LINE=%s awk %s \"$config\" > \"$content\"; cat \"$content\" > \"$tmp\"; mv -f \"$tmp\" \"$config\"; rm -f \"$content\"; trap - EXIT HUP INT TERM",
		shellQuote(configPath), shellQuote(action), shellQuote(name), shellQuote(line), shellQuote(program),
	)
}

func mysqlParameterBatchCommand(client string, target mysqlParameterTargetRequest, changes []mysqlParameterChangeRequest, restart bool) (string, error) {
	parts := make([]string, 0, len(changes)+1)
	for _, change := range changes {
		applyMode := "config"
		if mysqlParameterIsDynamic(change.Name) {
			applyMode = "both"
		}
		command, _, _, _, err := mysqlParameterCommand(client, mysqlParameterTaskRequest{
			Action: change.Action, Name: change.Name, Value: change.Value, ApplyMode: applyMode,
			ConfigPath: target.ConfigPath, SystemdUnit: target.SystemdUnit, Port: target.Port, MySQLDPath: target.MySQLDPath, Version: target.Version,
		})
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+command+")")
	}
	if restart {
		unit := strings.TrimSuffix(strings.TrimSpace(target.SystemdUnit), ".service")
		if unit == "" {
			unit = fmt.Sprintf("mysqld-%d", target.Port)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9@_.-]+$`).MatchString(unit) {
			return "", errors.New("invalid systemd unit")
		}
		parts = append(parts, fmt.Sprintf("systemctl restart %s && systemctl is-active %s && %s --execute=%s", shellQuote(unit), shellQuote(unit), client, shellQuote("SELECT CONCAT('GMHA_RESTARTED_VERSION\\t', @@version)")))
	}
	if len(parts) == 0 {
		return "", errors.New("parameter task has no changes or restart action")
	}
	return strings.Join(parts, " && "), nil
}

var dynamicMySQLParameterNames = map[string]struct{}{
	"autocommit": {}, "binlog_expire_logs_seconds": {}, "expire_logs_days": {}, "binlog_format": {}, "connect_timeout": {}, "event_scheduler": {},
	"general_log": {}, "general_log_file": {}, "group_concat_max_len": {}, "innodb_buffer_pool_size": {},
	"innodb_flush_log_at_trx_commit": {}, "innodb_io_capacity": {}, "innodb_io_capacity_max": {}, "innodb_lock_wait_timeout": {},
	"innodb_max_dirty_pages_pct": {}, "innodb_old_blocks_time": {}, "innodb_online_alter_log_max_size": {}, "innodb_print_all_deadlocks": {},
	"innodb_purge_threads": {}, "innodb_read_io_threads": {}, "innodb_stats_on_metadata": {}, "innodb_write_io_threads": {},
	"interactive_timeout": {}, "join_buffer_size": {}, "lock_wait_timeout": {}, "log_output": {}, "long_query_time": {},
	"max_allowed_packet": {}, "max_connect_errors": {}, "max_connections": {}, "max_execution_time": {}, "max_heap_table_size": {},
	"max_prepared_stmt_count": {}, "net_read_timeout": {}, "net_write_timeout": {}, "optimizer_switch": {}, "read_buffer_size": {},
	"read_only": {}, "read_rnd_buffer_size": {}, "slow_query_log": {}, "sort_buffer_size": {}, "sql_mode": {},
	"super_read_only": {}, "sync_binlog": {}, "table_definition_cache": {}, "table_open_cache": {}, "thread_cache_size": {},
	"tmp_table_size": {}, "transaction_isolation": {}, "tx_isolation": {}, "wait_timeout": {},
}

func mysqlParameterIsDynamic(name string) bool {
	_, ok := dynamicMySQLParameterNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func (h *TaskHandler) HandleCreateMySQLUninstallTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createMySQLUninstallTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateMySQLUninstallTask(r.Context(), taskusecase.CreateMySQLUninstallTaskRequest{Machine: req.Machine, Port: req.Port})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// HandleCreateMySQLTopologyTasks 为现有 MySQL 实例创建一主多从、双主或延时从库调整任务。
func (h *TaskHandler) HandleCreateMySQLTopologyTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createMySQLTopologyTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	nodes := make([]taskusecase.CreateMySQLTopologyNodeRequest, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		nodes = append(nodes, taskusecase.CreateMySQLTopologyNodeRequest{
			Machine: node.Machine, Port: node.Port, Role: node.Role,
			SourceMachine: node.SourceMachine, DelaySeconds: node.DelaySeconds,
		})
	}
	item, err := h.service.CreateMySQLTopologyTasks(r.Context(), taskusecase.CreateMySQLTopologyTaskRequest{
		Topology: req.Topology, Port: req.Port, RootPassword: req.RootPassword,
		ReplicationUser: req.ReplicationUser, ReplicationPassword: req.ReplicationPassword,
		CloneUser: req.CloneUser, ClonePassword: req.ClonePassword, UseClone: req.UseClone,
		PrimaryMachine: req.PrimaryMachine, CloneSeedMachine: req.CloneSeedMachine,
		CloneTargetMachines: req.CloneTargetMachines, ParallelType: req.ParallelType,
		ParallelWorkers: req.ParallelWorkers, Nodes: nodes,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *TaskHandler) HandleCreateClusterMySQLInstallTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req createClusterMySQLInstallTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateClusterMySQLInstallTasks(r.Context(), app.ClusterMySQLInstallRequest{Cluster: req.Cluster, Port: req.Port, ServerIDStart: req.ServerIDStart, MySQLUser: req.MySQLUser, InstanceDir: req.InstanceDir, DataDir: req.DataDir, BinlogDir: req.BinlogDir, RedoDir: req.RedoDir, UndoDir: req.UndoDir, TmpDir: req.TmpDir, BaseDir: req.BaseDir, MyCnfPath: req.MyCnfPath, SocketPath: req.SocketPath, ErrorLog: req.ErrorLog, PIDFile: req.PIDFile, CharacterSetsDir: req.CharacterSetsDir, PluginDir: req.PluginDir, RootPassword: req.RootPassword, Profile: req.Profile, Version: req.Version, Architecture: req.Architecture, InstallPTTools: req.InstallPTTools, InstallXtraBackup: req.InstallXtraBackup, MemoryAllocator: req.MemoryAllocator, RuntimeParameters: req.RuntimeParameters, Accounts: mysqlAccountRequests(req.Accounts)})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *TaskHandler) HandleCreateClusterMySQLUninstallTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Cluster string `json:"cluster"`
		Port    int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.CreateClusterMySQLUninstallTasks(r.Context(), app.ClusterMySQLUninstallRequest{Cluster: req.Cluster, Port: req.Port})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// mysqlAccountRequests 将 HTTP 请求中的 MySQL 账号配置转换为领域模型。
func mysqlAccountRequests(items []createMySQLAccountRequest) []taskdomain.MySQLAccountSpec {
	out := make([]taskdomain.MySQLAccountSpec, 0, len(items))
	for _, item := range items {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		out = append(out, taskdomain.MySQLAccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        enabled,
			ExtendedBackup: item.ExtendedBackup,
			Privileges:     item.Privileges,
		})
	}
	return out
}

// HandleMySQLPackages 返回安装任务可选择的 MySQL 版本与 Linux 架构信息。
func (h *TaskHandler) HandleMySQLPackages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	items, err := h.service.ListMySQLPackages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// HandleMySQLPackageDownload 处理 MySQL 安装包下载请求，从本地 software 目录提供文件。
func (h *TaskHandler) HandleMySQLPackageDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := filepath.Base(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/v1/software/mysql/")))
	if name == "" || name == "." {
		writeError(w, http.StatusBadRequest, http.ErrMissingFile)
		return
	}
	fullPath := filepath.Join("software", "mysql", name)
	if _, err := os.Stat(fullPath); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	http.ServeFile(w, r, fullPath)
}

// HandleAgentWS 返回 Agent WebSocket 任务分发处理器，负责任务下发和进度上报。
func (h *TaskHandler) HandleAgentWS() http.Handler {
	return websocket.Handler(func(conn *websocket.Conn) {
		agentID := strings.TrimSpace(conn.Request().URL.Query().Get("agent_id"))
		if agentID == "" {
			_ = conn.Close()
			return
		}
		capabilities := splitCapabilities(conn.Request().URL.Query().Get("capabilities"))
		machineID := strings.TrimSpace(conn.Request().URL.Query().Get("machine_id"))
		client := &wsTaskClient{conn: conn}
		h.service.RegisterAgentForMachineWithCapabilities(agentID, machineID, client, capabilities)
		defer h.service.UnregisterAgent(agentID, client)
		defer conn.Close()

		for {
			var report taskdomain.ReportEnvelope
			if err := websocket.JSON.Receive(conn, &report); err != nil {
				return
			}
			if strings.TrimSpace(report.AgentID) == "" {
				report.AgentID = agentID
			}
			_ = h.service.HandleReport(conn.Request().Context(), report)
		}
	})
}

// splitCapabilities 将逗号分隔的能力列表字符串拆分为切片。
func splitCapabilities(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// wsTaskClient 是 WebSocket 任务客户端，负责通过 WebSocket 连接向 Agent 发送任务。
type wsTaskClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// Send 向 Agent 发送任务分发消息，使用互斥锁保证并发安全。
func (c *wsTaskClient) Send(msg taskdomain.DispatchEnvelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return websocket.JSON.Send(c.conn, msg)
}
