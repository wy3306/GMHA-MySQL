package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupdomain "gmha/internal/domain/backup"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

func TestNextBackupTimeCustomInterval(t *testing.T) {
	start := time.Date(2026, 7, 13, 10, 0, 0, 0, time.Local)
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleCustom, StartAt: start, IntervalMinutes: 30}
	got := nextBackupTime(p, start.Add(31*time.Minute))
	want := start.Add(60 * time.Minute).UTC()
	if !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
}

func TestNextBackupTimeWeekly(t *testing.T) {
	start := time.Date(2026, 7, 13, 3, 15, 0, 0, time.Local) // Monday
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleWeekly, StartAt: start, Weekdays: []int{1, 5}}
	got := nextBackupTime(p, time.Date(2026, 7, 13, 4, 0, 0, 0, time.Local))
	want := time.Date(2026, 7, 17, 3, 15, 0, 0, time.Local).UTC()
	if !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
}

func TestNextBackupTimeWeeklyDoesNotRunBeforeStartDate(t *testing.T) {
	start := time.Date(2027, 7, 12, 4, 0, 0, 0, time.Local) // Monday
	p := backupdomain.Policy{ScheduleType: backupdomain.ScheduleWeekly, StartAt: start, Weekdays: []int{1, 5}}
	got := nextBackupTime(p, time.Date(2026, 7, 13, 4, 0, 0, 0, time.Local))
	if !got.Equal(start.UTC()) {
		t.Fatalf("next=%s want=%s", got, start.UTC())
	}
}

func TestRenderRemoteScriptQuotesArguments(t *testing.T) {
	command := renderRemoteScript("#!/bin/bash\necho ok\n", "backup", []string{"--target-dir", "/data/a b", "--user", "o'reilly"})
	for _, expected := range []string{"base64 -d", "'/data/a b'", `'o'"'"'reilly'`, "rm -f"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("command does not contain %q: %s", expected, command)
		}
	}
}

func TestXtraBackupScriptsEnforceServerSeriesCompatibility(t *testing.T) {
	for _, expected := range []string{
		"SELECT VERSION()",
		`5.7) required_xtrabackup_series="2.4"`,
		`8.*|9.*) required_xtrabackup_series="$mysql_series"`,
		"xtrabackup_series=%s",
	} {
		if !strings.Contains(xtrabackupBackupScript, expected) {
			t.Fatalf("backup script missing compatibility guard %q", expected)
		}
	}
	for _, expected := range []string{
		"backup_mysql_version",
		"required_xtrabackup_series",
		"xtrabackup_info",
		"before stopping MySQL",
	} {
		if !strings.Contains(xtrabackupRestoreScript, expected) {
			t.Fatalf("restore script missing compatibility guard %q", expected)
		}
	}
}

func TestXtraBackupBackupScriptChecksInstalledSeriesBeforeBackup(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable("mysql", `#!/usr/bin/env bash
if [[ "$*" == *"SELECT VERSION()"* ]]; then printf '%s\n' "${FAKE_MYSQL_VERSION:-8.4.10}"; fi
`)
	writeExecutable("xtrabackup", `#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then printf 'xtrabackup version %s based on MySQL server %s Linux\n' "${FAKE_XB_VERSION:-8.4.0-6}" "${FAKE_XB_SERIES:-8.4}"; fi
if [[ "${1:-}" == --defaults-file=* && "$*" == *"--backup"* ]]; then
  auth_file="${1#--defaults-file=}"
  auth_mode="$(stat -c %a "$auth_file" 2>/dev/null || stat -f %Lp "$auth_file")"
  [[ "$auth_mode" == "600" ]] || { echo "bad auth mode: $auth_mode" >&2; exit 91; }
  grep -q 'user="backup"' "$auth_file" || { echo "missing backup user" >&2; exit 92; }
  exit 0
fi
`)
	writeExecutable("flock", "#!/usr/bin/env bash\nexit 0\n")
	scriptPath := filepath.Join(root, "backup.sh")
	if err := os.WriteFile(scriptPath, []byte(xtrabackupBackupScript), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(target, xtraVersion, xtraSeries string) (string, error) {
		command := exec.Command("bash", scriptPath,
			"--target-dir", target,
			"--port", "3306",
			"--password-base64", "",
			"--retry-count", "0",
			"--disk-usage-threshold", "100",
		)
		command.Env = append(os.Environ(),
			"PATH="+binDir+":"+os.Getenv("PATH"),
			"FAKE_MYSQL_VERSION=8.4.10",
			"FAKE_XB_VERSION="+xtraVersion,
			"FAKE_XB_SERIES="+xtraSeries,
		)
		output, err := command.CombinedOutput()
		return string(output), err
	}
	compatibleTarget := filepath.Join(root, "compatible")
	output, err := run(compatibleTarget, "8.4.0-6", "8.4")
	if err != nil {
		t.Fatalf("compatible backup failed: %v\n%s", err, output)
	}
	meta, err := os.ReadFile(filepath.Join(compatibleTarget, "gmha-backup.meta"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"mysql_version=8.4.10", "xtrabackup_version=8.4.0-6", "xtrabackup_series=8.4"} {
		if !strings.Contains(string(meta), expected) {
			t.Fatalf("backup metadata missing %q:\n%s", expected, meta)
		}
	}

	incompatibleTarget := filepath.Join(root, "incompatible")
	output, err = run(incompatibleTarget, "8.0.35-36", "8.0")
	if err == nil || !strings.Contains(output, "required XtraBackup 8.4.x") {
		t.Fatalf("mismatched XtraBackup should fail before backup, err=%v output=%s", err, output)
	}
	if _, statErr := os.Stat(incompatibleTarget); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched precheck must not create target dir: %v", statErr)
	}
}

func TestXtraBackupRestoreScriptRejectsWrongSeriesBeforeDataChanges(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	fullDir := filepath.Join(root, "full")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fullDir, ".gmha-backup-complete"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fullDir, "gmha-backup.meta"), []byte("mysql_version=9.7.1\nxtrabackup_series=9.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "xtrabackup"), []byte("#!/usr/bin/env bash\necho 'xtrabackup version 8.4.0-6 based on MySQL server 8.4 Linux'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "restore.sh")
	if err := os.WriteFile(scriptPath, []byte(xtrabackupRestoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "mysql-data")
	command := exec.Command("bash", scriptPath,
		"--full-dir", fullDir,
		"--data-dir", dataDir,
		"--systemd-unit", "mysql-3306.service",
	)
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "requires XtraBackup 9.7.x") {
		t.Fatalf("restore must reject incompatible binary, err=%v output=%s", err, output)
	}
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("restore compatibility check must run before touching data dir: %v", statErr)
	}
}

func TestXtraBackupRestoreRollsBackAllInstanceDirectoriesOnCopyFailure(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	fullDir := filepath.Join(root, "full")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fullDir, ".gmha-backup-complete"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fullDir, "gmha-backup.meta"), []byte("mysql_version=8.0.35\nxtrabackup_series=8.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExecutable := func(name, content string) string {
		t.Helper()
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fakeXtraBackup := writeExecutable("xtrabackup", `#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  echo "xtrabackup version 8.0.35-36 based on MySQL server 8.0 Linux"
  exit 0
fi
if [[ "$*" == *"--prepare"* ]]; then
  echo "prepare:$*"
  exit 0
fi
if [[ "$*" == *"--copy-back"* ]]; then
  echo "intentional copy-back failure" >&2
  exit 42
fi
`)
	writeExecutable("systemctl", `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
exit 0
`)

	var managedDirs []string
	for _, name := range []string{"data", "binlog", "redo", "undo"} {
		dir := filepath.Join(root, "instance", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "original-"+name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		managedDirs = append(managedDirs, dir)
	}
	scriptPath := filepath.Join(root, "restore.sh")
	if err := os.WriteFile(scriptPath, []byte(xtrabackupRestoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	systemctlLog := filepath.Join(root, "systemctl.log")
	command := exec.Command("bash", scriptPath,
		"--full-dir", fullDir,
		"--data-dir", managedDirs[0],
		"--instance-binlog-dir", managedDirs[1],
		"--redo-dir", managedDirs[2],
		"--undo-dir", managedDirs[3],
		"--systemd-unit", "mysql-3306.service",
		"--xtrabackup", fakeXtraBackup,
	)
	command.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"SYSTEMCTL_LOG="+systemctlLog,
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "intentional copy-back failure") {
		t.Fatalf("copy-back failure should fail restore, err=%v output=%s", err, output)
	}
	if strings.Contains(string(output), "/.gmha_restore_") || !strings.Contains(string(output), "/gmha_restore_") {
		t.Fatalf("prepare staging directory must remain visible to InnoDB:\n%s", output)
	}
	for i, dir := range managedDirs {
		if _, statErr := os.Stat(filepath.Join(dir, "original-"+[]string{"data", "binlog", "redo", "undo"}[i])); statErr != nil {
			t.Fatalf("original directory %s was not restored: %v", dir, statErr)
		}
		matches, globErr := filepath.Glob(dir + ".before_restore_*")
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("rollback residue for %s: matches=%v err=%v", dir, matches, globErr)
		}
	}
	logData, err := os.ReadFile(systemctlLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(logData); strings.Count(got, "stop mysql-3306.service") < 2 || !strings.Contains(got, "start mysql-3306.service") {
		t.Fatalf("restore must stop for copy-back then restart original service during rollback:\n%s", got)
	}
}

func TestXtraBackupRestorePreparesDisposableIncrementalCopy(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	fullDir := filepath.Join(root, "full")
	incrementalDir := filepath.Join(root, "incremental")
	for _, dir := range []string{binDir, fullDir, incrementalDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{fullDir, incrementalDir} {
		if err := os.WriteFile(filepath.Join(dir, ".gmha-backup-complete"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fullDir, "gmha-backup.meta"), []byte("mysql_version=8.0.35\nxtrabackup_series=8.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeXtraBackup := filepath.Join(binDir, "xtrabackup")
	if err := os.WriteFile(fakeXtraBackup, []byte(`#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "xtrabackup version 8.0.35-36 based on MySQL server 8.0 Linux"; exit 0; fi
if [[ "$*" == *"--incremental-dir="* ]]; then
  for arg in "$@"; do
    if [[ "$arg" == --incremental-dir=* ]]; then
      incremental="${arg#--incremental-dir=}"
      echo "incremental-dir:$incremental"
      touch "$incremental/mutated-by-prepare"
    fi
  done
  exit 0
fi
if [[ "$*" == *"--prepare"* ]]; then exit 0; fi
if [[ "$*" == *"--copy-back"* ]]; then exit 42; fi
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "instance", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "restore.sh")
	if err := os.WriteFile(scriptPath, []byte(xtrabackupRestoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", scriptPath,
		"--full-dir", fullDir,
		"--incremental-dir", incrementalDir,
		"--data-dir", dataDir,
		"--systemd-unit", "mysql-3306.service",
		"--xtrabackup", fakeXtraBackup,
	)
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("intentional copy-back failure should fail restore:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(incrementalDir, "mutated-by-prepare")); !os.IsNotExist(statErr) {
		t.Fatalf("original incremental backup was mutated: %v", statErr)
	}
	if got := string(output); strings.Contains(got, "incremental-dir:"+incrementalDir+"\n") || !strings.Contains(got, "_incrementals/0") {
		t.Fatalf("prepare must use a disposable incremental copy:\n%s", got)
	}
}

func TestBackupServiceListsAPIReadyTargets(t *testing.T) {
	machines := &backupTargetMachineRepository{items: []machinedomain.Machine{
		{ID: "machine-online", Name: "db-a", IP: "10.0.0.11", Cluster: "prod", Status: machinedomain.StatusAgentOnline},
		{ID: "machine-offline", Name: "db-b", IP: "10.0.0.12", Cluster: "prod", Status: machinedomain.StatusAgentError},
		{ID: "machine-other", Name: "db-c", IP: "10.0.1.11", Cluster: "reporting", Status: machinedomain.StatusAgentOnline},
	}}
	instances := &backupTargetMySQLRepository{items: []mysqlapp.Instance{
		{MachineID: "machine-online", Port: 3306, Status: mysqlapp.StatusRunning, Version: "8.4.10", Architecture: "x86_64", PackageName: "mysql-8.4.10"},
		{MachineID: "machine-offline", Port: 3307, Status: mysqlapp.StatusStopped, Version: "8.0.46", Architecture: "x86_64"},
		{MachineID: "machine-other", Port: 3306, Status: mysqlapp.StatusRunning, Version: "8.4.10", Architecture: "x86_64"},
	}}
	service := NewBackupService(nil, nil, machines, instances)

	targets, err := service.ListTargets(context.Background(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets=%+v", targets)
	}
	if got := targets[0]; got.MachineID != "machine-online" || !got.BackupReady || !got.RestoreReady || got.MySQLVersion != "8.4.10" {
		t.Fatalf("online target=%+v", got)
	}
	if got := targets[1]; got.MachineID != "machine-offline" || got.BackupReady || got.RestoreReady || len(got.BlockingReasons) != 2 {
		t.Fatalf("offline target=%+v", got)
	}
}

func TestBackupServiceGetsIndividualResourcesWithoutSecrets(t *testing.T) {
	repository := &backupResourceRepository{
		policies: map[string]backupdomain.Policy{
			"policy-01": {ID: "policy-01", Name: "daily", MySQLUser: "backup", MySQLPassword: "secret"},
		},
		runs: map[string]backupdomain.Run{
			"run-01": {ID: "run-01", MachineID: "machine-01", Port: 3306, Status: backupdomain.RunPending},
		},
	}
	machines := &backupTargetMachineRepository{items: []machinedomain.Machine{
		{ID: "machine-01", Name: "db-a", IP: "10.0.0.11", Cluster: "prod", Status: machinedomain.StatusAgentOnline},
	}}
	service := NewBackupService(repository, nil, machines, &backupTargetMySQLRepository{})

	policy, err := service.GetPolicy(context.Background(), "policy-01")
	if err != nil {
		t.Fatal(err)
	}
	if policy.MySQLPassword != "" {
		t.Fatal("GetPolicy exposed mysql_password")
	}
	run, err := service.GetRun(context.Background(), "run-01")
	if err != nil {
		t.Fatal(err)
	}
	if run.MachineName != "db-a" || run.MachineIP != "10.0.0.11" {
		t.Fatalf("run target was not enriched: %+v", run)
	}
	if _, err = service.GetPolicy(context.Background(), "missing"); !errors.Is(err, ErrBackupPolicyNotFound) {
		t.Fatalf("missing policy error=%v", err)
	}
	if _, err = service.GetRun(context.Background(), "missing"); !errors.Is(err, ErrBackupRunNotFound) {
		t.Fatalf("missing run error=%v", err)
	}
}

func TestBackupServiceRejectsPointInTimeRestoreWithoutBinlog(t *testing.T) {
	repository := &backupResourceRepository{
		policies: map[string]backupdomain.Policy{},
		runs: map[string]backupdomain.Run{
			"run-01": {ID: "run-01", MachineID: "machine-01", Port: 3306, IncludeBinlog: false},
		},
	}
	service := NewBackupService(repository, nil, &backupTargetMachineRepository{}, &backupTargetMySQLRepository{})
	_, err := service.Restore(context.Background(), "run-01", RestoreOptions{
		Confirmation: "RESTORE run-01",
		Mode:         "point_in_time",
		RestoreTime:  time.Now().Add(-time.Hour),
	})
	if err == nil || !strings.Contains(err.Error(), "未包含 Binlog") {
		t.Fatalf("point-in-time restore error=%v", err)
	}
}

type backupTargetMachineRepository struct {
	items []machinedomain.Machine
}

func (r *backupTargetMachineRepository) Save(_ context.Context, machine machinedomain.Machine) (machinedomain.Machine, error) {
	return machine, nil
}
func (r *backupTargetMachineRepository) UpdateStatus(context.Context, string, machinedomain.Status, string) error {
	return nil
}
func (r *backupTargetMachineRepository) GetByID(_ context.Context, id string) (machinedomain.Machine, bool, error) {
	for _, machine := range r.items {
		if machine.ID == id {
			return machine, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}
func (r *backupTargetMachineRepository) GetByIP(_ context.Context, ip string) (machinedomain.Machine, bool, error) {
	for _, machine := range r.items {
		if machine.IP == ip {
			return machine, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}
func (r *backupTargetMachineRepository) List(context.Context) ([]machinedomain.Machine, error) {
	return append([]machinedomain.Machine(nil), r.items...), nil
}
func (r *backupTargetMachineRepository) UpdateBasics(context.Context, machinedomain.Machine) error {
	return nil
}
func (r *backupTargetMachineRepository) AssignCluster(context.Context, string, string) error {
	return nil
}
func (r *backupTargetMachineRepository) RebindCluster(context.Context, string, string) error {
	return nil
}
func (r *backupTargetMachineRepository) ClearCluster(context.Context, string) error { return nil }
func (r *backupTargetMachineRepository) Delete(context.Context, string) error       { return nil }

type backupTargetMySQLRepository struct {
	items []mysqlapp.Instance
}

func (r *backupTargetMySQLRepository) Get(_ context.Context, machineID string, port int) (mysqlapp.Instance, bool, error) {
	for _, instance := range r.items {
		if instance.MachineID == machineID && instance.Port == port {
			return instance, true, nil
		}
	}
	return mysqlapp.Instance{}, false, nil
}
func (r *backupTargetMySQLRepository) List(context.Context) ([]mysqlapp.Instance, error) {
	return append([]mysqlapp.Instance(nil), r.items...), nil
}

type backupResourceRepository struct {
	policies map[string]backupdomain.Policy
	runs     map[string]backupdomain.Run
}

func (r *backupResourceRepository) SavePolicy(_ context.Context, policy backupdomain.Policy) error {
	r.policies[policy.ID] = policy
	return nil
}
func (r *backupResourceRepository) GetPolicy(_ context.Context, id string) (backupdomain.Policy, bool, error) {
	policy, ok := r.policies[id]
	return policy, ok, nil
}
func (r *backupResourceRepository) ListPolicies(context.Context, string) ([]backupdomain.Policy, error) {
	return nil, nil
}
func (r *backupResourceRepository) ListDuePolicies(context.Context, time.Time) ([]backupdomain.Policy, error) {
	return nil, nil
}
func (r *backupResourceRepository) UpdatePolicySchedule(context.Context, string, time.Time, time.Time, bool) error {
	return nil
}
func (r *backupResourceRepository) DeletePolicy(_ context.Context, id string) error {
	delete(r.policies, id)
	return nil
}
func (r *backupResourceRepository) SaveRun(_ context.Context, run backupdomain.Run) error {
	r.runs[run.ID] = run
	return nil
}
func (r *backupResourceRepository) GetRun(_ context.Context, id string) (backupdomain.Run, bool, error) {
	run, ok := r.runs[id]
	return run, ok, nil
}
func (r *backupResourceRepository) ListRuns(context.Context, string, int) ([]backupdomain.Run, error) {
	return nil, nil
}
func (r *backupResourceRepository) SetRestoreTask(context.Context, string, string) error { return nil }
