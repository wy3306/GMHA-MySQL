package app

import (
	"testing"

	taskdomain "gmha/internal/domain/task"
)

type capabilityTestConnection struct{}

func (capabilityTestConnection) Send(taskdomain.DispatchEnvelope) error { return nil }

func TestMachineCapabilityRejectsLegacyAgent(t *testing.T) {
	service := NewTaskService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	conn := capabilityTestConnection{}
	service.RegisterAgentForMachineWithCapabilities("agent-db-1", "db-1", conn, []string{string(taskdomain.TypeExec)})

	if ok, reason := service.MachineCapability("db-1", taskdomain.CapabilityMySQLDefaultsFile); ok || reason == "" {
		t.Fatalf("legacy Agent must be rejected with a reason, got ok=%v reason=%q", ok, reason)
	}
}

func TestMachineCapabilityAcceptsSecureCredentialAgent(t *testing.T) {
	service := NewTaskService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	conn := capabilityTestConnection{}
	service.RegisterAgentForMachineWithCapabilities("custom-agent", "db-1", conn, []string{
		string(taskdomain.TypeExec),
		taskdomain.CapabilityMySQLDefaultsFile,
	})

	if ok, reason := service.MachineCapability("db-1", taskdomain.CapabilityMySQLDefaultsFile); !ok || reason != "" {
		t.Fatalf("capable Agent must pass, got ok=%v reason=%q", ok, reason)
	}
	service.UnregisterAgent("custom-agent", conn)
	if ok, reason := service.MachineCapability("db-1", taskdomain.CapabilityMySQLDefaultsFile); ok || reason == "" {
		t.Fatalf("disconnected Agent must be rejected, got ok=%v reason=%q", ok, reason)
	}
}
