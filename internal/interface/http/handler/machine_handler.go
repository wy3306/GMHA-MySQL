package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"gmha/internal/app"
	machineusecase "gmha/internal/usecase/machine"
)

// MachineHandler 是机器管理 HTTP API 的请求处理器。
type MachineHandler struct {
	service *app.MachineService
}

// NewMachineHandler 创建一个新的 MachineHandler 实例。
func NewMachineHandler(service *app.MachineService) *MachineHandler {
	return &MachineHandler{service: service}
}

// onboardMachineRequest 表示机器纳管请求体。
type onboardMachineRequest struct {
	MachineID      string `json:"machine_id"`
	Name           string `json:"name"`
	IP             string `json:"ip"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	SSHPassword    string `json:"ssh_password"`
	SSHPrivateKey  string `json:"ssh_private_key"`
	SSHPassphrase  string `json:"ssh_passphrase"`
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
	PreserveAgent  bool   `json:"preserve_agent"`
	PreserveMySQL  bool   `json:"preserve_mysql"`
}

// createSSHCredentialRequest 表示创建 SSH 凭证请求体。
type createSSHCredentialRequest struct {
	Name        string `json:"name"`
	SSHUser     string `json:"ssh_user"`
	Type        string `json:"type"`
	SSHPassword string `json:"ssh_password"`
	PrivateKey  string `json:"private_key"`
	Passphrase  string `json:"passphrase"`
}

type assignCredentialRequest struct {
	MachineIDs []string `json:"machine_ids"`
}

// createClusterRequest 表示创建集群请求体。
type createClusterRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// updateMachineRequest 表示更新机器请求体。
type updateMachineRequest struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	SSHPort int    `json:"ssh_port"`
	SSHUser string `json:"ssh_user"`
}

type deleteMachineRequest struct {
	DeleteMySQL bool `json:"delete_mysql"`
	DeleteAgent bool `json:"delete_agent"`
	DetachOnly  bool `json:"detach_only"`
}

type batchDeleteMachinesRequest struct {
	MachineIDs  []string `json:"machine_ids"`
	DeleteMySQL bool     `json:"delete_mysql"`
	DeleteAgent bool     `json:"delete_agent"`
	DetachOnly  bool     `json:"detach_only"`
	Concurrency int      `json:"concurrency"`
}

// assignClusterRequest 表示分配集群请求体。
type assignClusterRequest struct {
	Cluster string `json:"cluster"`
}

// updateClusterRequest 表示更新集群请求体。
type updateClusterRequest struct {
	NewName     string `json:"new_name"`
	Description string `json:"description"`
}

type assignClusterMembersRequest struct {
	MachineIDs []string `json:"machine_ids"`
}

// HandleMachines 处理机器列表查询（GET）和机器纳管（POST）请求。
func (h *MachineHandler) HandleMachines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListMachines(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
		cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
		filtered := items[:0]
		for _, item := range items {
			if keyword != "" {
				text := strings.ToLower(strings.Join([]string{item.ID, item.Name, item.IP, item.SSHUser, item.Cluster}, " "))
				if !strings.Contains(text, keyword) {
					continue
				}
			}
			if cluster == "__unassigned__" && strings.TrimSpace(item.Cluster) != "" {
				continue
			}
			if cluster != "" && cluster != "all" && cluster != "__unassigned__" && item.Cluster != cluster {
				continue
			}
			filtered = append(filtered, item)
		}
		writePagedJSON(w, r, filtered)
	case http.MethodPost:
		var req onboardMachineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := h.service.Onboard(r.Context(), machineusecase.OnboardMachineRequest{
			MachineID:      req.MachineID,
			Name:           req.Name,
			IP:             req.IP,
			SSHPort:        req.SSHPort,
			SSHUser:        req.SSHUser,
			SSHPassword:    req.SSHPassword,
			SSHPrivateKey:  req.SSHPrivateKey,
			SSHPassphrase:  req.SSHPassphrase,
			CredentialID:   req.CredentialID,
			CredentialName: req.CredentialName,
			PreserveAgent:  req.PreserveAgent,
			PreserveMySQL:  req.PreserveMySQL,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleBatchDeleteMachines 将一次批量删除作为一个任务中心业务流程处理。
func (h *MachineHandler) HandleBatchDeleteMachines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req batchDeleteMachinesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.DeleteMachinesWithOptions(r.Context(), req.MachineIDs, app.DeleteMachineOptions{
		DeleteMySQL: req.DeleteMySQL,
		DeleteAgent: req.DeleteAgent,
		DetachOnly:  req.DetachOnly,
	}, req.Concurrency)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandlePrecheck 在机器纳管前执行非破坏性的 SSH 与环境检查。
func (h *MachineHandler) HandlePrecheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req onboardMachineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	report, err := h.service.PrecheckOnboard(r.Context(), machineusecase.OnboardMachineRequest{Name: req.Name, IP: req.IP, SSHPort: req.SSHPort, SSHUser: req.SSHUser, SSHPassword: req.SSHPassword, SSHPrivateKey: req.SSHPrivateKey, SSHPassphrase: req.SSHPassphrase, CredentialID: req.CredentialID})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *MachineHandler) HandleCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		onboardMachineRequest
		ConfirmPhrase string `json:"confirm_phrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err := h.service.CleanupOnboardTarget(r.Context(), machineusecase.OnboardMachineRequest{Name: req.Name, IP: req.IP, SSHPort: req.SSHPort, SSHUser: req.SSHUser, SSHPassword: req.SSHPassword, SSHPrivateKey: req.SSHPrivateKey, SSHPassphrase: req.SSHPassphrase, CredentialID: req.CredentialID}, req.ConfirmPhrase)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleaned"})
}

// HandleCredentials 处理 SSH 凭证列表查询（GET）和创建（POST）请求。
func (h *MachineHandler) HandleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListSSHCredentials(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writePagedJSON(w, r, items)
	case http.MethodPost:
		var req createSSHCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		item, err := h.service.CreateSSHCredential(r.Context(), req.Name, req.SSHUser, req.Type, req.SSHPassword, req.PrivateKey, req.Passphrase)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleCredentialByID 处理按 ID 删除 SSH 凭证请求（DELETE）。
func (h *MachineHandler) HandleCredentialByID(w http.ResponseWriter, r *http.Request) {
	selector := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/ssh-credentials/"), "/")
	if strings.HasSuffix(selector, "/assign") {
		h.handleAssignCredential(w, r, strings.TrimSuffix(selector, "/assign"))
		return
	}
	if selector == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := h.service.DeleteSSHCredential(r.Context(), selector); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"credential": selector})
}

func (h *MachineHandler) handleAssignCredential(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req assignCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.service.AssignCredential(r.Context(), credentialID, req.MachineIDs); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential_id": credentialID, "machine_ids": req.MachineIDs})
}

// HandleMachineByID 处理按 ID 更新（PUT）和删除（DELETE）机器请求，以及分配集群子路径。
func (h *MachineHandler) HandleMachineByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/machines/")
	if strings.HasSuffix(path, "/assign-cluster") {
		machineID := strings.TrimSuffix(path, "/assign-cluster")
		h.handleAssignCluster(w, r, strings.TrimSuffix(machineID, "/"))
		return
	}
	if strings.HasSuffix(path, "/static-info") {
		h.handleMachineStaticInfo(w, r, strings.Trim(strings.TrimSuffix(path, "/static-info"), "/"))
		return
	}
	if strings.HasSuffix(path, "/dynamic-metrics") {
		h.handleMachineDynamicMetrics(w, r, strings.Trim(strings.TrimSuffix(path, "/dynamic-metrics"), "/"))
		return
	}
	if strings.HasSuffix(path, "/delete-precheck") {
		h.handleMachineDeletePrecheck(w, r, strings.Trim(strings.TrimSuffix(path, "/delete-precheck"), "/"))
		return
	}
	machineID := strings.Trim(path, "/")
	if machineID == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, ok, err := h.service.GetMachine(r.Context(), machineID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPut:
		var req updateMachineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.UpdateMachine(r.Context(), machineID, req.Name, req.IP, req.SSHPort, req.SSHUser); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"machine_id": machineID})
	case http.MethodDelete:
		// 无请求体时保留原有行为：删除机器前卸载 Agent。
		req := deleteMachineRequest{DeleteAgent: true}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result, err := h.service.DeleteMachineWithOptions(r.Context(), machineID, app.DeleteMachineOptions{
			DeleteMySQL: req.DeleteMySQL,
			DeleteAgent: req.DeleteAgent,
			DetachOnly:  req.DetachOnly,
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

func (h *MachineHandler) handleMachineDeletePrecheck(w http.ResponseWriter, r *http.Request, machineID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	report, err := h.service.PrecheckDeleteMachine(r.Context(), machineID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleMachineStaticInfo 查看已采集的静态信息（GET）或调用 CLI 同一内核重新采集（POST）。
func (h *MachineHandler) handleMachineStaticInfo(w http.ResponseWriter, r *http.Request, machineID string) {
	machine, ok, err := h.service.GetMachine(r.Context(), machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, err := h.service.GetStaticInfo(r.Context(), machine.IP)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodPost:
		item, err := h.service.RefreshStaticInfo(r.Context(), machine.IP)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleMachineDynamicMetrics 返回 Agent 已上报的主机动态指标。
func (h *MachineHandler) handleMachineDynamicMetrics(w http.ResponseWriter, r *http.Request, machineID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	machine, ok, err := h.service.GetMachine(r.Context(), machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	item, err := h.service.GetMachineDynamicMetrics(r.Context(), machine.IP)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// writePagedJSON 在提供 page 参数时返回分页结构；未提供时保持旧数组响应兼容 CLI/API 调用方。
func writePagedJSON[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if r.URL.Query().Get("page") == "" {
		writeJSON(w, http.StatusOK, items)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items[start:end], "total": len(items), "page": page, "page_size": pageSize})
}

// HandleClusters 处理集群列表查询（GET）和创建（POST）请求。
func (h *MachineHandler) HandleClusters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListClusters(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
		if keyword != "" {
			filtered := make([]app.ClusterView, 0, len(items))
			for _, item := range items {
				if strings.Contains(strings.ToLower(item.Name), keyword) || strings.Contains(strings.ToLower(item.Description), keyword) {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		writePagedJSON(w, r, items)
	case http.MethodPost:
		var req createClusterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.CreateCluster(r.Context(), req.Name, req.Description); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": req.Name, "description": req.Description})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleAssignCluster 处理将机器分配到集群的 POST 请求。
func (h *MachineHandler) handleAssignCluster(w http.ResponseWriter, r *http.Request, machineID string) {
	if r.Method == http.MethodDelete {
		if err := h.service.UnassignMachineCluster(r.Context(), machineID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"machine_id": machineID, "cluster": ""})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req assignClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.service.AssignMachineCluster(r.Context(), machineID, req.Cluster); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"machine_id": machineID, "cluster": req.Cluster})
}

// writeJSON 以 JSON 格式写入 HTTP 响应。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// HandleClusterByName 处理按集群名称查询（GET）、更新（PUT）和删除（DELETE）请求。
func (h *MachineHandler) HandleClusterByName(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/")
	if name == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListClusters(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for _, item := range items {
			if item.Name == name {
				writeJSON(w, http.StatusOK, item)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case http.MethodPut:
		var req updateClusterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.UpdateCluster(r.Context(), name, req.NewName, req.Description); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"old_name": name, "new_name": req.NewName, "description": req.Description})
	case http.MethodDelete:
		if err := h.service.DeleteCluster(r.Context(), name); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": name})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleClusterCleanup 执行 CLI“cluster cleanup”使用的同一集群清理服务。
// 该操作会按机器卸载 MySQL 与 Agent，再清理本地记录并删除集群，因此仅接受 POST。
func (h *MachineHandler) HandleClusterCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/")
	name := strings.TrimSuffix(path, "/cleanup")
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("cluster name is required"))
		return
	}
	result, err := h.service.CleanupCluster(r.Context(), name)
	if err != nil {
		// 即使部分机器失败，也返回明细，让 Web 能展示问题报告。
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// HandleClusterMembers 将一台或多台已有机器划入目标集群。
// 每台机器均复用 AssignMachineCluster，因此会沿用 CLI 的 Agent 安装和静态信息采集流程。
func (h *MachineHandler) HandleClusterMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/")
	clusterName := strings.Trim(strings.TrimSuffix(path, "/members"), "/")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, errors.New("cluster name is required"))
		return
	}
	var req assignClusterMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.MachineIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("machine_ids is required"))
		return
	}
	type memberResult struct {
		MachineID string `json:"machine_id"`
		Success   bool   `json:"success"`
		Error     string `json:"error,omitempty"`
	}
	results := make([]memberResult, 0, len(req.MachineIDs))
	failed := 0
	for _, machineID := range req.MachineIDs {
		machineID = strings.TrimSpace(machineID)
		if machineID == "" {
			continue
		}
		item := memberResult{MachineID: machineID}
		if err := h.service.AssignMachineCluster(r.Context(), machineID, clusterName); err != nil {
			item.Error = err.Error()
			failed++
		} else {
			item.Success = true
		}
		results = append(results, item)
	}
	response := map[string]any{"cluster": clusterName, "results": results, "failed": failed}
	if failed > 0 {
		response["error"] = "部分机器未能分配至集群"
		writeJSON(w, http.StatusBadRequest, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleClusterMachines 分页返回集群中的完整机器信息，供 Web 机器管理和 MySQL 状态合并视图使用。
func (h *MachineHandler) HandleClusterMachines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/")
	clusterName := strings.Trim(strings.TrimSuffix(path, "/machines"), "/")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, errors.New("cluster name is required"))
		return
	}
	items, err := h.service.ListMachines(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	filtered := items[:0]
	for _, item := range items {
		if item.Cluster == clusterName {
			filtered = append(filtered, item)
		}
	}
	writePagedJSON(w, r, filtered)
}

// writeError 以 JSON 格式写入错误响应。
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
