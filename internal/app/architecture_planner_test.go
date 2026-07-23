package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestArchitectureCommandsUseAgentManagedCredentialsWhenPasswordOmitted(t *testing.T) {
	client := mysqlArchitectureClient("", 3307)
	if !strings.Contains(client, "--defaults-extra-file=__GMHA_MYSQL_DEFAULTS_FILE__") || !strings.Contains(client, "--port=3307") || strings.Contains(client, "MYSQL_PWD") {
		t.Fatalf("unexpected passwordless architecture client: %s", client)
	}
	ptOptions := ptArchitectureOptions("", 3307)
	if !strings.Contains(ptOptions, "--defaults-file=__GMHA_MYSQL_DEFAULTS_FILE__") || strings.Contains(ptOptions, "--password") {
		t.Fatalf("unexpected passwordless PT options: %s", ptOptions)
	}
	command := mysqlArchitectureCommand("", 3307, "SELECT 1")
	if !strings.Contains(command, "--skip-column-names") {
		t.Fatalf("parsed architecture probes must omit column headers: %s", command)
	}
}

func TestArchitectureRootBootstrapUsesLocalSocket(t *testing.T) {
	client := mysqlArchitectureRootSocketClient("root-secret", "/data/3306/mysql.sock")
	for _, required := range []string{"MYSQL_PWD=", "--protocol=socket", "--socket='/data/3306/mysql.sock'", "--user=root"} {
		if !strings.Contains(client, required) {
			t.Fatalf("root bootstrap client does not contain %q: %s", required, client)
		}
	}
}

func TestArchitecturePreflightChecksManagementPrivileges(t *testing.T) {
	command := architecturePreflightCommand("", 3307)
	for _, required := range []string{"SHOW GRANTS FOR CURRENT_USER", "SYSTEM_VARIABLES_ADMIN", "SUPER", "REPLICATION (SLAVE|REPLICA)", "exit 77"} {
		if !strings.Contains(command, required) {
			t.Fatalf("preflight command does not contain %q: %s", required, command)
		}
	}
}

func TestKillBusinessSessionsToleratesThreadRacesButVerifiesFence(t *testing.T) {
	command := killBusinessSessionsCommand(hadomain.ArchitectureAdjustmentRequest{
		ReplicationUser: "mha", ManagementUsers: []string{"monitor", "backup"},
	}, "db-1", 3306)
	for _, required := range []string{
		"event_scheduler", "Daemon", "KILL CONNECTION", "|| true",
		"SELECT COUNT(*)", "business session(s) remain", "GMHA_BUSINESS_SESSIONS_CLEARED",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("business-session fencing command does not contain %q: %s", required, command)
		}
	}
}

func TestArchitecturePlanRepairsLegacyMHAPrivilegesWhenRootBootstrapProvided(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		RootPassword: "one-time-root-secret",
	})
	if len(steps) < 3 || steps[0].Code != "acquire_lock" || steps[1].Code != "repair_management_privileges" || steps[2].Code != "preflight" {
		t.Fatalf("management repair must run before MHA preflight: %+v", steps)
	}
	for index, step := range steps {
		if step.Order != index+1 {
			t.Fatalf("step %s order=%d, want %d", step.Code, step.Order, index+1)
		}
	}
}

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

func TestValidateArchitectureRequestAllowsIndependentInstances(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureStandalone,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "I"},
			{MachineID: "db-2", Port: 3306, Role: "I"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("independent topology should be valid: %v", err)
	}
}

func TestPlanStandaloneArchitectureIsExecutableWithoutMasterElection(t *testing.T) {
	machines := []machinedomain.Machine{{ID: "db-1", Name: "DB-01", IP: "10.0.0.1", Cluster: "demo"}, {ID: "db-2", Name: "DB-02", IP: "10.0.0.2", Cluster: "demo"}}
	instances := []mysqlapp.Instance{{MachineID: "db-1", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}, {MachineID: "db-2", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}
	service := NewHAService(fakeHARepo{}, vipScopeMachineRepo{items: machines}, fakeArchitectureInstanceRepo{items: instances})
	plan, err := service.PlanArchitectureAdjustment(context.Background(), "demo", hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureStandalone,
		Nodes:        []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "I"}, {MachineID: "db-2", Port: 3306, Role: "I"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Executable || plan.SelectedCandidate.MachineID != "" || len(plan.Steps) == 0 {
		t.Fatalf("unexpected standalone plan: %+v", plan)
	}
}

func TestValidateArchitectureRequestRejectsMixedIndependentReplicationRoles(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "I"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "independent") {
		t.Fatalf("mixed replicated/independent roles must be rejected, got %v", err)
	}
}

func TestValidateArchitectureRequestAllowsVIPMoveWhenStartingFromIndependentWriters(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureMasterSlave,
		MoveVIP:      true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "new-primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("independent writers are fenced together before VIP movement: %v", err)
	}
}

func TestValidateArchitectureRequestAllowsInitialVIPBindingWithoutCurrentMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		PreferredNewMasterMachineID: "new-primary",
		MoveVIP:                     true,
		InitializeVIP:               true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "new-primary", Role: "M"},
			{MachineID: "replica", Role: "S"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected initial VIP binding to be valid without an old master, got %v", err)
	}
}

func TestValidateArchitectureRequestRejectsExternallyAmbiguousVIPInitialization(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID:      "primary",
		PreferredNewMasterMachineID: "replica",
		MoveVIP:                     true,
		InitializeVIP:               true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "primary", Role: "S"},
			{MachineID: "replica", Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "cannot declare a current master") {
		t.Fatalf("expected ambiguous initialization to be rejected, got %v", err)
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

func TestValidateArchitectureRequestAllowsVIPOnlyBindingOnDualMaster(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureDualMaster,
		PreferredNewMasterMachineID: "db-1",
		MoveVIP:                     true,
		InitializeVIP:               true,
		VIPOnly:                     true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Role: "M"},
			{MachineID: "db-2", Role: "M"},
		},
	}
	if err := validateArchitectureRequest(req); err != nil {
		t.Fatalf("expected VIP-only initial binding on dual master to be valid, got %v", err)
	}
}

func TestVIPOnlyPlanDoesNotChangeMySQLTopology(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{VIPOnly: true, MoveVIP: true})
	want := []string{"acquire_lock", "preflight", "freeze_business_access", "drain_business_sessions", "check_vip_conflict", "withdraw_vip", "verify_zero_vip", "bind_vip", "verify_single_vip", "resume_business_connections", "release_lock"}
	if len(steps) != len(want) {
		t.Fatalf("unexpected VIP-only step count: %+v", steps)
	}
	for index, code := range want {
		if steps[index].Code != code || steps[index].Order != index+1 {
			t.Fatalf("unexpected VIP-only workflow at %d: %+v", index, steps)
		}
	}
	for _, step := range steps {
		switch step.Code {
		case "freeze_old_master", "promote_new_master", "repoint_replicas", "pt_verify_replication":
			t.Fatalf("VIP-only workflow must not change MySQL topology: %+v", steps)
		}
	}
}

func TestVIPOnlyInitialBindingSkipsBusinessPause(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{VIPOnly: true, MoveVIP: true, InitializeVIP: true})
	for _, step := range steps {
		switch step.Code {
		case "freeze_business_access", "drain_business_sessions", "resume_business_connections":
			t.Fatalf("initial binding has no existing VIP traffic to pause: %+v", steps)
		}
	}
}

func TestDualToMasterSlavePlanSkipsElectionAndPromotion(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{CurrentArchitecture: hadomain.ArchitectureDualMaster, Architecture: hadomain.ArchitectureMasterSlave})
	codes := map[string]bool{}
	for _, step := range steps {
		codes[step.Code] = true
	}
	for _, unwanted := range []string{"elect_candidate", "promote_new_master", "force_gate", "pt_repair_on_failure"} {
		if codes[unwanted] {
			t.Fatalf("dual-to-master-slave must skip %s: %+v", unwanted, steps)
		}
	}
	for _, required := range []string{"freeze_business_access", "wait_replication_zero", "reconfigure_topology", "verify_topology", "pt_verify_replication"} {
		if !codes[required] {
			t.Fatalf("dual-to-master-slave is missing %s: %+v", required, steps)
		}
	}
}

func TestMasterSlaveToDualPlanSkipsExistingMasterPromotion(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{CurrentArchitecture: hadomain.ArchitectureMasterSlave, Architecture: hadomain.ArchitectureDualMaster})
	for _, step := range steps {
		if step.Code == "elect_candidate" || step.Code == "promote_new_master" || step.Code == "force_gate" {
			t.Fatalf("master-slave-to-dual must not repeat election/promotion: %+v", steps)
		}
	}
}

func TestValidateArchitectureRequestRejectsVIPOnlyReplicaTarget(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture:                hadomain.ArchitectureMasterSlave,
		CurrentMasterMachineID:      "db-1",
		PreferredNewMasterMachineID: "db-2",
		MoveVIP:                     true,
		VIPOnly:                     true,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Role: "M"},
			{MachineID: "db-2", Role: "S", SourceMachineID: "db-1"},
		},
	}
	if err := validateArchitectureRequest(req); err == nil || !strings.Contains(err.Error(), "target must be a master") {
		t.Fatalf("expected VIP-only replica target to be rejected, got %v", err)
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
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{MoveVIP: true, CurrentMasterMachineID: "old-primary", PreferredNewMasterMachineID: "new-primary"})
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
	if orders["pt_verify_replication"] <= orders["verify_topology"] || orders["pt_verify_replication"] >= orders["move_vip"] {
		t.Fatalf("mandatory PT verification must follow topology verification and precede VIP movement: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["verify_single_vip"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("business connections must resume only after VIP verification and before lock release: %+v", orders)
	}
}

func TestIndependentToReplicaPlanAlignsGTIDBeforeReplication(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureMasterSlave})
	orders := map[string]int{}
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["elect_candidate"] == 0 || orders["align_replica_gtid"] == 0 || orders["promote_new_master"] == 0 || orders["repoint_replicas"] == 0 {
		t.Fatalf("independent-to-replica plan is incomplete: %+v", orders)
	}
	if !(orders["elect_candidate"] < orders["align_replica_gtid"] && orders["align_replica_gtid"] < orders["promote_new_master"] && orders["promote_new_master"] < orders["repoint_replicas"]) {
		t.Fatalf("unsafe GTID alignment ordering: %+v", orders)
	}
}

func TestStandalonePlanVerifiesWithPTBeforeDetach(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureStandalone})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["pt_verify_before_split"] == 0 || orders["detach_replication"] == 0 || orders["pt_verify_before_split"] >= orders["detach_replication"] {
		t.Fatalf("standalone split must pass PT verification before replication is detached: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["verify_topology"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("standalone nodes must stay offline until topology verification completes: %+v", orders)
	}
}

func TestIndependentToReplicationPlanFreezesBeforeElection(t *testing.T) {
	steps := architecturePlanSteps(hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureDualMaster})
	orders := make(map[string]int, len(steps))
	for _, step := range steps {
		orders[step.Code] = step.Order
	}
	if orders["freeze_old_master"] == 0 || orders["elect_candidate"] == 0 || orders["freeze_old_master"] >= orders["elect_candidate"] {
		t.Fatalf("independent writers must be frozen before GTID election: %+v", orders)
	}
	if orders["pt_verify_replication"] <= orders["repoint_replicas"] {
		t.Fatalf("new replication must be followed by PT verification: %+v", orders)
	}
	if orders["resume_business_connections"] <= orders["pt_verify_replication"] || orders["resume_business_connections"] >= orders["release_lock"] {
		t.Fatalf("independent writers must stay offline until PT verification completes: %+v", orders)
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

func TestArchitectureIndependentNodesAreValidatedWithoutElection(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{Architecture: hadomain.ArchitectureStandalone, CurrentMasterMachineID: "db-1", Nodes: []hadomain.ArchitectureNodeRequest{{MachineID: "db-1", Port: 3306, Role: "I"}, {MachineID: "db-2", Port: 3306, Role: "I"}}}
	scores := architectureCandidateScores("demo", req,
		map[string]machinedomain.Machine{"db-1": {ID: "db-1", IP: "10.0.0.1"}, "db-2": {ID: "db-2", IP: "10.0.0.2"}},
		map[string][]mysqlapp.Instance{"db-1": {{MachineID: "db-1", Port: 3306, ServerID: 1, Status: mysqlapp.StatusRunning}}, "db-2": {{MachineID: "db-2", Port: 3306, ServerID: 2, Status: mysqlapp.StatusRunning}}},
	)
	if len(scores) != 2 || !scores[0].Eligible || !scores[1].Eligible {
		t.Fatalf("independent nodes should pass health validation without master election: %+v", scores)
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
	statusPrefix := strings.Split(command, " -e 'SHOW REPLICA STATUS")[0]
	if strings.Contains(statusPrefix, "--skip-column-names") {
		t.Fatalf("SHOW REPLICA STATUS must retain field labels for awk parsing: %s", command)
	}
}

func TestArchitectureShellCommandsParse(t *testing.T) {
	vip := hadomain.ClusterVIPConfig{ClusterID: "demo", VIPAddress: "10.0.0.100", VIPPrefix: 24, DefaultInterface: "eth0", BGPLocalAS: 65000, BGPPeerAS: 65001, BGPPeerAddress: "10.0.0.254"}
	commands := map[string]string{
		"pt install":          installCompatiblePTCommand("secret", 3306),
		"replication wait":    replicationCatchupCommand("secret", 3306),
		"delayed replication": delayedReplicationHealthCommand("secret", 3306, 600),
		"standalone detach":   standaloneDetachCommand("secret", 3306),
		"standalone verify":   verifyIndependentNodeCommand("secret", 3306),
		"PT checksum":         ptChecksumCommand("secret", 3306),
		"PT checksum verify":  ptChecksumVerificationCommand("secret", 3306),
		"L2 withdraw":         l2VIPRemoveCommand(vip),
		"L2 bind":             l2VIPBindCommand(vip),
		"BGP withdraw":        bgpVIPWithdrawCommand(vip),
		"BGP announce":        bgpVIPAnnounceCommand(vip, machinedomain.Machine{IP: "10.0.0.2"}),
	}
	for name, command := range commands {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Errorf("%s command has invalid shell syntax: %v\n%s", name, err, output)
		}
	}
}

func TestStandaloneDetachKeepsBusinessConnectionsOfflineUntilVerification(t *testing.T) {
	command := standaloneDetachCommand("secret", 3306)
	if !strings.Contains(command, "offline_mode=ON") || strings.Contains(command, "offline_mode=OFF") {
		t.Fatalf("standalone detach must keep business connections isolated: %s", command)
	}
}

func TestArchitectureRoleChangesPersistAcrossMySQLRestart(t *testing.T) {
	client := mysqlArchitectureClient("secret", 3306)
	writable := mysqlRolePersistenceCommand(client, false)
	for _, required := range []string{
		"SET PERSIST super_read_only=OFF",
		"SET PERSIST read_only=OFF",
		"SET GLOBAL super_read_only=OFF",
		"SET GLOBAL read_only=OFF",
	} {
		if !strings.Contains(writable, required) {
			t.Fatalf("writable role command missing %q: %s", required, writable)
		}
	}
	readOnly := mysqlRolePersistenceCommand(client, true)
	for _, required := range []string{
		"SET PERSIST read_only=ON",
		"SET PERSIST super_read_only=ON",
		"SET GLOBAL read_only=ON",
		"SET GLOBAL super_read_only=ON",
	} {
		if !strings.Contains(readOnly, required) {
			t.Fatalf("read-only role command missing %q: %s", required, readOnly)
		}
	}
	for name, command := range map[string]string{"writable": writable, "read-only": readOnly} {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("%s role command has invalid shell syntax: %v\n%s", name, err, output)
		}
	}
}

func TestDualMasterVerificationRequiresBothWritableFlagsOff(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		Architecture: hadomain.ArchitectureDualMaster,
		Nodes: []hadomain.ArchitectureNodeRequest{
			{MachineID: "db-1", Port: 3306, Role: "M"},
			{MachineID: "db-2", Port: 3306, Role: "M"},
		},
	}
	for _, node := range req.Nodes {
		command := verifyArchitectureNodeCommand(req, node, "db-1")
		for _, required := range []string{"@@read_only=0", "@@super_read_only=0", "ROLE_OK"} {
			if !strings.Contains(command, required) {
				t.Fatalf("dual-master verification for %s missing %q: %s", node.MachineID, required, command)
			}
		}
	}
}

func TestPTCheckRequiresOfflineInstallationAndVersionGate(t *testing.T) {
	command := installCompatiblePTCommand("secret", 3306)
	for _, want := range []string{"offline dependencies are missing", "enable offline PT installation", "min_pt=3.7.1", "8.*|9.*", "pt-table-checksum", "perl -MDBI -MDBD::mysql"} {
		if !strings.Contains(command, want) {
			t.Fatalf("PT install command missing %q", want)
		}
	}
	for _, forbidden := range []string{"repo.percona.com", "apt-get", "dnf install", "yum install"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("PT compatibility check must not access package repositories: %s", command)
		}
	}
}

func TestPTChecksumSupportsRowBinlogClusters(t *testing.T) {
	command := ptChecksumCommand("secret", 3306)
	for _, required := range []string{"--no-check-binlog-format", "--ignore-databases=mysql,sys,performance_schema,information_schema,gmha,percona", "GMHA_PT_NO_BUSINESS_TABLES"} {
		if !strings.Contains(command, required) {
			t.Fatalf("PT checksum command missing %q: %s", required, command)
		}
	}
}

type fakeArchitectureInstanceRepo struct{ items []mysqlapp.Instance }

func (r fakeArchitectureInstanceRepo) List(context.Context) ([]mysqlapp.Instance, error) {
	return r.items, nil
}
func (r fakeArchitectureInstanceRepo) Get(context.Context, string, int) (mysqlapp.Instance, bool, error) {
	return mysqlapp.Instance{}, false, nil
}
func (r fakeArchitectureInstanceRepo) Delete(context.Context, string, int) error { return nil }
func (r fakeArchitectureInstanceRepo) UpdateStatus(context.Context, string, int, string) error {
	return nil
}
func (r fakeArchitectureInstanceRepo) PruneUninstalled(context.Context) (int64, error) { return 0, nil }
