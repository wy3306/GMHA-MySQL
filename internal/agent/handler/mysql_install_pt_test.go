package handler

import (
	"strings"
	"testing"
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
		"install -m 0755",
		"libdbd-mysql-perl",
		"perl-DBD-MySQL",
		"perl -MDBI -MDBD::mysql -MIO::Socket::SSL",
		"pt-table-sync --version",
		"pt-online-schema-change",
		"pt-query-digest",
		"pt-replica-restart",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("PT installation command does not contain %q", expected)
		}
	}
}

func TestInstallXtraBackupCommandInstallsDependenciesAndValidatesSeries(t *testing.T) {
	command := installXtraBackupCommand("8.0.44", "percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz", "http://manager:8080/api/v1/packages/xtrabackup/package.tar.gz")
	for _, expected := range []string{
		"libev4", "libgcrypt20", "libcurl4", "libaio1", "rsync", "lz4", "zstd",
		"http://manager:8080/api/v1/packages/xtrabackup/", "xtrabackup binary not found", "xbstream", "ldd", "version $mysql_series",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("XtraBackup installation command does not contain %q", expected)
		}
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
