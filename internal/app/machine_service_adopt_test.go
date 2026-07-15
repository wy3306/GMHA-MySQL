package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

type cleanupSSHClient struct {
	commands  []string
	output    string
	outputErr error
}

func (c *cleanupSSHClient) CheckTrustConnection(context.Context, machinedomain.Endpoint, string) (bool, error) {
	return true, nil
}
func (c *cleanupSSHClient) TestConnection(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth) error {
	return nil
}
func (c *cleanupSSHClient) Run(_ context.Context, _ machinedomain.Endpoint, _ machinedomain.SSHAuth, command string) error {
	c.commands = append(c.commands, command)
	return nil
}
func (c *cleanupSSHClient) RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error) {
	return []byte(c.output), c.outputErr
}

type detachMachineRepo struct {
	machine machinedomain.Machine
	deleted bool
}

func (r *detachMachineRepo) Save(context.Context, machinedomain.Machine) (machinedomain.Machine, error) {
	return r.machine, nil
}
func (r *detachMachineRepo) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}
func (r *detachMachineRepo) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	return r.machine, id == r.machine.ID, nil
}
func (r *detachMachineRepo) GetByIP(context.Context, string) (machinedomain.Machine, bool, error) {
	return r.machine, true, nil
}
func (r *detachMachineRepo) List(context.Context) ([]machinedomain.Machine, error) {
	return []machinedomain.Machine{r.machine}, nil
}
func (r *detachMachineRepo) UpdateBasics(context.Context, machinedomain.Machine) error { return nil }
func (r *detachMachineRepo) AssignCluster(context.Context, string, string) error       { return nil }
func (r *detachMachineRepo) RebindCluster(context.Context, string, string) error       { return nil }
func (r *detachMachineRepo) ClearCluster(context.Context, string) error                { return nil }
func (r *detachMachineRepo) Delete(_ context.Context, id string) error {
	if id == r.machine.ID {
		r.deleted = true
	}
	return nil
}

func TestParsePreservedMySQLConfig(t *testing.T) {
	config, path, err := parsePreservedMySQLConfig([]byte("noise\n__GMHA_MYSQL_CONFIG__/home/gmha/agent/mysql-heartbeat.json\n{\"instances\":[{\"port\":3306,\"systemd_unit\":\"mysqld-3306\",\"data_dir\":\"/data/mysql/3306\"}]}"))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if path != "/home/gmha/agent/mysql-heartbeat.json" {
		t.Fatalf("unexpected path %q", path)
	}
	if len(config.Instances) != 1 || config.Instances[0].Port != 3306 || config.Instances[0].DataDir != "/data/mysql/3306" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParsePreservedMySQLConfigAllowsNoConfig(t *testing.T) {
	config, path, err := parsePreservedMySQLConfig([]byte(""))
	if err != nil || path != "" || len(config.Instances) != 0 {
		t.Fatalf("expected empty discovery result, got path=%q config=%#v err=%v", path, config, err)
	}
}

func TestDeleteMachineDetachOnlyDoesNotNeedRemoteServices(t *testing.T) {
	repo := &detachMachineRepo{machine: machinedomain.Machine{ID: "machine-offline", IP: "192.0.2.20"}}
	service := &MachineService{machineRepo: repo}
	result, err := service.DeleteMachineWithOptions(context.Background(), repo.machine.ID, DeleteMachineOptions{DetachOnly: true})
	if err != nil {
		t.Fatalf("detach machine: %v", err)
	}
	if !repo.deleted || !result.DetachedOnly || !result.LocalCleaned {
		t.Fatalf("unexpected detach result: deleted=%v result=%+v", repo.deleted, result)
	}
}

func TestDeleteMachineRejectsClusterMember(t *testing.T) {
	repo := &detachMachineRepo{machine: machinedomain.Machine{ID: "machine-member", IP: "192.0.2.21", Cluster: "mysql-prod"}}
	service := &MachineService{machineRepo: repo}
	_, err := service.DeleteMachineWithOptions(context.Background(), repo.machine.ID, DeleteMachineOptions{DetachOnly: true})
	if err == nil || !strings.Contains(err.Error(), "remove it from the cluster") {
		t.Fatalf("expected cluster membership error, got %v", err)
	}
	if repo.deleted {
		t.Fatal("cluster member must not be deleted")
	}
}

func TestUninstallMySQLViaSSHUsesRegisteredPathsAndSystemd(t *testing.T) {
	sshClient := &cleanupSSHClient{}
	service := &MachineService{sshClient: sshClient}
	machine := machinedomain.Machine{ID: "machine-offline", IP: "192.0.2.20", SSHPort: 22, SSHUser: "root"}
	instance := mysqlapp.Instance{
		MachineID:   machine.ID,
		Port:        3306,
		InstanceDir: "/srv/mysql/3306",
		DataDir:     "/data/mysql/3306",
		BinlogDir:   "/logs/mysql/3306/binlog",
		RedoDir:     "/fast/mysql/3306/redo",
		UndoDir:     "/fast/mysql/3306/undo",
		TmpDir:      "/var/tmp/mysql-3306",
		BaseDir:     "/usr/local/mysql",
		SystemdUnit: "mysqld-3306",
		MyCnfPath:   "/srv/mysql/3306/my.cnf",
	}
	if err := service.uninstallMySQLViaSSH(context.Background(), machine, instance); err != nil {
		t.Fatalf("SSH uninstall: %v", err)
	}
	joined := strings.Join(sshClient.commands, "\n")
	for _, expected := range []string{"systemctl stop", "mysqld-3306", instance.DataDir, instance.BinlogDir, "systemctl daemon-reload"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("SSH cleanup commands do not contain %q:\n%s", expected, joined)
		}
	}
}

func TestConfirmPreservedAgentAcceptsActiveServiceAfterRestartError(t *testing.T) {
	sshClient := &cleanupSSHClient{output: "state=active result=success exit=0"}
	service := &MachineService{}
	err := service.confirmPreservedAgentOnline(context.Background(), sshClient, machinedomain.Endpoint{}, machinedomain.SSHAuth{}, "machine-1", time.Now(), errors.New("Process exited with status 1"))
	if err != nil {
		t.Fatalf("active preserved Agent must not be reported as failed: %v", err)
	}
}

func TestConfirmPreservedAgentReportsShortServiceDiagnosis(t *testing.T) {
	sshClient := &cleanupSSHClient{output: "state=failed result=exit-code exit=1"}
	service := &MachineService{}
	err := service.confirmPreservedAgentOnline(context.Background(), sshClient, machinedomain.Endpoint{}, machinedomain.SSHAuth{}, "machine-1", time.Now(), errors.New("exit status 1"))
	if err == nil || !strings.Contains(err.Error(), "state=failed") {
		t.Fatalf("expected concise service diagnosis, got %v", err)
	}
}
