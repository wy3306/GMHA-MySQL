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
		"tar -xzf",
		"install -m 0755",
		"libdbd-mysql-perl",
		"perl-DBD-MySQL",
		"pt-table-sync --version",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("PT installation command does not contain %q", expected)
		}
	}
}
