package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBuildPublishesBothArchitecturesWithIncreasingVersions(t *testing.T) {
	root := t.TempDir()
	packages := &PackageService{storagePath: filepath.Join(root, "software")}
	if err := ensurePackageDirectories(packages.storagePath); err != nil {
		t.Fatal(err)
	}
	// A fake compiler records the embedded version in its output; no host execution required.
	bin := filepath.Join(root, "tools")
	os.Mkdir(bin, 0755)
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 case "$1" in -ldflags=*) flags="$1";; -o) shift; output="$1";; esac
 shift
done
if [ "$GOARCH" = "$FAIL_ARCH" ]; then exit 7; fi
printf '%s %s' "$GOARCH" "$flags" > "$output"
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	first, err := BuildAndPublishAgent(context.Background(), root, packages)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Version != first[1].Version || first[0].Arch != "x86_64" || first[1].Arch != "aarch64" {
		t.Fatalf("invalid release: %+v", first)
	}
	for _, item := range first {
		path, _ := packages.Open("gmha-agent", item.Name)
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "Version="+item.Version) || item.SHA256 == "" {
			t.Fatalf("metadata does not match binary: %+v %s", item, data)
		}
		if _, err := packages.Verify("gmha-agent", item.Name); err != nil {
			t.Fatal(err)
		}
	}
	second, err := BuildAndPublishAgent(context.Background(), root, packages)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, ok := compareComponentVersions(second[0].Version, first[0].Version); !ok || cmp <= 0 {
		t.Fatal("version reused")
	}
	t.Setenv("FAIL_ARCH", "arm64")
	if _, err := BuildAndPublishAgent(context.Background(), root, packages); err == nil {
		t.Fatal("expected build failure")
	}
	items, _ := packages.List("gmha-agent", "")
	if len(items) != 4 {
		t.Fatalf("failed build leaked partial release: %d", len(items))
	}
}

type failingPackageReader struct{}

func (failingPackageReader) Read(p []byte) (int, error) {
	copy(p, "partial")
	return 7, io.ErrUnexpectedEOF
}
func TestFailedUploadCannotReplacePublishedPackage(t *testing.T) {
	root := t.TempDir()
	s := &PackageService{storagePath: root}
	ensurePackageDirectories(root)
	_, err := s.SaveUploadWithMetadata("gmha-agent", "x86_64", "agent-V1.0.0.bin", "V1.0.0", "", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUpload("gmha-agent", "x86_64", "agent-V1.0.0.bin", failingPackageReader{}); err == nil {
		t.Fatal("expected error")
	}
	data, err := os.ReadFile(filepath.Join(root, "gmha-agent", "agent-V1.0.0.bin"))
	if err != nil || string(data) != "original" {
		t.Fatalf("published release damaged: %s %v", data, err)
	}
}
