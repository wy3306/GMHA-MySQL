package handler

import (
	"encoding/json"
	"net/http"
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
	Name           string `json:"name"`
	IP             string `json:"ip"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	SSHPassword    string `json:"ssh_password"`
	CredentialID   string `json:"credential_id"`
	CredentialName string `json:"credential_name"`
}

// createSSHCredentialRequest 表示创建 SSH 凭证请求体。
type createSSHCredentialRequest struct {
	Name        string `json:"name"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
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

// assignClusterRequest 表示分配集群请求体。
type assignClusterRequest struct {
	Cluster string `json:"cluster"`
}

// updateClusterRequest 表示更新集群请求体。
type updateClusterRequest struct {
	NewName     string `json:"new_name"`
	Description string `json:"description"`
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
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var req onboardMachineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := h.service.Onboard(r.Context(), machineusecase.OnboardMachineRequest{
			Name:           req.Name,
			IP:             req.IP,
			SSHPort:        req.SSHPort,
			SSHUser:        req.SSHUser,
			SSHPassword:    req.SSHPassword,
			CredentialID:   req.CredentialID,
			CredentialName: req.CredentialName,
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

// HandleCredentials 处理 SSH 凭证列表查询（GET）和创建（POST）请求。
func (h *MachineHandler) HandleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.ListSSHCredentials(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var req createSSHCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		item, err := h.service.CreateSSHCredential(r.Context(), req.Name, req.SSHUser, req.SSHPassword)
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

// HandleMachineByID 处理按 ID 更新（PUT）和删除（DELETE）机器请求，以及分配集群子路径。
func (h *MachineHandler) HandleMachineByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/machines/")
	if strings.HasSuffix(path, "/assign-cluster") {
		machineID := strings.TrimSuffix(path, "/assign-cluster")
		h.handleAssignCluster(w, r, strings.TrimSuffix(machineID, "/"))
		return
	}
	machineID := strings.Trim(path, "/")
	if machineID == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
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
		if err := h.service.DeleteMachine(r.Context(), machineID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"machine_id": machineID})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
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
		writeJSON(w, http.StatusOK, items)
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

// writeError 以 JSON 格式写入错误响应。
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
