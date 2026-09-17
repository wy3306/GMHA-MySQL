package agent

import (
	"context"
	"errors"
	agentdomain "gmha/internal/domain/agent"
	machinedomain "gmha/internal/domain/machine"
	"strings"
	"testing"
	"time"
)

type upgradeVersionMachine struct{}

func (upgradeVersionMachine) GetByID(context.Context, string) (machinedomain.Machine, bool, error) {
	return machinedomain.Machine{ID: "m", IP: "192.0.2.1", SSHUser: "root", Status: machinedomain.StatusAgentError}, true, nil
}
func (upgradeVersionMachine) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}

type upgradeVersionRepo struct{ item agentdomain.Agent }

func (r *upgradeVersionRepo) GetByMachineID(context.Context, string) (agentdomain.Agent, bool, error) {
	return r.item, true, nil
}
func (r *upgradeVersionRepo) Save(_ context.Context, a agentdomain.Agent) (agentdomain.Agent, error) {
	r.item = a
	return a, nil
}
func (r *upgradeVersionRepo) UpdateState(context.Context, string, agentdomain.State, string) error {
	return nil
}

type upgradeVersionRenderer struct{}

func (upgradeVersionRenderer) RenderAgentConfig(AgentConfigRenderInput) ([]byte, error) {
	return []byte("config"), nil
}
func (upgradeVersionRenderer) RenderSystemd(SystemdRenderInput) ([]byte, error) {
	return []byte("unit"), nil
}

type upgradeVersionSSH struct {
	version  string
	commands []string
}

func (s *upgradeVersionSSH) Run(_ context.Context, _ machinedomain.Endpoint, _ machinedomain.SSHAuth, cmd string) error {
	s.commands = append(s.commands, cmd)
	return nil
}
func (s *upgradeVersionSSH) RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error) {
	return []byte(s.version), nil
}
func (s *upgradeVersionSSH) Upload(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string, []byte, string) error {
	return nil
}

type upgradeVersionHeartbeat struct{ err error }

func (h upgradeVersionHeartbeat) WaitForFreshHeartbeat(context.Context, string, time.Time, time.Duration) error {
	return nil
}
func (h upgradeVersionHeartbeat) WaitForVersionHeartbeat(context.Context, string, string, time.Time, time.Duration) error {
	return h.err
}
func TestUpgradeConfirmsCandidateAndReportedVersion(t *testing.T) {
	for _, tc := range []struct {
		name, probe string
		hbErr       error
		success     bool
	}{
		{"mislabeled package", "V1.0.0", nil, false},
		{"old heartbeat", "V1.1.0", errors.New("old process heartbeat"), false},
		{"offline SSH upgrade", "V1.1.0", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &upgradeVersionRepo{item: agentdomain.Agent{ID: "a", MachineID: "m", Version: "V1.0.0", InstallDir: "/opt/agent"}}
			ssh := &upgradeVersionSSH{version: tc.probe}
			u := NewUpgradeAgentUsecase(UpgradeDependencies{MachineRepo: upgradeVersionMachine{}, AgentRepo: repo, SSHClient: ssh, Renderer: upgradeVersionRenderer{}, Heartbeat: upgradeVersionHeartbeat{tc.hbErr}})
			_, err := u.Execute(context.Background(), UpgradeAgentRequest{MachineID: "m", Version: "V1.1.0", ManagerGRPCAddr: "manager:9100", ManagerHTTPAddr: "http://manager:8080"}, []byte("candidate"))
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result %v", err)
			}
			want := "V1.0.0"
			if tc.success {
				want = "V1.1.0"
			}
			if repo.item.Version != want {
				t.Fatalf("record lies about installed version: %s", repo.item.Version)
			}
			if tc.probe == "V1.0.0" {
				for _, cmd := range ssh.commands {
					if strings.Contains(cmd, "mv -f") {
						t.Fatal("mislabeled candidate replaced installed binary")
					}
				}
			}
		})
	}
}
