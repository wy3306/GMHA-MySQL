// Package launcher provides the small bootstrap Web application used to start
// the full GMHA Manager without requiring a terminal command.
package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Config describes the bootstrap server and the Manager process it controls.
type Config struct {
	Listen            string
	ManagerURL        string
	ManagerListen     string
	ManagerGRPCListen string
	ManagerBinary     string
	DataPath          string
	AgentBinary       string
	LogPath           string
	OpenBrowser       bool
}

// Controller owns Manager discovery and startup.
type Controller struct {
	cfg    Config
	client *http.Client
	mu     sync.Mutex
}

// NewController normalizes all paths so the release package can be launched
// from any working directory.
func NewController(cfg Config) (*Controller, error) {
	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = "127.0.0.1:8079"
	}
	if strings.TrimSpace(cfg.ManagerURL) == "" {
		cfg.ManagerURL = "auto"
	}
	if strings.TrimSpace(cfg.ManagerListen) == "" {
		cfg.ManagerListen = ":8080"
	}
	if strings.TrimSpace(cfg.ManagerGRPCListen) == "" {
		cfg.ManagerGRPCListen = ":9100"
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(executable)
	if strings.TrimSpace(cfg.ManagerBinary) == "" {
		cfg.ManagerBinary = filepath.Join(base, "gmha")
	}
	if strings.TrimSpace(cfg.DataPath) == "" {
		cfg.DataPath = filepath.Join(base, "data", "manager.db")
	}
	if strings.TrimSpace(cfg.AgentBinary) == "" {
		cfg.AgentBinary = filepath.Join(base, "bin", "agentd")
	}
	if strings.TrimSpace(cfg.LogPath) == "" {
		cfg.LogPath = filepath.Join(base, "logs", "manager.log")
	}
	for _, path := range []*string{&cfg.ManagerBinary, &cfg.DataPath, &cfg.AgentBinary, &cfg.LogPath} {
		if !filepath.IsAbs(*path) {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				return nil, err
			}
			*path = absolute
		}
	}
	return &Controller{cfg: cfg, client: &http.Client{Timeout: 1200 * time.Millisecond}}, nil
}

type statusResponse struct {
	Running    bool   `json:"running"`
	ManagerURL string `json:"manager_url"`
	LogPath    string `json:"log_path"`
	Message    string `json:"message"`
}

// Handler returns the bootstrap UI and its local control API.
func (c *Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", c.handleStatus)
	mux.HandleFunc("/api/start", c.handleStart)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, launcherHTML)
	})
	return mux
}

func (c *Controller) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	c.writeStatus(w, r, http.StatusOK, c.healthy(r.Context()), "")
}

func (c *Controller) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.healthy(r.Context()) {
		c.writeStatus(w, r, http.StatusOK, true, "Manager 已经在运行")
		return
	}
	if err := c.startManager(); err != nil {
		c.writeError(w, http.StatusBadRequest, err)
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if c.healthy(context.Background()) {
			c.writeStatus(w, r, http.StatusOK, true, "Manager 启动成功，可以进入控制台")
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	c.writeError(w, http.StatusGatewayTimeout, fmt.Errorf("Manager 未在 20 秒内就绪，请查看日志 %s", c.cfg.LogPath))
}

func (c *Controller) healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.managerHealthURL()+"/api/v1/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Controller) startManager() error {
	if info, err := os.Stat(c.cfg.ManagerBinary); err != nil {
		return fmt.Errorf("找不到 Manager 程序 %s: %w", c.cfg.ManagerBinary, err)
	} else if info.IsDir() {
		return fmt.Errorf("Manager 程序路径是目录: %s", c.cfg.ManagerBinary)
	}
	if err := os.MkdirAll(filepath.Dir(c.cfg.DataPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.cfg.LogPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(c.cfg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(c.cfg.ManagerBinary,
		"serve",
		"--listen", c.cfg.ManagerListen,
		"--grpc-listen", c.cfg.ManagerGRPCListen,
		"--db", c.cfg.DataPath,
		"--agent-binary", c.cfg.AgentBinary,
	)
	cmd.Dir = filepath.Dir(c.cfg.ManagerBinary)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (c *Controller) writeStatus(w http.ResponseWriter, r *http.Request, code int, running bool, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(statusResponse{Running: running, ManagerURL: c.browserManagerURL(r), LogPath: c.cfg.LogPath, Message: message})
}

func (c *Controller) managerHealthURL() string {
	_, port, err := net.SplitHostPort(c.cfg.ManagerListen)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port)
}

func (c *Controller) browserManagerURL(r *http.Request) string {
	if value := strings.TrimSpace(c.cfg.ManagerURL); value != "" && value != "auto" {
		return strings.TrimRight(value, "/")
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil || host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	_, port, err := net.SplitHostPort(c.cfg.ManagerListen)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (c *Controller) writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// Serve starts the bootstrap server and optionally opens the user's browser.
func (c *Controller) Serve() error {
	server := &http.Server{Addr: c.cfg.Listen, Handler: c.Handler(), ReadHeaderTimeout: 10 * time.Second}
	url := launcherURL(c.cfg.Listen)
	log.Printf("GMHA Web 启动器已运行: %s", url)
	if c.cfg.OpenBrowser {
		go func() {
			time.Sleep(350 * time.Millisecond)
			if err := OpenBrowser(url); err != nil {
				log.Printf("无法自动打开浏览器，请手动访问 %s: %v", url, err)
			}
		}()
	}
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func launcherURL(listen string) string {
	host := strings.TrimSpace(listen)
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	}
	return "http://" + host
}

// OpenBrowser uses the platform's normal URL opener without invoking a shell.
func OpenBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{url}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		command, args = "xdg-open", []string{url}
	}
	return exec.Command(command, args...).Start()
}

const launcherHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>GMHA 启动中心</title><style>
:root{color-scheme:dark;font-family:Inter,"PingFang SC","Microsoft YaHei",sans-serif;background:#07111f;color:#eaf2ff}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(circle at 20% 15%,#153a59 0,transparent 34%),radial-gradient(circle at 85% 85%,#173b31 0,transparent 30%),#07111f}.shell{width:min(780px,calc(100% - 32px));padding:42px;border:1px solid #28425c;border-radius:24px;background:rgba(10,24,40,.9);box-shadow:0 30px 90px #0008}.brand{display:flex;align-items:center;gap:16px}.mark{display:grid;place-items:center;width:56px;height:56px;border-radius:16px;background:linear-gradient(135deg,#43d8bd,#3287ff);font-weight:900;font-size:20px;color:#04131d}h1{margin:0;font-size:30px}p{color:#9fb4c9;line-height:1.7}.status{margin:34px 0 24px;padding:20px;border:1px solid #263f58;border-radius:16px;background:#091a2b;display:flex;justify-content:space-between;align-items:center}.lamp{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:9px;background:#64748b;box-shadow:0 0 0 5px #64748b20}.online .lamp{background:#42d6a4;box-shadow:0 0 0 5px #42d6a420}.actions{display:flex;gap:12px;flex-wrap:wrap}button,a{appearance:none;border:0;border-radius:12px;padding:13px 20px;font-size:15px;font-weight:700;text-decoration:none;cursor:pointer}.primary{background:linear-gradient(135deg,#43d8bd,#2c8cff);color:#04131d}.secondary{background:#182c42;color:#dcecff;border:1px solid #33506d}.disabled{opacity:.42;pointer-events:none}.meta{margin-top:26px;padding-top:20px;border-top:1px solid #22394f;font-size:13px;color:#7f98af;word-break:break-all}.message{min-height:24px;color:#80ddca}button:disabled{opacity:.55;cursor:wait}@media(max-width:600px){.shell{padding:26px}.status{align-items:flex-start;gap:14px;flex-direction:column}h1{font-size:25px}}
</style></head><body><main class="shell"><div class="brand"><div class="mark">GM</div><div><h1>GMHA 启动中心</h1><p style="margin:5px 0 0">启动 Manager，然后进入 MySQL 高可用管理控制台</p></div></div><section id="status" class="status"><div><b><i class="lamp"></i><span id="state">正在检测 Manager…</span></b><p id="detail" style="margin:5px 0 0">请稍候</p></div></section><div class="actions"><button id="start" class="primary" onclick="startManager()">启动 Manager</button><a id="open" class="secondary disabled" target="_blank" rel="noopener">进入 GMHA 控制台 →</a></div><p id="message" class="message"></p><div class="meta">Manager 日志：<span id="log">—</span></div></main><script>
const statusBox=document.querySelector('#status'),state=document.querySelector('#state'),detail=document.querySelector('#detail'),start=document.querySelector('#start'),openLink=document.querySelector('#open'),message=document.querySelector('#message'),logPath=document.querySelector('#log');
function render(data){statusBox.classList.toggle('online',data.running);state.textContent=data.running?'Manager 正常运行':'Manager 尚未启动';detail.textContent=data.running?'服务健康检查已通过':'点击下方按钮启动完整程序';start.disabled=data.running;start.textContent=data.running?'Manager 已启动':'启动 Manager';openLink.href=data.manager_url;openLink.classList.toggle('disabled',!data.running);logPath.textContent=data.log_path||'—';if(data.message)message.textContent=data.message}
async function refresh(){try{const r=await fetch('/api/status');render(await r.json())}catch(e){message.textContent='状态检查失败：'+e.message}}
async function startManager(){start.disabled=true;start.textContent='正在启动…';message.textContent='正在初始化数据库并启动服务，请稍候';try{const r=await fetch('/api/start',{method:'POST'}),data=await r.json();if(!r.ok)throw new Error(data.error||'启动失败');render(data)}catch(e){message.textContent='启动失败：'+e.message;start.disabled=false;start.textContent='重新启动'} }
refresh();setInterval(refresh,3000);
</script></body></html>`
