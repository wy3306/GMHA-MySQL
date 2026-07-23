package handler

import (
	"os/exec"
	"strings"
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func TestInstallPTToolsCommandUsesManagerPackage(t *testing.T) {
	command := installPTToolsCommand(
		"/usr/local/mysql",
		"percona-toolkit-3.7.1-noarch.tar.gz",
		"http://manager:8080/api/v1/packages/percona-toolkit/percona-toolkit-3.7.1-noarch.tar.gz",
	)
	for _, expected := range []string{
		"http://manager:8080/api/v1/packages/percona-toolkit/",
		"(Distrib|Ver)[[:space:]]+([0-9]+\\.[0-9]+)",
		"tar -xzf",
		"--no-network",
		"dpkg -i",
		"rpm -Uvh --replacepkgs",
		"vendor/perl5",
		"PERL5LIB",
		"perl -MDBI -MDBD::mysql -MIO::Socket::SSL",
		"pt-table-sync --version",
		"pt-online-schema-change",
		"pt-archiver",
		"pt-query-digest",
		"pt-replica-restart",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("PT installation command does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"apt-get update", "dnf -y install", "yum -y install", "repo.percona.com"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("offline PT installation command unexpectedly contains %q", forbidden)
		}
	}
	if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("offline PT installation command has invalid shell syntax: %v\n%s", err, output)
	}
}

func TestInstallXtraBackupCommandInstallsDependenciesAndValidatesSeries(t *testing.T) {
	command := installXtraBackupCommand("8.0.44", "percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz", "http://manager:8080/api/v1/packages/xtrabackup/package.tar.gz")
	for _, expected := range []string{
		"libev4", "libgcrypt20", "libcurl4", "libaio1", "rsync", "lz4", "zstd",
		"http://manager:8080/api/v1/packages/xtrabackup/", "xtrabackup binary not found", "xbstream", "ldd", "version $xbk_series",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("XtraBackup installation command does not contain %q", expected)
		}
	}
}

func TestInstallXtraBackupCommandMapsMySQL57ToXtraBackup24(t *testing.T) {
	command := installXtraBackupCommand("5.7.44", "percona-xtrabackup-2.4.29-Linux-x86_64.glibc2.17-minimal.tar.gz", "http://manager:8080/xtrabackup.tar.gz")
	for _, expected := range []string{"5.7) xbk_series=2.4", `based on MySQL server $mysql_series`, `required XtraBackup $xbk_series`} {
		if !strings.Contains(command, expected) {
			t.Fatalf("MySQL 5.7 XtraBackup command does not contain %q", expected)
		}
	}
}

func TestMySQLConfigValidationDetectsValidateConfigSupportAtRuntime(t *testing.T) {
	legacy := mysqlConfigValidationCommand("/usr/local/mysql-3306/bin/mysqld", "/data/3306/my.cnf", "5.7.44")
	if !strings.Contains(legacy, "--verbose --help") || !strings.Contains(legacy, "grep -q -- '--validate-config'") {
		t.Fatalf("unexpected MySQL 5.7 validation command: %s", legacy)
	}
	modern := mysqlConfigValidationCommand("/usr/local/mysql-3306/bin/mysqld", "/data/3306/my.cnf", "8.0.44")
	if !strings.Contains(modern, "--validate-config") {
		t.Fatalf("unexpected MySQL 8 validation command: %s", modern)
	}
}

func TestConfigureTCMallocCommandUsesSystemdDropInAndValidation(t *testing.T) {
	command := configureTCMallocCommand("/usr/local/mysql", "mysqld")
	for _, expected := range []string{"libgoogle-perftools4", "gperftools-libs", "libtcmalloc_minimal", "LD_PRELOAD", "/etc/systemd/system/mysqld.service.d/allocator.conf", "mysqld --version", "systemctl daemon-reload"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("tcmalloc configuration command does not contain %q", expected)
		}
	}
}

func TestManagerResourceURLUsesActiveManagerForCurrentAndLegacyTasks(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "relative current task",
			raw:  "/api/v1/software/mysql/mysql-8.0.44.tar.xz",
			want: "http://192.168.31.59:8080/api/v1/software/mysql/mysql-8.0.44.tar.xz",
		},
		{
			name: "absolute legacy task is rebased",
			raw:  "http://10.211.241.17:8080/api/v1/software/mysql/mysql-8.0.44.tar.xz",
			want: "http://192.168.31.59:8080/api/v1/software/mysql/mysql-8.0.44.tar.xz",
		},
		{
			name: "external resource is preserved",
			raw:  "https://downloads.example.com/mysql.tar.xz",
			want: "https://downloads.example.com/mysql.tar.xz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := managerResourceURL("http://192.168.31.59:8080", tt.raw, "/api/v1/software/mysql/")
			if got != tt.want {
				t.Fatalf("managerResourceURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMySQLInstallPathsAndServiceArePortScoped(t *testing.T) {
	runner3306 := mysqlInstallRunner{spec: taskdomain.MySQLInstallSpec{
		Port: 3306, PackageName: "mysql-8.0.44-linux-x86_64.tar.xz", BaseDir: "/usr/local/mysql-3306",
		InstanceDir: "/data/3306", DataDir: "/data/3306/data", SystemdUnitName: "mysqld-3306",
	}}
	runner3307 := mysqlInstallRunner{spec: taskdomain.MySQLInstallSpec{
		Port: 3307, PackageName: "mysql-8.0.44-linux-x86_64.tar.xz", BaseDir: "/usr/local/mysql-3307",
		InstanceDir: "/data/3307", DataDir: "/data/3307/data", SystemdUnitName: "mysqld-3307",
	}}
	if runner3306.packageInstallDir() == runner3307.packageInstallDir() {
		t.Fatal("different ports must use different extracted binary directories")
	}
	command := runner3307.checkEnvCommand()
	for _, expected := range []string{"mysqld-3307.service", "port 3307 is already listening", "base_dir symlink is already used"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("environment check does not contain %q", expected)
		}
	}
}
