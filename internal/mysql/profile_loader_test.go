package mysql

import "testing"

func TestLoadProfileUsesEmbeddedProfileOutsideProjectDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	profile, err := LoadProfile("configs", "default")
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	if profile.Name != "default" || profile.BufferPoolRatio <= 0 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

func TestLoadProfileDoesNotMaskMissingCustomRoot(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := LoadProfile("custom-configs", "default"); err == nil {
		t.Fatal("LoadProfile() expected an error for a missing custom root")
	}
}
