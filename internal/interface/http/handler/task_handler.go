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
	taskusecase "gmha/internal/usecase/task"
	"golang.org/x/net/websocket"
)

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
	MySQLUser       string   `json:"mysql_user"`
	MySQLPassword   string   `json:"mysql_password"`
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

type mysqlUserTaskRequest struct {
	Machine        string   `json:"machine"`
	Port           int      `json:"port"`
	Action         string   `json:"action"`
	TargetUsername string   `json:"target_username"`
	TargetPassword string   `json:"target_password"`
	TargetHost     string   `json:"target_host"`
	Privileges     []string `json:"privileges"`
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
}

type mysqlParameterTargetRequest struct {
	Machine     string `json:"machine"`
	Port        int    `json:"port"`
	ConfigPath  string `json:"config_path"`
	SystemdUnit string `json:"systemd_unit"`
	MySQLDPath  string `json:"-"`
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
			command, commandErr := clusterAutomationCommand(req)
			if commandErr != nil {
				item.Error = commandErr.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			if opts, ok := databaseAutomationTaskOptions(req); ok {
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

func databaseAutomationTaskOptions(req clusterAutomationRequest) (app.ExecTaskOptions, bool) {
	port := req.Port
	switch req.Operation {
	case "collect_mysql":
		return app.ExecTaskOptions{Operation: "mysql_collect", DisplayName: "采集 MySQL 运行数据", StepName: "查询数据库运行状态", Port: port}, true
	case "mysql_parameter":
		return app.ExecTaskOptions{Operation: "mysql_parameter", DisplayName: "修改 MySQL 参数 " + strings.TrimSpace(req.ParameterName), StepName: "应用数据库参数", Port: port}, true
	case "mysql_user":
		action := map[string]string{"create": "创建数据库用户", "update": "修改数据库用户密码", "delete": "删除数据库用户", "grant": "授予数据库权限", "revoke": "回收数据库权限", "query": "查询数据库授权", "list": "查询数据库用户", "lock": "锁定数据库用户", "unlock": "解锁数据库用户"}[req.UserAction]
		return app.ExecTaskOptions{Operation: "mysql_user_" + req.UserAction, DisplayName: action, StepName: action, Port: port}, true
	default:
		return app.ExecTaskOptions{}, false
	}
}

var mysqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$.-]{0,63}$`)
var mysqlHostPattern = regexp.MustCompile(`^[A-Za-z0-9%_.*:.-]{1,255}$`)
var mysqlParameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

var mysqlPrivilegeSet = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true, "CREATE USER": true, "ALTER": true, "DROP": true,
	"SHOW VIEW": true, "TRIGGER": true, "EVENT": true, "PROCESS": true, "RELOAD": true, "LOCK TABLES": true,
	"REPLICATION CLIENT": true, "REPLICATION SLAVE": true, "CONNECTION_ADMIN": true, "SYSTEM_VARIABLES_ADMIN": true, "REPLICATION_SLAVE_ADMIN": true, "BACKUP_ADMIN": true, "CLONE_ADMIN": true,
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
	case "collect_machine", "shell", "collect_mysql", "mysql_user", "mysql_parameter":
	default:
		return fmt.Errorf("unsupported automation operation %q", req.Operation)
	}
	if req.Operation == "shell" && strings.TrimSpace(req.Script) == "" {
		return errors.New("shell script is required")
	}
	if req.Operation == "collect_mysql" || req.Operation == "mysql_user" || req.Operation == "mysql_parameter" {
		if req.Port <= 0 || req.Port > 65535 {
			return errors.New("a valid MySQL port is required")
		}
		if !mysqlIdentifierPattern.MatchString(strings.TrimSpace(req.MySQLUser)) || req.MySQLPassword == "" {
			return errors.New("MySQL administrator username and password are required")
		}
	}
	if req.Operation == "mysql_user" {
		return validateMySQLUserAction(req.UserAction, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges)
	}
	if req.Operation == "mysql_parameter" {
		if !mysqlParameterPattern.MatchString(strings.TrimSpace(req.ParameterName)) || strings.TrimSpace(req.ParameterValue) == "" {
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

func clusterAutomationCommand(req clusterAutomationRequest) (string, error) {
	if req.Operation == "shell" {
		return req.Script, nil
	}
	if req.Operation == "collect_mysql" {
		return mysqlCommand(req, "SELECT @@hostname AS hostname, @@version AS version, @@port AS port; SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_connected','Questions','Queries','Uptime');"), nil
	}
	if req.Operation == "mysql_user" {
		sql, err := mysqlUserSQL(req.UserAction, req.TargetUsername, req.TargetHost, req.TargetPassword, req.Privileges)
		if err != nil {
			return "", err
		}
		return mysqlCommand(req, sql), nil
	}
	if req.Operation == "mysql_parameter" {
		name, value := req.ParameterName, sqlString(req.ParameterValue)
		parts := make([]string, 0, 2)
		if req.ApplyMode == "dynamic" || req.ApplyMode == "both" {
			parts = append(parts, mysqlCommand(req, fmt.Sprintf("SET GLOBAL %s = %s; SELECT @@GLOBAL.%s AS effective_value;", name, value, name)))
		}
		if req.ApplyMode == "restart" || req.ApplyMode == "both" {
			configPath, unit := req.ConfigPath, req.SystemdUnit
			if configPath == "" {
				configPath = "/etc/my.cnf"
			}
			if unit == "" {
				unit = "mysqld"
			}
			line := name + "=" + req.ParameterValue
			parts = append(parts, fmt.Sprintf("config=%s; tmp=\"${config}.gmha.$$\"; if grep -qE '^[[:space:]]*%s[[:space:]]*=' \"$config\"; then sed -E 's|^[[:space:]]*%s[[:space:]]*=.*|%s|' \"$config\" > \"$tmp\"; else cp \"$config\" \"$tmp\"; printf '\\n%%s\\n' %s >> \"$tmp\"; fi; mv \"$tmp\" \"$config\"; systemctl restart %s; systemctl is-active %s", shellQuote(configPath), name, name, shellQuote(line), shellQuote(line), shellQuote(unit), shellQuote(unit)))
		}
		return strings.Join(parts, " && "), nil
	}
	return "", errors.New("unsupported automation operation")
}

func clusterAutomationArtifactDir() string {
	return filepath.Join(os.TempDir(), "gmha", "cluster-reports")
}

func mysqlCommand(req clusterAutomationRequest, sql string) string {
	password := shellQuote(req.MySQLPassword)
	return fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=%s --batch --raw --execute=%s", password, req.Port, shellQuote(req.MySQLUser), shellQuote(sql))
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
		parts = append(parts, mysqlParameterConfigCommand(configPath, name, req.Value, false))
		configChanged = true
	}
	if req.Action == "delete" {
		parts = append(parts, mysqlParameterConfigCommand(configPath, name, "", true))
		configChanged = true
	}
	if configChanged && strings.TrimSpace(req.MySQLDPath) != "" {
		parts = append(parts, mysqlParameterValidateConfigCommand(req.MySQLDPath, configPath))
	}
	if req.Action == "update" && (req.ApplyMode == "dynamic" || req.ApplyMode == "both") {
		sql := fmt.Sprintf("SET GLOBAL %s = %s; SELECT CONCAT('GMHA_EFFECTIVE_VALUE\\t', @@GLOBAL.%s);", name, sqlString(req.Value), name)
		parts = append(parts, client+" --execute="+shellQuote(sql))
	}
	if req.Action == "delete" {
		resetSQL := "RESET PERSIST IF EXISTS " + name
		if req.ApplyMode == "dynamic" || req.ApplyMode == "both" {
			resetSQL = "SET GLOBAL " + name + " = DEFAULT; " + resetSQL
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

func mysqlParameterValidateConfigCommand(mysqldPath, configPath string) string {
	validate := shellQuote(strings.TrimSpace(mysqldPath)) + " --defaults-file=" + shellQuote(configPath) + " --validate-config"
	return "if ! " + validate + "; then cp -a \"$backup\" \"$config\"; echo 'GMHA_CONFIG_VALIDATION_FAILED: restored previous my.cnf' >&2; exit 1; fi"
}

func mysqlParameterCollectionSQL() string {
	names := make([]string, 0, len(dynamicMySQLParameterNames))
	for name := range dynamicMySQLParameterNames {
		names = append(names, sqlString(name))
	}
	sort.Strings(names)
	return "SELECT CONCAT('GMHA_MYSQL_PARAMETER\\t', VARIABLE_NAME, '\\t', " +
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
			ConfigPath: target.ConfigPath, SystemdUnit: target.SystemdUnit, Port: target.Port, MySQLDPath: target.MySQLDPath,
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
	"autocommit": {}, "binlog_expire_logs_seconds": {}, "binlog_format": {}, "connect_timeout": {}, "event_scheduler": {},
	"general_log": {}, "general_log_file": {}, "group_concat_max_len": {}, "innodb_buffer_pool_size": {},
	"innodb_flush_log_at_trx_commit": {}, "innodb_io_capacity": {}, "innodb_io_capacity_max": {}, "innodb_lock_wait_timeout": {},
	"innodb_max_dirty_pages_pct": {}, "innodb_old_blocks_time": {}, "innodb_online_alter_log_max_size": {}, "innodb_print_all_deadlocks": {},
	"innodb_purge_threads": {}, "innodb_read_io_threads": {}, "innodb_stats_on_metadata": {}, "innodb_write_io_threads": {},
	"interactive_timeout": {}, "join_buffer_size": {}, "lock_wait_timeout": {}, "log_output": {}, "long_query_time": {},
	"max_allowed_packet": {}, "max_connect_errors": {}, "max_connections": {}, "max_execution_time": {}, "max_heap_table_size": {},
	"max_prepared_stmt_count": {}, "net_read_timeout": {}, "net_write_timeout": {}, "optimizer_switch": {}, "read_buffer_size": {},
	"read_only": {}, "read_rnd_buffer_size": {}, "slow_query_log": {}, "sort_buffer_size": {}, "sql_mode": {},
	"super_read_only": {}, "sync_binlog": {}, "table_definition_cache": {}, "table_open_cache": {}, "thread_cache_size": {},
	"tmp_table_size": {}, "transaction_isolation": {}, "wait_timeout": {},
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
