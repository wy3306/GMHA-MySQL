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
