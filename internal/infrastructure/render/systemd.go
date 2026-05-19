package render

import agentusecase "gmha/internal/usecase/agent"

func (r *Renderer) RenderSystemd(input agentusecase.SystemdRenderInput) ([]byte, error) {
	tpl := `[Unit]
Description=GMHA Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory={{ .InstallDir }}
ExecStart={{ .InstallDir }}/agentd --config {{ .InstallDir }}/agent.yaml
Restart=always
RestartSec=5s
TimeoutStopSec=15s
KillMode=mixed
SendSIGKILL=yes

[Install]
WantedBy=multi-user.target
`
	return executeTemplate("agent-systemd", tpl, input)
}
