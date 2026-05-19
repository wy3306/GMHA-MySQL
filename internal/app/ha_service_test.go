package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hadomain "gmha/internal/domain/ha"
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

func TestBGPDriverIsNotImplemented(t *testing.T) {
	driver := NotImplementedVipDriver{Mode: hadomain.VipRouteModeBGP, Message: "BGP VIP driver is not implemented; automatic failover is blocked to avoid unsafe L2 VIP drift across L3 network."}
	_, err := driver.Move(context.Background(), MoveVipRequest{VIP: "10.0.0.10", ToInterface: "eth1"})
	if err == nil || !strings.Contains(err.Error(), "BGP VIP driver is not implemented") {
		t.Fatalf("expected BGP not implemented error, got %v", err)
	}
}

type fakeHARepo struct {
	network hadomain.NetworkPolicy
	ifaces  []hadomain.MachineNetworkInterface
}

func (f fakeHARepo) EnsureDefaultPolicies(context.Context, string) error { return nil }
func (f fakeHARepo) GetFailoverPolicy(context.Context, string) (hadomain.FailoverPolicy, error) {
	return hadomain.FailoverPolicy{}, errors.New("not implemented")
}
func (f fakeHARepo) GetNetworkPolicy(context.Context, string) (hadomain.NetworkPolicy, error) {
	return f.network, nil
}
func (f fakeHARepo) ListVIPConfigs(context.Context, string) ([]hadomain.ClusterVIPConfig, error) {
	return nil, errors.New("not implemented")
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
