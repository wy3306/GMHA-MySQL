package app

import "testing"

func TestComponentVersionAndArchitectureNormalization(t *testing.T) {
	for input, want := range map[string]string{"0.0.2": "V0.0.2", "v1.2.3": "V1.2.3", "V2.0.0": "V2.0.0"} {
		if got := componentVersion(input); got != want {
			t.Fatalf("componentVersion(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{"amd64": "x86_64", "x86_64": "x86_64", "arm64": "aarch64", "aarch64": "aarch64"} {
		if got := normalizeComponentArch(input); got != want {
			t.Fatalf("normalizeComponentArch(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareComponentVersions(t *testing.T) {
	tests := []struct {
		current string
		target  string
		want    int
		ok      bool
	}{
		{current: "V0.1.0", target: "V0.2.0", want: -1, ok: true},
		{current: "1.10.0", target: "1.9.9", want: 1, ok: true},
		{current: "v2.0", target: "V2.0.0", want: 0, ok: true},
		{current: "V1.2.3+build.4", target: "1.2.4", want: -1, ok: true},
		{current: "unknown", target: "V1.0.0", want: 0, ok: false},
	}
	for _, test := range tests {
		got, ok := compareComponentVersions(test.current, test.target)
		if got != test.want || ok != test.ok {
			t.Fatalf("compareComponentVersions(%q, %q) = (%d, %v), want (%d, %v)", test.current, test.target, got, ok, test.want, test.ok)
		}
	}
}

func TestVersionRelation(t *testing.T) {
	if got := versionRelation("V1.0.0", "V1.1.0"); got != "upgrade" {
		t.Fatalf("upgrade relation = %q", got)
	}
	if got := versionRelation("V1.1.0", "V1.1.0"); got != "current" {
		t.Fatalf("current relation = %q", got)
	}
	if got := versionRelation("V2.0.0", "V1.9.0"); got != "downgrade" {
		t.Fatalf("downgrade relation = %q", got)
	}
}

func TestHighestComponentVersionIgnoresUnknownAndUsesNumericOrder(t *testing.T) {
	got := highestComponentVersion("未知", "V0.0.9", "v0.0.10", "", "invalid")
	if got != "V0.0.10" {
		t.Fatalf("highestComponentVersion() = %q, want V0.0.10", got)
	}
}

func TestUpgradePackagesAreMarkedAndSortedByHighestKnownVersion(t *testing.T) {
	items := []UpgradePackageView{
		{PackageItem: PackageItem{Name: "agent-old", Version: "V0.0.9", Arch: "x86_64"}},
		{PackageItem: PackageItem{Name: "agent-arm", Version: "V0.0.10", Arch: "aarch64"}},
		{PackageItem: PackageItem{Name: "agent-amd", Version: "V0.0.10", Arch: "x86_64"}},
		{PackageItem: PackageItem{Name: "agent-unknown", Version: "unknown", Arch: "x86_64"}},
	}
	latest := highestComponentVersion("V0.0.8", items[0].Version, items[1].Version, items[2].Version, items[3].Version)
	markLatestUpgradePackages(items, latest)
	sortUpgradePackageViews(items)

	if latest != "V0.0.10" {
		t.Fatalf("latest = %q, want V0.0.10", latest)
	}
	if items[0].Version != "V0.0.10" || items[1].Version != "V0.0.10" || !items[0].Latest || !items[1].Latest {
		t.Fatalf("highest packages were not retained first and marked latest: %#v", items)
	}
	if items[len(items)-1].Name != "agent-unknown" || items[len(items)-1].Latest {
		t.Fatalf("unknown package should sort last: %#v", items[len(items)-1])
	}
}

func TestDetectAgentVersionOutput(t *testing.T) {
	tests := map[string]string{
		"V0.0.1\n":              "V0.0.1",
		"gmha-agent v1.2.3\n":   "V1.2.3",
		"version: 2.10.0+linux": "V2.10.0+linux",
	}
	for output, want := range tests {
		got, err := detectAgentVersionOutput([]byte(output))
		if err != nil || got != want {
			t.Fatalf("detectAgentVersionOutput(%q) = (%q, %v), want %q", output, got, err, want)
		}
	}
	if _, err := detectAgentVersionOutput([]byte("unknown")); err == nil {
		t.Fatal("invalid Agent version output should fail")
	}
}
