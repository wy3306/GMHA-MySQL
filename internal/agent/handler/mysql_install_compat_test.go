package handler

import (
	"strings"
	"testing"
)

func TestInstallDependenciesSelectsPackagesAcrossUbuntuGenerations(t *testing.T) {
	command := installDependenciesCommand()
	for _, required := range []string{
		"apt_pick libncurses6 libncurses5",
		"apt_pick libaio1 libaio1t64",
		"dnf",
		"yum",
		"ncurses-compat-libs",
		"libaio-devel",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("dependency installer does not contain %q: %s", required, command)
		}
	}
}
