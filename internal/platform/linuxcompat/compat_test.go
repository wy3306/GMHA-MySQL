package linuxcompat

import (
	"strings"
	"testing"
)

func TestEvaluateLinuxCompatibilityMatrix(t *testing.T) {
	tests := []struct {
		os    string
		arch  string
		glibc string
		level Level
		can   bool
	}{
		{"Ubuntu 24.04", "x86_64", "2.39", LevelSupported, true},
		{"Ubuntu 20.04", "amd64", "2.31", LevelSupported, true},
		{"Ubuntu 18.04", "x86_64", "2.27", LevelLegacy, true},
		{"Ubuntu 16.04", "x86_64", "2.23", LevelUnsupported, false},
		{"CentOS Linux 7", "x86_64", "2.17", LevelLegacy, true},
		{"CentOS Linux 6", "x86_64", "2.12", LevelUnsupported, false},
		{"CentOS Stream 9", "aarch64", "2.34", LevelSupported, true},
		{"Rocky Linux 9.4", "x86_64", "2.34", LevelSupported, true},
		{"AlmaLinux 8.10", "x86_64", "2.28", LevelSupported, true},
		{"Debian GNU/Linux 12", "x86_64", "2.36", LevelSupported, true},
		{"Some Custom Linux 1", "x86_64", "2.36", LevelUnknown, false},
	}
	for _, test := range tests {
		t.Run(test.os, func(t *testing.T) {
			report := Evaluate(test.os, test.arch, test.glibc)
			if report.Level != test.level || report.CanInstall != test.can {
				t.Fatalf("Evaluate(%q) = level=%s can=%t, want level=%s can=%t: %+v", test.os, report.Level, report.CanInstall, test.level, test.can, report)
			}
		})
	}
}

func TestEvaluateRejectsUnsupportedArchitectureAndOldGlibc(t *testing.T) {
	report := Evaluate("Ubuntu 22.04", "ppc64le", "2.35")
	if report.CanInstall || !strings.Contains(strings.Join(report.Blockers, " "), "x86_64") {
		t.Fatalf("unsupported architecture should be blocked: %+v", report)
	}
	report = Evaluate("Rocky Linux 9", "x86_64", "2.12")
	if report.CanInstall || !strings.Contains(strings.Join(report.Blockers, " "), "2.17") {
		t.Fatalf("old glibc should be blocked: %+v", report)
	}
}
