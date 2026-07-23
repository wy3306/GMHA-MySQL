// package_selector_test.go 包含安装包选择器的单元测试。
package mysql

import (
	"os"
	"path/filepath"
	"testing"

	collectdomain "gmha/internal/collect"
)

// TestPackageSelectorSelectsHighestCompatibleGlibc 测试安装包选择器是否能选择与机器 glibc 版本兼容的最高版本安装包。
func TestPackageSelectorSelectsHighestCompatibleGlibc(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"mysql-8.0.44-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.0.44-linux-glibc2.28-aarch64.tar.xz",
		"mysql-8.0.44-linux-glibc2.28-x86_64.tar.xz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	item, err := NewPackageSelector(dir).Select(collectdomain.MachineInfo{
		Arch:         "x86_64",
		GlibcVersion: "2.42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.FileName != "mysql-8.0.44-linux-glibc2.28-x86_64.tar.xz" {
		t.Fatalf("unexpected package: %s", item.FileName)
	}
}

// TestParsePackageWithUnderscoreArch 测试包含下划线架构名称的安装包文件名解析。
func TestParsePackageWithUnderscoreArch(t *testing.T) {
	item, ok := parsePackage("mysql-8.0.44-linux-glibc2.28-x86_64.tar.xz")
	if !ok {
		t.Fatal("package should parse")
	}
	if item.Arch != "x86_64" {
		t.Fatalf("unexpected arch: %s", item.Arch)
	}
	if item.GlibcVersion.Major != 2 || item.GlibcVersion.Minor != 28 {
		t.Fatalf("unexpected glibc: %+v", item.GlibcVersion)
	}
}

func TestParseMySQL57TarGzPackage(t *testing.T) {
	name := "mysql-5.7.44-linux-glibc2.12-x86_64.tar.gz"
	item, ok := parsePackage(name)
	if !ok || item.Version != "5.7.44" || item.Arch != "x86_64" || item.GlibcVersion.Minor != 12 {
		t.Fatalf("unexpected MySQL 5.7 package metadata: ok=%t item=%+v", ok, item)
	}
	version, err := PackageVersion(name)
	if err != nil || version != "5.7.44" {
		t.Fatalf("PackageVersion() = %q, %v", version, err)
	}
}

func TestPackageSelectorListsSupportedVersionsWithCompatibilityMetadata(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"mysql-8.0.34-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.0.35-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.9.0-linux-glibc2.28-x86_64.tar.xz",
		"mysql-9.0.1-linux-glibc2.28-x86_64.tar.xz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	items, err := NewPackageSelector(dir).ListOptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d supported packages, want 3: %#v", len(items), items)
	}
	if items[0].Version != "9.0.1" || items[0].ReleaseTrack != "9.x Innovation" {
		t.Fatalf("unexpected first package: %#v", items[0])
	}
	if len(items[0].RuntimeParameterGroups) == 0 {
		t.Fatal("version parameter metadata is missing")
	}
}

func TestPackageSelectorSelectsIndependentVersionAndArchitecture(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"mysql-8.0.35-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.0.35-linux-glibc2.28-x86_64.tar.xz",
		"mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.4.6-linux-glibc2.17-aarch64.tar.xz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	item, err := NewPackageSelector(dir).SelectVersionArchitecture(collectdomain.MachineInfo{
		Arch:         "amd64",
		GlibcVersion: "2.20",
	}, "8.0.35", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if item.FileName != "mysql-8.0.35-linux-glibc2.17-x86_64.tar.xz" {
		t.Fatalf("unexpected package: %s", item.FileName)
	}
}

func TestPackageSelectorMatchesExactGlibc217Package(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"mysql-8.0.44-linux-glibc2.17-x86_64.tar.xz",
		"mysql-8.0.44-linux-glibc2.28-x86_64.tar.xz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	item, err := NewPackageSelector(dir).SelectVersionArchitecture(collectdomain.MachineInfo{Arch: "x86_64", GlibcVersion: "2.17"}, "8.0.44", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if item.FileName != "mysql-8.0.44-linux-glibc2.17-x86_64.tar.xz" {
		t.Fatalf("unexpected package: %s", item.FileName)
	}
}

func TestPackageSelectorRejectsArchitectureDifferentFromMachine(t *testing.T) {
	dir := t.TempDir()
	name := "mysql-8.4.6-linux-glibc2.17-aarch64.tar.xz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewPackageSelector(dir).SelectVersionArchitecture(collectdomain.MachineInfo{
		Arch:         "x86_64",
		GlibcVersion: "2.28",
	}, "8.4.6", "aarch64")
	if err == nil {
		t.Fatal("expected architecture mismatch error")
	}
}

func TestPackageMetadataCanBeRecoveredForLegacyInstances(t *testing.T) {
	name := "mysql-8.4.6-linux-glibc2.17-aarch64.tar.xz"
	version, err := PackageVersion(name)
	if err != nil {
		t.Fatal(err)
	}
	architecture, err := PackageArchitecture(name)
	if err != nil {
		t.Fatal(err)
	}
	if version != "8.4.6" || architecture != "aarch64" {
		t.Fatalf("unexpected metadata: version=%s architecture=%s", version, architecture)
	}
}
