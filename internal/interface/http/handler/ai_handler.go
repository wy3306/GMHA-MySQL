package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gmha/internal/app"
	aidomain "gmha/internal/domain/ai"
)

type AIHandler struct{ service *app.AIService }

func NewAIHandler(service *app.AIService) *AIHandler { return &AIHandler{service: service} }

func (h *AIHandler) Handle(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/ai"), "/")
	switch path {
	case "":
		h.overview(w, r)
	case "capabilities":
		h.capabilities(w, r)
	case "providers":
		h.providers(w, r)
	case "providers/test":
		h.testProvider(w, r)
	case "settings":
		h.settings(w, r)
	case "sessions":
		h.sessions(w, r)
	case "sessions/archive":
		h.archiveSession(w, r)
	case "sessions/restore":
		h.restoreSession(w, r)
	case "memory":
		h.memory(w, r)
	case "chat":
		h.chat(w, r)
	case "analyze":
		h.analyze(w, r)
	case "plans/execute":
		h.executePlan(w, r)
	case "plans/reject":
		h.rejectPlan(w, r)
	case "workflows/pause":
		h.pauseWorkflow(w, r)
	case "workflows/resume":
		h.resumeWorkflow(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *AIHandler) sessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		includeArchived := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_archived")), "true")
		value, err := h.service.ListConversationSessions(r.Context(), includeArchived)
		writeAI(w, value, err)
	case http.MethodPost:
		var input struct {
			Title string `json:"title"`
		}
		if !decodeAI(w, r, &input) {
			return
		}
		value, err := h.service.CreateConversationSession(r.Context(), input.Title)
		writeAI(w, value, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *AIHandler) archiveSession(w http.ResponseWriter, r *http.Request) {
	h.changeSessionArchiveState(w, r, true)
}

func (h *AIHandler) restoreSession(w http.ResponseWriter, r *http.Request) {
	h.changeSessionArchiveState(w, r, false)
}

func (h *AIHandler) changeSessionArchiveState(w http.ResponseWriter, r *http.Request, archived bool) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	var (
		value aidomain.ConversationSession
		err   error
	)
	if archived {
		value, err = h.service.ArchiveConversationSession(r.Context(), input.ID)
	} else {
		value, err = h.service.RestoreConversationSession(r.Context(), input.ID)
	}
	writeAI(w, value, err)
}

func (h *AIHandler) memory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := h.service.GetSessionMemory(r.Context(), r.URL.Query().Get("session_id"))
		writeAI(w, value, err)
	case http.MethodPut:
		var input app.AISessionMemoryUpdate
		if !decodeAI(w, r, &input) {
			return
		}
		value, err := h.service.SaveSessionMemory(r.Context(), input)
		writeAI(w, value, err)
	case http.MethodDelete:
		err := h.service.DeleteSessionMemory(r.Context(), r.URL.Query().Get("session_id"))
		writeAI(w, map[string]bool{"deleted": err == nil}, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *AIHandler) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeAI(w, map[string]any{
		"api_version":       "v1",
		"actions":           app.AIActionCatalog(),
		"cluster_endpoints": app.ClusterAPICatalog(),
		"conversation_memory": map[string]any{
			"session_scoped":          true,
			"recent_message_limit":    16,
			"recent_character_limit":  24000,
			"rolling_summary":         true,
			"validated_active_intent": true,
			"endpoints": []string{
				"GET,POST /api/v1/ai/sessions",
				"POST /api/v1/ai/sessions/archive",
				"POST /api/v1/ai/sessions/restore",
				"GET,PUT,DELETE /api/v1/ai/memory",
			},
		},
		"security_boundary": "secure_input_api 参数只能通过受保护表单或密钥通道提交，不得写入模型对话",
	}, nil)
}

func (h *AIHandler) pauseWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.PauseWorkflow(r.Context(), input.ID)
	writeAI(w, value, err)
}

func (h *AIHandler) resumeWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.ResumeWorkflow(r.Context(), input.ID)
	writeAI(w, value, err)
}

func (h *AIHandler) overview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	value, err := h.service.Overview(r.Context())
	writeAI(w, value, err)
}

func (h *AIHandler) providers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		var input aidomain.Provider
		if !decodeAI(w, r, &input) {
			return
		}
		value, err := h.service.SaveProvider(r.Context(), input)
		writeAI(w, value, err)
	case http.MethodDelete:
		err := h.service.DeleteProvider(r.Context(), strings.TrimSpace(r.URL.Query().Get("id")))
		writeAI(w, map[string]bool{"deleted": err == nil}, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *AIHandler) testProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	err := h.service.TestProvider(r.Context(), input.ID)
	writeAI(w, map[string]string{"status": "connected"}, err)
}

func (h *AIHandler) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input aidomain.Settings
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.SaveSettings(r.Context(), input)
	writeAI(w, value, err)
}

func (h *AIHandler) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		SessionID  string `json:"session_id"`
		ProviderID string `json:"provider_id"`
		Message    string `json:"message"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.Chat(r.Context(), input.SessionID, input.ProviderID, input.Message)
	writeAI(w, value, err)
}

func (h *AIHandler) analyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ProviderID string `json:"provider_id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.AnalyzeNow(r.Context(), "manual", input.ProviderID)
	writeAI(w, value, err)
}

func (h *AIHandler) executePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID           string `json:"id"`
		Confirmation string `json:"confirmation"`
		Approved     bool   `json:"approved"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.ExecutePlan(r.Context(), input.ID, input.Confirmation, input.Approved)
	writeAI(w, value, err)
}

func (h *AIHandler) rejectPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAI(w, r, &input) {
		return
	}
	value, err := h.service.RejectPlan(r.Context(), input.ID)
	writeAI(w, value, err)
}

func decodeAI(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeAI(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, aidomain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, aidomain.ErrConflict):
		writeError(w, http.StatusConflict, errors.New("计划状态已变化，请刷新页面后重试"))
		return
	case strings.Contains(err.Error(), "模型连接失败"), strings.Contains(err.Error(), "模型返回 HTTP"):
		status = http.StatusBadGateway
	}
	writeError(w, status, err)
}
