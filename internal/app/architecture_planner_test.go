package app

import (
	"os/exec"
	"strings"
	"testing"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestValidateArchitectureRequestRejectsDelayedMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "M", DelaySeconds: 30},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "delayed replica") {
		t.Fatalf("expected delayed master validation error, got %v", err)
	}
}

func TestValidateArchitectureRequestAllowsThreeMasterRoots(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMultiMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "m1", Port: 3306, Role: "M"},
			{MachineID: "m2", Port: 3306, Role: "M"},
			{MachineID: "m3", Port: 3306, Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected three-master architecture to be valid, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsTwoNodeMultiMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMultiMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "m1", Port: 3306, Role: "M"},
			{MachineID: "m2", Port: 3306, Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "at least three masters") {
		t.Fatalf("expected multi-master validation error, got %v", err)
	}
}

func TestValidateArchitectureRequestRequiresCurrentMasterForVIP(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		MoveVIP:      true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "new-primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "current master") {
		t.Fatalf("expected current-master validation error, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsVIPMoveToSameMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		MoveVIP:                     true,
		CurrentMasterMachineID:      "primary",
		PreferredNewMasterMachineID: "primary",
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "different target master") {
		t.Fatalf("expected same-master VIP validation error, got %v", err)
	}
}

func TestArchitectureCandidateScoresUseRequestedInstancePort(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:           hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID: "primary",
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Port: 3306, Role: "M"},
			{MachineID: "replica", Port: 3307, Role: "M"},
		},
	}
	machines := map[string]machinedomain.Machine{
		"primary": {ID: "primary", Name: "db-1", IP: "10.0.0.1"},
		"replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"},
	}
	instances := map[string][]mysqlapp.Instance{
		"primary": {{MachineID: "primary", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}},
		"replica": {
			{MachineID: "replica", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning},
			{MachineID: "replica", Port: 3307, ServerID: 2, Status: mysqlapp.StatusRunning},
		},
	}

	scores := architectureCandidateScores("demo", req, machines, instances)
	if len(scores) != 2 {
		t.Fatalf("score count = %d, want 2", len(scores))
	}
	if !scores[1].Eligible || scores[1].Port != 3307 || scores[1].InstanceID != "replica:3307" {
		t.Fatalf("requested instance was not selected correctly: %+v", scores[1])
	}
}

func TestArchitecturePlanPromotesBeforeVIPMove(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{MoveVIP: true})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["promote_new_master"] == 0 || orders["move_vip"] == 0 || orders["promote_new_master"] >= orders["move_vip"] {
		t.Fatalf("unsafe plan ordering: promote=%d move_vip=%d", orders["promote_new_master"], orders["move_vip"])
	}
	if orders["wait_replication_zero"] >= orders["force_gate"] {
		t.Fatalf("force confirmation must follow replication wait: %+v", orders)
	}
}

func TestArchitectureCandidateRejectsReplicaRole(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave, Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "replica", Port: 3306, Role: "S"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"replica": {{MachineID: "replica", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 1 || scores[0].Eligible || !strings.Contains(strings.Join(scores[0].RejectReasons, " "), "target role") {
		t.Fatalf("replica role was eligible for promotion: %+v", scores)
	}
}

func TestArchitectureCandidateAllowsInPlaceCurrentMasterEdit(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave, CurrentMasterMachineID: "primary", PreferredNewMasterMachineID: "primary", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "primary", Port: 3306, Role: "M"}, {MachineID: "replica", Port: 3306, Role: "S"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"primary": {ID: "primary", Name: "db-1", IP: "10.0.0.1"}, "replica": {ID: "replica", Name: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"primary": {{MachineID: "primary", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}}, "replica": {{MachineID: "replica", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 2 || !scores[0].Eligible {
		t.Fatalf("current primary should remain eligible for an in-place topology edit: %+v", scores)
	}
}

func TestL2VIPCommandsRemoveBeforeBindAndAnnounce(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0", ArpingCount: 4}
	remove := l2VIPRemoveCommand(vip)
	bind := l2VIPBindCommand(vip)
	for _, want := range []string{"ip addr del", "grep -Fxq"} {
		if !strings.Contains(remove, want) {
			t.Fatalf("remove command missing %q: %s", want, remove)
		}
	}
	for _, want := range []string{"ip addr add", "arping -U -c 4", "grep -Fxq"} {
		if !strings.Contains(bind, want) {
			t.Fatalf("bind command missing %q: %s", want, bind)
		}
	}
}

func TestBGPCommandsWithdrawBeforeAnnounce(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{VIPAddress: "10.0.0.100", BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254", BGPCommunity: "65000:100"}
	withdraw := bgpVIPWithdrawCommand(vip)
	announce := bgpVIPAnnounceCommand(vip, machinedomain.Machine{IP: "10.0.0.2"})
	for _, want := range []string{"no network 10.0.0.100/32", "ip addr del"} {
		if !strings.Contains(withdraw, want) {
			t.Fatalf("withdraw command missing %q: %s", want, withdraw)
		}
	}
	for _, want := range []string{"neighbor 10.0.0.254 remote-as 65001", "network 10.0.0.100/32 route-map GMHA-VIP", "show bgp ipv4 unicast"} {
		if !strings.Contains(announce, want) {
			t.Fatalf("announce command missing %q: %s", want, announce)
		}
	}
}

func TestKeepalivedConfigTracksWritableMySQL(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{ClusterID: "demo", VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0"}
	command := keepalivedInstallCommand(vip, hadomain.ArchitectureNodeRequest{MachineID: "m1", Port: 3306}, machinedomain.Machine{IP: "10.0.0.1"}, machinedomain.Machine{IP: "10.0.0.2"}, "secret", 150)
	for _, want := range []string{"keepalived", "base64 -d", "keepalived -t -f"} {
		if !strings.Contains(command, want) {
			t.Fatalf("Keepalived command missing %q", want)
		}
	}
}

func TestGTIDTransactionCount(t *testing.T) {
	set := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:1-10:15,bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb:1-3"
	if got := gtidTransactionCount(set); got != 14 {
		t.Fatalf("gtidTransactionCount() = %d, want 14", got)
	}
}

func TestGTIDSetSubsetRejectsDivergentHistory(t *testing.T) {
	uuid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if !gtidSetSubset(uuid+":1-5", uuid+":1-10") {
		t.Fatal("expected earlier GTID set to be a subset")
	}
	if gtidSetSubset(uuid+":1-5:12", uuid+":1-10") {
		t.Fatal("divergent GTID interval must not be treated as a subset")
	}
	if gtidSetSubset("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb:1", uuid+":1-10") {
		t.Fatal("different source UUID must not be treated as a subset")
	}
	if !gtidSetSubset(uuid+":1-10", uuid+":1-5:6-10") {
		t.Fatal("adjacent intervals should be normalized before subset comparison")
	}
}

func TestDelayedReplicaHealthChecksConfiguredDelay(t *testing.T) {
	command := delayedReplicationHealthCommand("secret", 3306, 3600)
	for _, want := range []string{"SQL_Delay", "3600", "Replica_IO_Running", "Replica_SQL_Running"} {
		if !strings.Contains(command, want) {
			t.Fatalf("delayed replica command missing %q", want)
		}
	}
}

func TestReplicationCatchupChecksExecutedGTID(t *testing.T) {
	command := replicationCatchupCommand("secret", 3306)
	for _, want := range []string{"GTID_SUBSET", "RECEIVED_TRANSACTION_SET", "gtid_executed", "Seconds_Behind"} {
		if !strings.Contains(command, want) {
			t.Fatalf("replication catchup command missing %q", want)
		}
	}
}

func TestArchitectureShellCommandsParse(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{ClusterID: "demo", VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0", BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254"}
	commands := map[string]string{
		"pt install":          installCompatiblePTCommand("secret", 3306),
		"replication wait":    replicationCatchupCommand("secret", 3306),
		"delayed replication": delayedReplicationHealthCommand("secret", 3306, 600),
		"BGP withdraw":        bgpVIPWithdrawCommand(vip),
		"BGP announce":        bgpVIPAnnounceCommand(vip, machinedomain.Machine{IP: "10.0.0.2"}),
		"Keepalived install":  keepalivedInstallCommand(vip, hadomain.ArchitectureNodeRequest{MachineID: "m1", Port: 3306}, machinedomain.Machine{IP: "10.0.0.1"}, machinedomain.Machine{IP: "10.0.0.2"}, "secret", 150),
	}
	for name, command := range commands {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Errorf("%s command has invalid shell syntax: %v\n%s", name, err, output)
		}
	}
}

func TestPTInstallUsesOfficialRepositoryAndVersionGate(t *testing.T) {
	command := installCompatiblePTCommand("secret", 3306)
	for _, want := range []string{"repo.percona.com/apt/percona-release_latest.generic_all.deb", "repo.percona.com/yum/percona-release-latest.noarch.rpm", "min_pt=3.7.1", "8.4.*", "pt-table-checksum"} {
		if !strings.Contains(command, want) {
			t.Fatalf("PT install command missing %q", want)
		}
	}
}
