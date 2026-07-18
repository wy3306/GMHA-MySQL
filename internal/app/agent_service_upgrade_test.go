package app

import (
	"context"
	"strings"
	"testing"
	"time"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
)

type versionAgentRepo struct{ agent agentdomain.Agent }

func (r *versionAgentRepo) Save(_ context.Context, item agentdomain.Agent) (agentdomain.Agent, error) {
	r.agent = item
	return item, nil
}
func (r *versionAgentRepo) GetByMachineID(context.Context, string) (agentdomain.Agent, bool, error) {
	return r.agent, r.agent.MachineID != "", nil
}
func (r *versionAgentRepo) List(context.Context) ([]agentdomain.Agent, error) {
	if r.agent.MachineID == "" {
		return nil, nil
	}
	return []agentdomain.Agent{r.agent}, nil
}
func (r *versionAgentRepo) UpdateState(context.Context, string, agentdomain.State, string) error {
	return nil
}
func (r *versionAgentRepo) MarkRegistered(context.Context, string, time.Time) error  { return nil }
func (r *versionAgentRepo) UpdateHeartbeat(context.Context, string, time.Time) error { return nil }
func (r *versionAgentRepo) DeleteByMachineID(context.Context, string) error          { return nil }

type versionSSHClient struct{ command string }

func (*versionSSHClient) Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error {
	return nil
}
func (c *versionSSHClient) RunOutput(_ context.Context, _ machinedomain.Endpoint, _ machinedomain.SSHAuth, command string) ([]byte, error) {
	c.command = command
	return []byte("gmha-agent V1.4.2\n"), nil
}
func (*versionSSHClient) Upload(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string, []byte, string) error {
	return nil
}

func TestAgentUpgradeGuardRejectsDuplicateAndReleases(t *testing.T) {
	service := &AgentService{}
	if !service.beginAgentUpgrade("machine-1") {
		t.Fatal("first upgrade should acquire the guard")
	}
	if service.beginAgentUpgrade("machine-1") {
		t.Fatal("duplicate upgrade should be rejected")
	}
	if !service.beginAgentUpgrade("machine-2") {
		t.Fatal("a different machine should be allowed to upgrade concurrently")
	}
	service.finishAgentUpgrade("machine-1")
	if !service.beginAgentUpgrade("machine-1") {
		t.Fatal("guard should be released after the upgrade finishes")
	}
}

func TestDetectVersionByIPPersistsRemoteBinaryVersion(t *testing.T) {
	machine := machinedomain.Machine{ID: "machine-1", Name: "DB-01", IP: "192.0.2.10", SSHPort: 22, SSHUser: "root", AgentInstallDir: "/opt/gmha/agent"}
	machineRepo := &detachMachineRepo{machine: machine}
	agentRepo := &versionAgentRepo{agent: agentdomain.Agent{ID: "agent-machine-1", MachineID: machine.ID, InstallDir: "/opt/gmha/agent", State: agentdomain.StateOffline}}
	sshClient := &versionSSHClient{}
	service := NewAgentService(agentRepo, machineRepo, sshClient, nil, nil, nil, nil, nil, nil, nil, "", "", "")

	view, err := service.DetectVersionByIP(context.Background(), machine.IP)
	if err != nil {
		t.Fatalf("detect Agent version: %v", err)
	}
	if view.Version != "V1.4.2" || agentRepo.agent.Version != "V1.4.2" {
		t.Fatalf("version was not persisted and returned: view=%q repo=%q", view.Version, agentRepo.agent.Version)
	}
	if !strings.Contains(sshClient.command, "'/opt/gmha/agent/agentd' --version") {
		t.Fatalf("unexpected version detection command: %q", sshClient.command)
	}
}
