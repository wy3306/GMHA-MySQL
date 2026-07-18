package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
)

func TestCandidateSelectorDataFreshnessBeatsElectionPriority(t *testing.T) {
	selector := NewCandidateSelector()
	winner, ranked, err := selector.Select([]hadomain.CandidateScore{
		{InstanceID: "fresh", Eligible: true, DataFreshnessScore: 10, RelayReceivedScore: 10, RelayExecutedScore: 10, ElectionPriority: 0},
		{InstanceID: "stale-priority", Eligible: true, DataFreshnessScore: 9, RelayReceivedScore: 10, RelayExecutedScore: 10, ElectionPriority: 1000000},
	})
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if winner.InstanceID != "fresh" {
		t.Fatalf("winner = %s, ranked=%+v", winner.InstanceID, ranked)
	}
}

func TestRelayReplayCompleteFailsOnSQLErrorAndLag(t *testing.T) {
	if err := RelayReplayComplete(RelayStatus{SQLRunning: true, LastSQLError: "duplicate key"}); err == nil {
		t.Fatal("expected SQL error")
	}
	if err := RelayReplayComplete(RelayStatus{SQLRunning: true, DelaySeconds: 1}); err == nil {
		t.Fatal("expected delay error")
	}
	if err := RelayReplayComplete(RelayStatus{SQLRunning: true, ExecMasterLogPos: 10, ReadMasterLogPos: 20}); err == nil {
		t.Fatal("expected relay position error")
	}
	if err := RelayReplayComplete(RelayStatus{SQLRunning: true, DelaySeconds: 0, ExecMasterLogPos: 20, ReadMasterLogPos: 20}); err != nil {
		t.Fatalf("expected complete relay replay: %v", err)
	}
}

func TestVIPInterfaceSelectorDoesNotDefaultEth0(t *testing.T) {
	selector := &VIPInterfaceSelector{repo: fakeHARepo{
		network: hadomain.NetworkPolicy{ClusterID: "c1", NetworkTopology: "L2", VIPRouteMode: "L2_ARP", AutoDetectVIPInterface: true},
		ifaces:  []hadomain.MachineNetworkInterface{{MachineID: "m1", InterfaceName: "eth0", IsUp: true}},
	}}
	_, err := selector.Select(context.Background(), SelectVIPInterfaceRequest{ClusterID: "c1", MachineID: "m1", VIPAddress: "10.0.0.10"})
	if err == nil || !strings.Contains(err.Error(), "unable to determine VIP interface") {
		t.Fatalf("expected explicit interface error, got %v", err)
	}
}

func TestBGPDriverRequiresArchitectureStateMachine(t *testing.T) {
	driver := ArchitectureManagedVIPDriver{Mode: hadomain.VipRouteModeBGP}
	_, err := driver.Move(context.Background(), MoveVipRequest{VIP: "10.0.0.10", ToInterface: "eth1"})
	if err == nil || !strings.Contains(err.Error(), "architecture adjustment state machine") {
		t.Fatalf("expected architecture state-machine guard, got %v", err)
	}
}

func TestResolveAutomaticVIPModeHidesLegacyInput(t *testing.T) {
	service := NewHAService(fakeHARepo{network: hadomain.NetworkPolicy{NetworkTopology: "L2"}}, nil, nil)
	cfg, err := service.resolveAutomaticVIPMode(context.Background(), "demo", hadomain.ClusterVIPConfig{VIPRouteMode: "KEEPALIVED"})
	if err != nil {
		t.Fatalf("resolve automatic VIP mode: %v", err)
	}
	if cfg.VIPRouteMode != hadomain.VipRouteModeL2ARP || !cfg.ArpingEnabled || cfg.BGPEnabled {
		t.Fatalf("unexpected automatic L2 config: %+v", cfg)
	}
}

func TestResolveAutomaticVIPModeRejectsIncompleteL3Policy(t *testing.T) {
	service := NewHAService(fakeHARepo{network: hadomain.NetworkPolicy{NetworkTopology: "L3"}}, nil, nil)
	_, err := service.resolveAutomaticVIPMode(context.Background(), "demo", hadomain.ClusterVIPConfig{})
	if err == nil || !strings.Contains(err.Error(), "BGP") {
		t.Fatalf("expected missing BGP policy error, got %v", err)
	}
}

func TestVIPScanInterfaceParsesAgentOutput(t *testing.T) {
	detail := TaskDetail{Steps: []taskdomain.Step{{Message: "command output\n" + vipScanMarker + "ens192\n"}}}
	if got := vipScanInterface(detail); got != "ens192" {
		t.Fatalf("interface = %q, want ens192", got)
	}
	detail.Steps[0].Message = vipScanMarker + "UNBOUND\n"
	if got := vipScanInterface(detail); got != "" {
		t.Fatalf("unbound scan returned interface %q", got)
	}
}

func TestArchitectureVIPScopeIncludesEveryClusterMachine(t *testing.T) {
	machines := []machinedomain.Machine{
		{ID: "selected", Cluster: "demo"},
		{ID: "hidden-holder", Cluster: "demo"},
		{ID: "other-cluster", Cluster: "other"},
	}
	service := NewHAService(fakeHARepo{}, vipScopeMachineRepo{items: machines}, nil)
	got, err := service.allClusterVIPMachines(context.Background(), "demo", map[string]machinedomain.Machine{"selected": machines[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["hidden-holder"].ID == "" || got["other-cluster"].ID != "" {
		t.Fatalf("unexpected VIP safety scope: %+v", got)
	}
}

type vipScopeMachineRepo struct{ items []machinedomain.Machine }

func (r vipScopeMachineRepo) Save(context.Context, machinedomain.Machine) (machinedomain.Machine, error) {
	return machinedomain.Machine{}, nil
}
func (r vipScopeMachineRepo) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}
func (r vipScopeMachineRepo) GetByID(context.Context, string) (machinedomain.Machine, bool, error) {
	return machinedomain.Machine{}, false, nil
}
func (r vipScopeMachineRepo) GetByIP(context.Context, string) (machinedomain.Machine, bool, error) {
	return machinedomain.Machine{}, false, nil
}
func (r vipScopeMachineRepo) List(context.Context) ([]machinedomain.Machine, error) {
	return r.items, nil
}
func (r vipScopeMachineRepo) UpdateBasics(context.Context, machinedomain.Machine) error { return nil }
func (r vipScopeMachineRepo) AssignCluster(context.Context, string, string) error       { return nil }
func (r vipScopeMachineRepo) RebindCluster(context.Context, string, string) error       { return nil }
func (r vipScopeMachineRepo) ClearCluster(context.Context, string) error                { return nil }
func (r vipScopeMachineRepo) Delete(context.Context, string) error                      { return nil }

type fakeHARepo struct {
	network hadomain.NetworkPolicy
	ifaces  []hadomain.MachineNetworkInterface
	vips    []hadomain.ClusterVIPConfig
}

func (f fakeHARepo) EnsureDefaultPolicies(context.Context, string) error { return nil }
func (f fakeHARepo) GetFailoverPolicy(context.Context, string) (hadomain.FailoverPolicy, error) {
	return hadomain.FailoverPolicy{}, errors.New("not implemented")
}
func (f fakeHARepo) GetNetworkPolicy(context.Context, string) (hadomain.NetworkPolicy, error) {
	return f.network, nil
}
func (f fakeHARepo) ListVIPConfigs(context.Context, string) ([]hadomain.ClusterVIPConfig, error) {
	return f.vips, nil
}
func (f fakeHARepo) UpsertVIPBindingState(context.Context, hadomain.VIPBindingState) error {
	return errors.New("not implemented")
}
func (f fakeHARepo) GetVIPBindingStates(context.Context, string) ([]hadomain.VIPBindingState, error) {
	return nil, errors.New("not implemented")
}
func (f fakeHARepo) ListMachineInterfaces(context.Context, string) ([]hadomain.MachineNetworkInterface, error) {
	return f.ifaces, nil
}
func (f fakeHARepo) AcquireFailoverLock(context.Context, string, string, string, time.Duration) error {
	return errors.New("not implemented")
}
func (f fakeHARepo) RenewFailoverLock(context.Context, string, string, time.Duration) error {
	return errors.New("not implemented")
}
func (f fakeHARepo) ReleaseFailoverLock(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (f fakeHARepo) SaveFailoverEvent(context.Context, hadomain.FailoverEvent) error {
	return errors.New("not implemented")
}
func (f fakeHARepo) GetFailoverEvent(context.Context, string, string) (hadomain.FailoverEvent, bool, error) {
	return hadomain.FailoverEvent{}, false, errors.New("not implemented")
}
func (f fakeHARepo) InsertVIPOperationLog(context.Context, string, string, string, string, string, string, string, string, int, string, string, string, string) error {
	return errors.New("not implemented")
}
