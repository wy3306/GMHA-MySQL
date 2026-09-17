package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerSourceFingerprintTracksRuntimeSourceAndIgnoresOutputs(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module gmha\n")
	mustWrite("cmd/gmha/main.go", "package main\n")
	mustWrite("internal/interface/http/frontend/src/main.js", "export const version = 1\n")

	initial, err := managerSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite("dist/gmha", "build output")
	mustWrite("internal/example_test.go", "package internal")
	ignored, err := managerSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if ignored != initial {
		t.Fatal("build output or test-only change triggered source fingerprint")
	}
	mustWrite("internal/interface/http/frontend/src/main.js", "export const version = 2\n")
	changed, err := managerSourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed == initial {
		t.Fatal("frontend runtime source change was not detected")
	}
}

func TestManagerSourceFileScope(t *testing.T) {
	cases := map[string]bool{
		"go.mod":              true,
		"internal/app/app.go": true,
		"cmd/gmha/main.go":    true,
		"internal/interface/http/frontend/src/main.js":       true,
		"internal/interface/http/frontend/src/style.css":     true,
		"internal/app/app_test.go":                           false,
		"internal/interface/http/frontend/src/main.test.mjs": false,
		"dist/gmha":       false,
		"data/manager.db": false,
	}
	for path, expected := range cases {
		if actual := managerSourceFile(path); actual != expected {
			t.Fatalf("managerSourceFile(%q) = %v, want %v", path, actual, expected)
		}
	}
}
