package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gmha/internal/app"
	accountdomain "gmha/internal/domain/account"
)

const platformSessionCookie = "gmha_session"

type accountContextKey struct{}

type AccountHandler struct{ accounts *app.AccountService }

func NewAccountHandler(accounts *app.AccountService) *AccountHandler {
	return &AccountHandler{accounts: accounts}
}

func (h *AccountHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct{ Username, Password string }
	if !decodeAccount(w, r, &input) {
		return
	}
	token, user, expires, err := h.accounts.Login(r.Context(), input.Username, input.Password)
	if err != nil {
		writeAccountError(w, http.StatusUnauthorized, err)
		return
	}
	setPlatformSessionCookie(w, r, token, expires)
	writeAccountJSON(w, http.StatusOK, map[string]any{"user": user, "expires_at": expires})
}

func (h *AccountHandler) HandleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, err := h.userFromRequest(r)
	if err != nil {
		token, demoUser, expires, demoErr := h.accounts.DemoAutoLoginAdmin(r.Context())
		if demoErr != nil {
			writeAccountError(w, http.StatusUnauthorized, errors.New("登录已失效，请重新登录"))
			return
		}
		setPlatformSessionCookie(w, r, token, expires)
		user = demoUser
	}
	writeAccountJSON(w, http.StatusOK, map[string]any{"user": user})
}

func setPlatformSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: platformSessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: int(app.PlatformSessionDuration.Seconds()), Expires: expires})
}

func (h *AccountHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(platformSessionCookie); err == nil {
		_ = h.accounts.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: platformSessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: requestUsesHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	writeAccountJSON(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (h *AccountHandler) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	user, ok := CurrentAccount(r.Context())
	if !ok || !app.CanManageAccounts(user) {
		writeAccountError(w, http.StatusForbidden, errors.New("无账号管理权限"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/accounts")
	if path == "/catalog" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeAccountJSON(w, http.StatusOK, h.accounts.Catalog())
		return
	}
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			items, err := h.accounts.List(r.Context(), user)
			if err != nil {
				writeAccountError(w, http.StatusForbidden, err)
				return
			}
			writeAccountJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
		case http.MethodPost:
			var input app.AccountInput
			if !decodeAccount(w, r, &input) {
				return
			}
			item, err := h.accounts.Create(r.Context(), user, input)
			if err != nil {
				writeAccountError(w, http.StatusBadRequest, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	id := strings.Trim(path, "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input app.AccountInput
		if !decodeAccount(w, r, &input) {
			return
		}
		item, err := h.accounts.Update(r.Context(), user, id, input)
		if err != nil {
			writeAccountError(w, accountErrorStatus(err), err)
			return
		}
		writeAccountJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if err := h.accounts.Delete(r.Context(), user, id); err != nil {
			writeAccountError(w, accountErrorStatus(err), err)
			return
		}
		writeAccountJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *AccountHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") || publicPlatformPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, err := h.userFromRequest(r)
		if err != nil {
			writeAccountError(w, http.StatusUnauthorized, errors.New("请先登录"))
			return
		}
		permission := permissionForPlatformPath(r.URL.Path)
		if permission != "" && !app.HasPlatformPermission(user, permission) {
			writeAccountError(w, http.StatusForbidden, errors.New("当前账号没有该功能权限"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountContextKey{}, user)))
	})
}

func CurrentAccount(ctx context.Context) (accountdomain.User, bool) {
	user, ok := ctx.Value(accountContextKey{}).(accountdomain.User)
	return user, ok
}

func (h *AccountHandler) userFromRequest(r *http.Request) (accountdomain.User, error) {
	cookie, err := r.Cookie(platformSessionCookie)
	if err != nil {
		return accountdomain.User{}, err
	}
	return h.accounts.Authenticate(r.Context(), cookie.Value)
}

func requestUsesHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func publicPlatformPath(path string) bool {
	return path == "/api/v1/healthz" || path == "/api/v1/auth/login" || path == "/api/v1/auth/session" || path == "/api/v1/auth/logout" || path == "/api/v1/agents/register" || path == "/api/v1/agents/heartbeat" || strings.HasPrefix(path, "/api/v1/software/") || strings.HasPrefix(path, "/api/v1/alerts/export/")
}

func permissionForPlatformPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/accounts"):
		return "accounts.manage"
	case strings.HasPrefix(path, "/api/v1/machines"), strings.HasPrefix(path, "/api/v1/ssh-credentials"), strings.HasPrefix(path, "/api/v1/agents"):
		return "resources.manage"
	case strings.HasPrefix(path, "/api/v1/clusters"), strings.HasPrefix(path, "/api/v1/backup"):
		return "clusters.manage"
	case strings.HasPrefix(path, "/api/v1/mysql"), strings.HasPrefix(path, "/api/v1/sql-diagnostics"), strings.HasPrefix(path, "/api/v1/performance"), strings.HasPrefix(path, "/api/v1/dynamic-collect"), strings.HasPrefix(path, "/api/v1/mysql-dynamic-collect"):
		return "database.manage"
	case strings.HasPrefix(path, "/api/v1/packages"), strings.HasPrefix(path, "/api/v1/package-settings"):
		return "database.manage"
	case strings.HasPrefix(path, "/api/v1/alerts"):
		return "alerts.manage"
	case strings.HasPrefix(path, "/api/v1/ai"):
		return "automation.manage"
	case strings.HasPrefix(path, "/api/v1/tasks"):
		return "tasks.manage"
	case strings.HasPrefix(path, "/api/v1/manager"), strings.HasPrefix(path, "/api/v1/upgrades"):
		return "platform.manage"
	default:
		return "overview.view"
	}
}

func decodeAccount(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAccountError(w, http.StatusBadRequest, errors.New("请求内容格式不正确"))
		return false
	}
	return true
}

func accountErrorStatus(err error) int {
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound
	}
	if strings.Contains(err.Error(), "无账号管理权限") || strings.Contains(err.Error(), "只能由") {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func writeAccountError(w http.ResponseWriter, status int, err error) {
	writeAccountJSON(w, status, map[string]string{"error": err.Error()})
}
func writeAccountJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
