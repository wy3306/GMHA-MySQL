// package_selector.go 实现 MySQL 安装包的自动选择，根据机器架构和 glibc 版本匹配最合适的安装包。
package mysql

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	collectdomain "gmha/internal/collect"
)

// Package 定义 MySQL 安装包的信息，包含文件名、路径、版本号、架构和 glibc 版本。
type Package struct {
	FileName     string
	FullPath     string
	Version      string
	Arch         string
	GlibcVersion version
}

// PackageSelector 是安装包选择器，负责根据机器信息从本地软件目录中选择最合适的 MySQL 安装包。
type PackageSelector struct {
	softwareDir string
}

// NewPackageSelector 创建并返回一个新的安装包选择器实例。
func NewPackageSelector(softwareDir string) *PackageSelector {
	return &PackageSelector{softwareDir: softwareDir}
}

// Select 根据机器信息从软件目录中选择匹配架构和 glibc 版本的最优 MySQL 安装包。
func (s *PackageSelector) Select(info collectdomain.MachineInfo) (Package, error) {
	entries, err := os.ReadDir(s.softwareDir)
	if err != nil {
		return Package{}, err
	}
	targetArch := normalizeArch(info.Arch)
	targetGlibc, err := parseVersion(info.GlibcVersion)
	if err != nil {
		return Package{}, err
	}

	candidates := make([]Package, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		item, ok := parsePackage(entry.Name())
		if !ok {
			continue
		}
		if normalizeArch(item.Arch) != targetArch {
			continue
		}
		if compareVersion(item.GlibcVersion, targetGlibc) > 0 {
			continue
		}
		item.FullPath = filepath.Join(s.softwareDir, item.FileName)
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return Package{}, errors.New("no local mysql package matches machine arch/glibc")
	}
	sort.Slice(candidates, func(i, j int) bool {
		return compareVersion(candidates[i].GlibcVersion, candidates[j].GlibcVersion) > 0
	})
	return candidates[0], nil
}

// version 定义版本号结构，包含主版本号和次版本号。
type version struct {
	Major int
	Minor int
}

// parsePackage 解析 MySQL 安装包文件名，提取版本号、架构和 glibc 版本信息。
func parsePackage(name string) (Package, bool) {
	if !strings.HasPrefix(name, "mysql-") || !strings.HasSuffix(name, ".tar.xz") {
		return Package{}, false
	}
	trimmed := strings.TrimSuffix(name, ".tar.xz")
	parts := strings.Split(trimmed, "-")
	if len(parts) < 5 {
		return Package{}, false
	}
	glibcIndex := -1
	for i, part := range parts {
		if strings.HasPrefix(part, "glibc") {
			glibcIndex = i
			break
		}
	}
	if glibcIndex < 0 || glibcIndex+1 >= len(parts) {
		return Package{}, false
	}
	glibc := strings.TrimPrefix(parts[glibcIndex], "glibc")
	v, err := parseVersion(glibc)
	if err != nil {
		return Package{}, false
	}
	return Package{
		FileName:     name,
		Version:      parts[1],
		Arch:         strings.Join(parts[glibcIndex+1:], "-"),
		GlibcVersion: v,
	}, true
}

// parseVersion 解析版本号字符串（如 "2.28"），返回主版本号和次版本号。
func parseVersion(raw string) (version, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) < 2 {
		return version{}, errors.New("invalid version: " + raw)
	}
	return version{Major: atoiSafe(parts[0]), Minor: atoiSafe(parts[1])}, nil
}

// compareVersion 比较两个版本号的大小，返回 -1、0 或 1。
func compareVersion(a, b version) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	return 0
}

// normalizeArch 将架构名称标准化为统一格式（x86_64 或 aarch64）。
func normalizeArch(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "x86_64", "amd64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}
