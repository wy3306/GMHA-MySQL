package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"gmha/internal/app"
)

// PackageHandler 提供安装包仓库的上传、下载、删除与存放位置管理接口。
type PackageHandler struct {
	service *app.PackageService
}

func NewPackageHandler(service *app.PackageService) *PackageHandler {
	return &PackageHandler{service: service}
}

func (h *PackageHandler) HandlePackages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.service.List(r.URL.Query().Get("category"), r.URL.Query().Get("keyword"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "settings": h.service.Settings()})
	case http.MethodPost:
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		defer file.Close()
		item, err := h.service.SaveUploadWithMetadata(r.FormValue("category"), r.FormValue("arch"), header.Filename, r.FormValue("version"), r.FormValue("description"), file)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// HandleFetchPackage 从内置的可信软件目录下载并入库。
func (h *PackageHandler) HandleFetchPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CatalogID string `json:"catalog_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.FetchCatalogPackage(r.Context(), req.CatalogID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// HandleFetchPackageBundle 一键下载 MySQL 本体及所选版本的推荐工具。
func (h *PackageHandler) HandleFetchPackageBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BundleID string `json:"bundle_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := h.service.FetchPackageBundle(r.Context(), req.BundleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := http.StatusCreated
	if !result.Complete {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (h *PackageHandler) HandleVerifyPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Category string `json:"category"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.service.Verify(req.Category, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *PackageHandler) HandlePackageByPath(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/packages/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusBadRequest, http.ErrMissingFile)
		return
	}
	switch r.Method {
	case http.MethodGet:
		path, err := h.service.Open(parts[0], parts[1])
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		http.ServeFile(w, r, path)
	case http.MethodDelete:
		if err := h.service.Delete(parts[0], parts[1]); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *PackageHandler) HandleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.Settings())
	case http.MethodPut:
		var req struct {
			StoragePath string `json:"storage_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.SetStoragePath(req.StoragePath); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, h.service.Settings())
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.service.SetStoragePath(r.FormValue("storage_path")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *PackageHandler) HandleDeleteForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.service.Delete(r.FormValue("category"), r.FormValue("name")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
