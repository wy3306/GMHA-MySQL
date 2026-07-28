package agent

import (
	"context"
	"strings"
	"testing"

	machinedomain "gmha/internal/domain/machine"
)

type linuxPrecheckSSH struct {
	output string
}

func (s linuxPrecheckSSH) Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error {
	return nil
}
func (s linuxPrecheckSSH) RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error) {
	return []byte(s.output), nil
}
func (s linuxPrecheckSSH) Upload(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string, []byte, string) error {
	return nil
}

func TestAgentSystemdActivationEnablesBootStartupAndVerifiesState(t *testing.T) {
	command := strings.Join(agentSystemdActivationCommands(), " && ")
	for _, expected := range []string{
		"systemctl daemon-reload",
		"systemctl enable gmha-agent.service",
		"systemctl restart gmha-agent.service",
		"systemctl is-enabled --quiet gmha-agent.service",
		"systemctl is-active --quiet gmha-agent.service",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("systemd activation sequence does not contain %q: %s", expected, command)
		}
	}
}

func TestParseLinuxPrecheck(t *testing.T) {
	values := parseLinuxPrecheck([]byte("os=CentOS Linux 7\narch=x86_64\nglibc=2.17\nsystemd=yes\n"))
	if values["os"] != "CentOS Linux 7" || values["arch"] != "x86_64" || values["systemd"] != "yes" {
		t.Fatalf("unexpected Linux precheck values: %#v", values)
	}
}

func TestValidateLinuxTargetAcceptsCentOS7AndRejectsUnsupportedSystems(t *testing.T) {
	usecase := &InstallAgentUsecase{sshClient: linuxPrecheckSSH{output: "os=CentOS Linux 7\narch=x86_64\nglibc=2.17\nsystemd=yes\n"}}
	if err := usecase.validateLinuxTarget(context.Background(), machinedomain.Endpoint{}, machinedomain.SSHAuth{}, []byte("test fixture")); err != nil {
		t.Fatalf("CentOS 7 compatibility path should remain available: %v", err)
	}
	usecase.sshClient = linuxPrecheckSSH{output: "os=CentOS Linux 6\narch=x86_64\nglibc=2.12\nsystemd=yes\n"}
	if err := usecase.validateLinuxTarget(context.Background(), machinedomain.Endpoint{}, machinedomain.SSHAuth{}, []byte("test fixture")); err == nil {
		t.Fatal("CentOS 6 must be rejected before uploading the Agent")
	}
	usecase.sshClient = linuxPrecheckSSH{output: "os=Ubuntu 24.04\narch=x86_64\nglibc=2.39\nsystemd=no\n"}
	if err := usecase.validateLinuxTarget(context.Background(), machinedomain.Endpoint{}, machinedomain.SSHAuth{}, []byte("test fixture")); err == nil || !strings.Contains(err.Error(), "systemd") {
		t.Fatalf("non-systemd target must be rejected, got %v", err)
	}
}
