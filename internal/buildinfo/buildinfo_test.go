package buildinfo

import "testing"

func TestCurrentVersionDefaultsAndNormalizes(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for input, want := range map[string]string{"": "V0.0.2", "0.0.2": "V0.0.2", "v0.0.3": "V0.0.3", "V1.2.3": "V1.2.3"} {
		Version = input
		if got := CurrentVersion(); got != want {
			t.Fatalf("CurrentVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
