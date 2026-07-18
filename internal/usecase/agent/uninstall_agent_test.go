package agent

import (
	"context"
	"strings"
	"testing"

	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
)

type uninstallTestMachineRepo struct {
	machine machinedomain.Machine
}

func (r *uninstallTestMachineRepo) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	return r.machine, id == r.machine.ID, nil
}
func (r *uninstallTestMachineRepo) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}

type uninstallTestAgentRepo struct {
	agent   agentdomain.Agent
	deleted bool
}

func (r *uninstallTestAgentRepo) GetByMachineID(context.Context, string) (agentdomain.Agent, bool, error) {
	return r.agent, true, nil
}
func (r *uninstallTestAgentRepo) UpdateState(context.Context, string, agentdomain.State, string) error {
	return nil
}
func (r *uninstallTestAgentRepo) DeleteByMachineID(context.Context, string) error {
	r.deleted = true
	return nil
}

type uninstallTestSSHClient struct {
	verification string
}

func (c *uninstallTestSSHClient) Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error {
	return nil
}
func (c *uninstallTestSSHClient) RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error) {
	return []byte(c.verification), nil
}
func (c *uninstallTestSSHClient) Upload(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string, []byte, string) error {
	return nil
}

func TestAgentRemovalVerificationCommandChecksEveryResidue(t *testing.T) {
	command := agentRemovalVerificationCommand("/home/gmha/agent")
	for _, expected := range []string{
		agentRemovalVerifiedMarker,
		"systemctl is-active gmha-agent",
		"LoadState",
		"/proc/[0-9]*",
		"/home/gmha/agent/agentd",
		"/etc/systemd/system/gmha-agent.service",
		"dir_state=absent",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("verification command missing %q:\n%s", expected, command)
		}
	}
}

func TestUninstallDoesNotDeletePlatformRecordUntilRemoteVerificationPasses(t *testing.T) {
	machineRepo := &uninstallTestMachineRepo{machine: machinedomain.Machine{ID: "machine-1", IP: "192.0.2.40", SSHPort: 22, SSHUser: "root"}}
	agentRepo := &uninstallTestAgentRepo{agent: agentdomain.Agent{ID: "agent-machine-1", MachineID: "machine-1", InstallDir: "/home/gmha/agent"}}
	sshClient := &uninstallTestSSHClient{verification: agentRemovalVerifiedMarker + "clean=0 state=active load=loaded pids=42 units=- install_dir=absent"}
	usecase := NewUninstallAgentUsecase(UninstallDependencies{MachineRepo: machineRepo, AgentRepo: agentRepo, SSHClient: sshClient})

	if _, err := usecase.Execute(context.Background(), UninstallAgentRequest{MachineID: machineRepo.machine.ID}); err == nil || !strings.Contains(err.Error(), "复检失败") {
		t.Fatalf("expected verification failure, got %v", err)
	}
	if agentRepo.deleted {
		t.Fatal("Agent platform record must be retained while remote residues exist")
	}

	sshClient.verification = agentRemovalVerifiedMarker + "clean=1 state=inactive load=not-found pids=- units=- install_dir=absent"
	response, err := usecase.Execute(context.Background(), UninstallAgentRequest{MachineID: machineRepo.machine.ID})
	if err != nil || !response.Verified || !agentRepo.deleted {
		t.Fatalf("expected verified uninstall, response=%+v deleted=%v err=%v", response, agentRepo.deleted, err)
	}
}
