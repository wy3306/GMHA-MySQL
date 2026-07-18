package machine

import (
	"context"
	"testing"

	machinedomain "gmha/internal/domain/machine"
)

type onboardSSHClient struct {
	auth       machinedomain.SSHAuth
	testCalled bool
}

func (c *onboardSSHClient) CheckTrustConnection(_ context.Context, _ machinedomain.Endpoint, auth machinedomain.SSHAuth) (bool, error) {
	c.auth = auth
	return auth.PrivateKey == "credential-private-key", nil
}
func (c *onboardSSHClient) TestConnection(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth) error {
	c.testCalled = true
	return nil
}

type onboardMachineRepo struct{ saved machinedomain.Machine }

func (r *onboardMachineRepo) Save(_ context.Context, machine machinedomain.Machine) (machinedomain.Machine, error) {
	r.saved = machine
	return machine, nil
}
func (r *onboardMachineRepo) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}
func (r *onboardMachineRepo) GetByID(context.Context, string) (machinedomain.Machine, bool, error) {
	return machinedomain.Machine{}, false, nil
}
func (r *onboardMachineRepo) GetByIP(context.Context, string) (machinedomain.Machine, bool, error) {
	return machinedomain.Machine{}, false, nil
}
func (r *onboardMachineRepo) List(context.Context) ([]machinedomain.Machine, error) { return nil, nil }
func (r *onboardMachineRepo) UpdateBasics(context.Context, machinedomain.Machine) error {
	return nil
}
func (r *onboardMachineRepo) AssignCluster(context.Context, string, string) error { return nil }
func (r *onboardMachineRepo) RebindCluster(context.Context, string, string) error { return nil }
func (r *onboardMachineRepo) ClearCluster(context.Context, string) error          { return nil }
func (r *onboardMachineRepo) Delete(context.Context, string) error                { return nil }

func TestExecuteUsesCredentialPrivateKeyForExistingTrust(t *testing.T) {
	client := &onboardSSHClient{}
	repo := &onboardMachineRepo{}
	usecase := NewOnboardUsecase(Dependencies{MachineRepo: repo, SSHClient: client})

	response, err := usecase.Execute(context.Background(), OnboardMachineRequest{
		Name: "db01", IP: "192.0.2.10", SSHPort: 22, SSHUser: "root",
		SSHPrivateKey: "credential-private-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.auth.PrivateKey != "credential-private-key" {
		t.Fatalf("trust check did not receive credential private key: %#v", client.auth)
	}
	if client.testCalled {
		t.Fatal("password/setup connection should not run when credential key already has trust")
	}
	if response.Status != string(machinedomain.StatusSSHTrustReady) {
		t.Fatalf("status = %q, want %q", response.Status, machinedomain.StatusSSHTrustReady)
	}
}
