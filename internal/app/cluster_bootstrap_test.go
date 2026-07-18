package app

import (
	"context"
	"strings"
	"testing"

	hadomain "gmha/internal/domain/ha"
	taskdomain "gmha/internal/domain/task"
)

func validClusterBootstrapRequest() ClusterBootstrapRequest {
	return ClusterBootstrapRequest{
		Architecture:     hadomain.ArchitectureMasterSlave,
		PrimaryMachineID: "machine-1",
		Installs: []ClusterBootstrapInstall{
			{Machine: "10.0.0.1", MachineID: "machine-1", Port: 3306, ServerID: 1, RootPassword: "root-secret", Accounts: []taskdomain.MySQLAccountSpec{{Role: "mha", Username: "mha", Password: "secret", Enabled: true}}},
			{Machine: "10.0.0.2", MachineID: "machine-2", Port: 3306, ServerID: 2, RootPassword: "root-secret", Accounts: []taskdomain.MySQLAccountSpec{{Role: "mha", Username: "mha", Password: "secret", Enabled: true}}},
		},
	}
}

func TestResolveBootstrapVIPModeAutomatically(t *testing.T) {
	service := NewHAService(fakeHARepo{network: hadomain.NetworkPolicy{ClusterID: "demo", VIPRouteMode: hadomain.VipRouteModeL2ARP}}, nil, nil)
	masterSlave := service.resolveBootstrapVIPConfig(context.Background(), "demo", hadomain.ArchitectureMasterSlave, hadomain.ClusterVIPConfig{VIPRouteMode: "AUTO"})
	if masterSlave.VIPRouteMode != hadomain.VipRouteModeL2ARP {
		t.Fatalf("expected L2/ARP, got %s", masterSlave.VIPRouteMode)
	}
	dualMaster := service.resolveBootstrapVIPConfig(context.Background(), "demo", hadomain.ArchitectureDualMaster, hadomain.ClusterVIPConfig{VIPRouteMode: "AUTO"})
	if dualMaster.VIPRouteMode != hadomain.VipRouteModeL2ARP {
		t.Fatalf("expected automatic L2/ARP, got %s", dualMaster.VIPRouteMode)
	}
}

func TestValidateClusterBootstrapRequiresMHAAccount(t *testing.T) {
	req := validClusterBootstrapRequest()
	req.Installs[0].Accounts = nil
	if err := validateClusterBootstrapRequest(req); err == nil || !strings.Contains(err.Error(), "MHA management account") {
		t.Fatalf("expected MHA account error, got %v", err)
	}
}

func TestValidateClusterBootstrapMasterSlave(t *testing.T) {
	if err := validateClusterBootstrapRequest(validClusterBootstrapRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestArchitectureRootPasswordUsesPerMachineCredential(t *testing.T) {
	req := hadomain.ArchitectureAdjustmentRequest{
		RootPassword:  "shared-secret",
		RootPasswords: map[string]string{"machine-1": "primary-secret", "machine-2": "replica-secret"},
	}
	if got := architectureRootPassword(req, "machine-1"); got != "primary-secret" {
		t.Fatalf("expected primary machine password, got %q", got)
	}
	if got := architectureRootPassword(req, "machine-2"); got != "replica-secret" {
		t.Fatalf("expected replica machine password, got %q", got)
	}
	if got := architectureRootPassword(req, "machine-3"); got != "shared-secret" {
		t.Fatalf("expected shared fallback password, got %q", got)
	}
}

func TestValidateClusterBootstrapDualMasterRequiresSecondMaster(t *testing.T) {
	req := validClusterBootstrapRequest()
	req.Architecture = hadomain.ArchitectureDualMaster
	if err := validateClusterBootstrapRequest(req); err == nil || !strings.Contains(err.Error(), "secondary_master_machine_id") {
		t.Fatalf("expected secondary master error, got %v", err)
	}
	req.SecondaryMasterMachineID = "machine-2"
	if err := validateClusterBootstrapRequest(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateClusterBootstrapVIPRequiresAddress(t *testing.T) {
	req := validClusterBootstrapRequest()
	req.EnableVIP = true
	if err := validateClusterBootstrapRequest(req); err == nil || !strings.Contains(err.Error(), "vip_address") {
		t.Fatalf("expected VIP address error, got %v", err)
	}
}

func TestClusterInstallFailureSummaryIncludesTargetStepAndReason(t *testing.T) {
	detail := TaskDetail{
		Task:        taskdomain.Task{ID: "task-install-1", MachineID: "machine-1", Status: taskdomain.StatusFailed, CurrentStep: "initialize_mysql"},
		MachineName: "db-primary",
		MachineIP:   "10.0.0.1",
		Steps:       []taskdomain.Step{{ID: "step-init", StepName: "initialize_mysql", Status: taskdomain.StepFailed, Message: "mysqld --initialize exited with status 1"}},
		Events:      []taskdomain.Event{{ID: "event-error", StepID: "step-init", EventType: taskdomain.EventError, Content: "data directory is not empty"}},
	}

	summary := clusterInstallFailureSummary(detail)
	for _, expected := range []string{"db-primary", "10.0.0.1", "task-install-1", "initialize_mysql", "mysqld --initialize exited with status 1"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("failure summary missing %q: %s", expected, summary)
		}
	}
}

func TestClusterInstallFailureSummaryUsesErrorEventWhenStepMessageMissing(t *testing.T) {
	detail := TaskDetail{
		Task:   taskdomain.Task{ID: "task-install-2", MachineID: "machine-2", Status: taskdomain.StatusFailed, CurrentStep: "extract_package"},
		Steps:  []taskdomain.Step{{ID: "step-extract", StepName: "extract_package", Status: taskdomain.StepFailed}},
		Events: []taskdomain.Event{{ID: "event-error", StepID: "step-extract", EventType: taskdomain.EventError, Content: "archive checksum mismatch"}},
	}
	if summary := clusterInstallFailureSummary(detail); !strings.Contains(summary, "archive checksum mismatch") {
		t.Fatalf("failure summary did not use ERROR event: %s", summary)
	}
}
