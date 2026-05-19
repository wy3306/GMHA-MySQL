package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
		items, err := h.service.ListTasks(r.Context(), 50)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type createExecTaskRequest struct {
	Machine string `json:"machine"`
	Command string `json:"command"`
}

type createCollectMachineInfoRequest struct {
	Machine string `json:"machine"`
}

type createMySQLInstallTaskRequest struct {
	Machine          string                      `json:"machine"`
	Port             int                         `json:"port"`
	ServerID         int                         `json:"server_id"`
	MySQLUser        string                      `json:"mysql_user"`
	InstanceDir      string                      `json:"instance_dir"`
	DataDir          string                      `json:"data_dir"`
	BinlogDir        string                      `json:"binlog_dir"`
	RedoDir          string                      `json:"redo_dir"`
	UndoDir          string                      `json:"undo_dir"`
	TmpDir           string                      `json:"tmp_dir"`
	BaseDir          string                      `json:"base_dir"`
	SocketPath       string                      `json:"socket_path"`
	ErrorLog         string                      `json:"error_log"`
	PIDFile          string                      `json:"pid_file"`
	CharacterSetsDir string                      `json:"character_sets_dir"`
	PluginDir        string                      `json:"plugin_dir"`
	RootPassword     string                      `json:"root_password"`
	Profile          string                      `json:"profile"`
	Accounts         []createMySQLAccountRequest `json:"accounts"`
}

type createMySQLAccountRequest struct {
	Role           string `json:"role"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Host           string `json:"host"`
	Enabled        *bool  `json:"enabled"`
	ExtendedBackup bool   `json:"extended_backup,omitempty"`
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
		Machine:          req.Machine,
		Port:             req.Port,
		ServerID:         req.ServerID,
		MySQLUser:        req.MySQLUser,
		InstanceDir:      req.InstanceDir,
		DataDir:          req.DataDir,
		BinlogDir:        req.BinlogDir,
		RedoDir:          req.RedoDir,
		UndoDir:          req.UndoDir,
		TmpDir:           req.TmpDir,
		BaseDir:          req.BaseDir,
		SocketPath:       req.SocketPath,
		ErrorLog:         req.ErrorLog,
		PIDFile:          req.PIDFile,
		CharacterSetsDir: req.CharacterSetsDir,
		PluginDir:        req.PluginDir,
		RootPassword:     req.RootPassword,
		Profile:          req.Profile,
		Accounts:         mysqlAccountRequests(req.Accounts),
	})
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
		})
	}
	return out
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
		client := &wsTaskClient{conn: conn}
		h.service.RegisterAgentWithCapabilities(agentID, client, capabilities)
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
