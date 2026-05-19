// Package system 提供系统配置文件的模板渲染功能，用于生成 Agent 环境配置和 systemd 服务单元文件。
package system

import (
	"bytes"
	"text/template"
	"time"
)

// Renderer 是配置文件渲染器，根据管理端配置生成 Agent 所需的各类配置文件。
type Renderer struct {
	managerURL       string
	agentInstallDir  string
	agentConfigDir   string
	agentServiceName string
}

// NewRenderer 创建一个新的配置渲染器实例。
func NewRenderer(managerURL, agentInstallDir, agentConfigDir, agentServiceName string) *Renderer {
	return &Renderer{
		managerURL:       managerURL,
		agentInstallDir:  agentInstallDir,
		agentConfigDir:   agentConfigDir,
		agentServiceName: agentServiceName,
	}
}

// AgentEnvData 是渲染 Agent 环境配置文件所需的模板数据。
type AgentEnvData struct {
	HostID            string
	BootstrapToken    string
	ManagerURL        string
	HeartbeatInterval time.Duration
	Version           string
}

// RenderAgentEnv 渲染生成 Agent 的环境变量配置文件内容。
func (r *Renderer) RenderAgentEnv(data AgentEnvData) (string, error) {
	tpl := `HOST_ID={{ .HostID }}
BOOTSTRAP_TOKEN={{ .BootstrapToken }}
MANAGER_URL={{ .ManagerURL }}
HEARTBEAT_INTERVAL={{ .HeartbeatInterval }}
VERSION={{ .Version }}
`
	return executeTemplate(tpl, data)
}

// RenderSystemdUnit 渲染生成 systemd 服务单元文件内容，用于注册 GMHA Agent 为系统服务。
func (r *Renderer) RenderSystemdUnit() (string, error) {
	data := struct {
		ServiceName string
		InstallDir  string
		ConfigDir   string
	}{
		ServiceName: r.agentServiceName,
		InstallDir:  r.agentInstallDir,
		ConfigDir:   r.agentConfigDir,
	}
	tpl := `[Unit]
Description=GMHA Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile={{ .ConfigDir }}/agent.env
ExecStart={{ .InstallDir }}/agent --config {{ .ConfigDir }}/agent.env
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
`
	return executeTemplate(tpl, data)
}

// executeTemplate 通用模板执行函数，解析模板源码并用给定数据渲染输出。
func executeTemplate(src string, data any) (string, error) {
	tpl, err := template.New("tpl").Parse(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
