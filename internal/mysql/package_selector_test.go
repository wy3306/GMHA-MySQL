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
