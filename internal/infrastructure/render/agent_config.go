package render

import (
	"bytes"
	"text/template"

	agentusecase "gmha/internal/usecase/agent"
)

type Renderer struct{}

func NewRenderer() *Renderer {
	return &Renderer{}
}

func (r *Renderer) RenderAgentConfig(input agentusecase.AgentConfigRenderInput) ([]byte, error) {
	tpl := `agent_id: {{ .AgentID }}
machine_id: {{ .MachineID }}
machine_ip: {{ .MachineIP }}
install_dir: {{ .InstallDir }}
manager_mode: grpc
manager_http_addr: {{ .ManagerHTTPAddr }}
manager_grpc_addr: {{ .ManagerGRPCAddr }}
heartbeat_interval: {{ .HeartbeatInterval }}
token: {{ .Token }}
`
	return executeTemplate("agent-config", tpl, input)
}

func executeTemplate(name, src string, data any) ([]byte, error) {
	tpl, err := template.New(name).Parse(src)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
