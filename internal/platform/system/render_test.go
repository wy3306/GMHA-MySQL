package system

import (
	"strings"
	"testing"
	"time"
)

// TestRenderAgentEnv 测试 Agent 环境配置文件的渲染功能，验证输出包含必要的配置项。
func TestRenderAgentEnv(t *testing.T) {
	renderer := NewRenderer("http://127.0.0.1:8080", "/opt/gmha/bin", "/etc/gmha", "gmha-agent")
	got, err := renderer.RenderAgentEnv(AgentEnvData{
		HostID:            "host-1",
		BootstrapToken:    "token-1",
		ManagerURL:        "http://127.0.0.1:8080",
		HeartbeatInterval: 15 * time.Second,
		Version:           "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HOST_ID=host-1", "BOOTSTRAP_TOKEN=token-1", "MANAGER_URL=http://127.0.0.1:8080"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}
