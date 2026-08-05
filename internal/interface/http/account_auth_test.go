package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serveAccountRequest(router http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func responseSessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "gmha_session" {
			return cookie
		}
	}
	t.Fatal("response did not contain gmha_session")
	return nil
}

func TestPlatformLoginAccountPermissionsAndLogout(t *testing.T) {
	core := newRouterSmokeApp(t)
	router := NewRouter(core)

	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/machines", "", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API returned %d: %s", got.Code, got.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/software/packages/mysql-shell/missing.tar.gz", "", nil); got.Code == http.StatusUnauthorized {
		t.Fatalf("Agent package download was incorrectly protected by a browser session: %s", got.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodDelete, "/api/v1/software/packages/mysql-shell/missing.tar.gz", "", nil); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Agent package endpoint accepted a mutating method: %d %s", got.Code, got.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login returned %d: %s", got.Code, got.Body.String())
	}
	login := serveAccountRequest(router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("admin login returned %d: %s", login.Code, login.Body.String())
	}
	adminCookie := responseSessionCookie(t, login)
	if adminCookie.MaxAge != 7200 || !adminCookie.HttpOnly || adminCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %#v", adminCookie)
	}

	created := serveAccountRequest(router, http.MethodPost, "/api/v1/accounts", `{"username":"dbauser","name":"数据库管理员","email":"dba@example.com","phone":"13800000000","password":"password-123","role":"dba","permissions":["overview.view","database.manage"],"enabled":true}`, adminCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create account returned %d: %s", created.Code, created.Body.String())
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &user); err != nil || user.ID == "" {
		t.Fatalf("invalid created account: %v %s", err, created.Body.String())
	}

	dbaLogin := serveAccountRequest(router, http.MethodPost, "/api/v1/auth/login", `{"username":"dbauser","password":"password-123"}`, nil)
	if dbaLogin.Code != http.StatusOK {
		t.Fatalf("dba login returned %d: %s", dbaLogin.Code, dbaLogin.Body.String())
	}
	dbaCookie := responseSessionCookie(t, dbaLogin)
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/mysql/instances", "", dbaCookie); got.Code != http.StatusOK {
		t.Fatalf("allowed database API returned %d: %s", got.Code, got.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/machines", "", dbaCookie); got.Code != http.StatusForbidden {
		t.Fatalf("forbidden resource API returned %d: %s", got.Code, got.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/accounts", "", dbaCookie); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin account list returned %d: %s", got.Code, got.Body.String())
	}
	directory := serveAccountRequest(router, http.MethodGet, "/api/v1/alerts/recipient-directory", "", adminCookie)
	if directory.Code != http.StatusOK || !bytes.Contains(directory.Body.Bytes(), []byte(`"username":"dbauser"`)) {
		t.Fatalf("alert recipient directory returned %d: %s", directory.Code, directory.Body.String())
	}
	if bytes.Contains(directory.Body.Bytes(), []byte("dba@example.com")) {
		t.Fatalf("recipient directory exposed an email address: %s", directory.Body.String())
	}
	channel := serveAccountRequest(router, http.MethodPost, "/api/v1/alerts/channels", `{"name":"DBA邮箱","type":"email","enabled":true,"minimum_severity":"warning","recipient_roles":["dba"],"content_filter":{"event_states":["firing","resolved"]},"config":{"host":"smtp.example.com","port":"465","username":"sender@example.com","password":"secret","from":"sender@example.com"}}`, adminCookie)
	if channel.Code != http.StatusOK {
		t.Fatalf("role-routed email channel returned %d: %s", channel.Code, channel.Body.String())
	}
	if bytes.Contains(channel.Body.Bytes(), []byte("dba@example.com")) || bytes.Contains(channel.Body.Bytes(), []byte(`"to"`)) {
		t.Fatalf("email channel response exposed resolved recipients: %s", channel.Body.String())
	}

	logout := serveAccountRequest(router, http.MethodPost, "/api/v1/auth/logout", `{}`, dbaCookie)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout returned %d: %s", logout.Code, logout.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/mysql/instances", "", dbaCookie); got.Code != http.StatusUnauthorized {
		t.Fatalf("logged out session returned %d: %s", got.Code, got.Body.String())
	}
}

func TestDemoAutoLoginAdminRequiresExplicitEnvironmentSwitch(t *testing.T) {
	t.Setenv("GMHA_DEMO_AUTO_LOGIN_ADMIN", "true")
	core := newRouterSmokeApp(t)
	router := NewRouter(core)

	session := serveAccountRequest(router, http.MethodGet, "/api/v1/auth/session", "", nil)
	if session.Code != http.StatusOK {
		t.Fatalf("demo auto login returned %d: %s", session.Code, session.Body.String())
	}
	cookie := responseSessionCookie(t, session)
	if !cookie.HttpOnly || cookie.MaxAge != int(2*time.Hour/time.Second) {
		t.Fatalf("unexpected demo session cookie: %#v", cookie)
	}
	var payload struct {
		User struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &payload); err != nil || payload.User.Username != "admin" || payload.User.Role != "admin" {
		t.Fatalf("unexpected demo session payload: %v %s", err, session.Body.String())
	}
	if got := serveAccountRequest(router, http.MethodGet, "/api/v1/machines", "", cookie); got.Code != http.StatusOK {
		t.Fatalf("demo admin session was not accepted: %d %s", got.Code, got.Body.String())
	}
}
