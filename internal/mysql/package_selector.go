// package_selector.go 实现 MySQL 安装包的自动选择，根据机器架构和 glibc 版本匹配最合适的安装包。
package mysql

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

// PackageOption 是前端选择安装版本时使用的可序列化安装包信息。
type PackageOption struct {
	FileName               string                  `json:"file_name"`
	Version                string                  `json:"version"`
	Arch                   string                  `json:"arch"`
	GlibcVersion           string                  `json:"glibc_version"`
	ReleaseTrack           string                  `json:"release_track"`
	PTToolsSupported       bool                    `json:"pt_tools_supported"`
	RuntimeParameterGroups []RuntimeParameterGroup `json:"runtime_parameter_groups"`
}

// PackageSelector 是安装包选择器，负责根据机器信息从本地软件目录中选择最合适的 MySQL 安装包。
type PackageSelector struct {
	mu          sync.RWMutex
	softwareDir string
}

// NewPackageSelector 创建并返回一个新的安装包选择器实例。
func NewPackageSelector(softwareDir string) *PackageSelector {
	return &PackageSelector{softwareDir: softwareDir}
}

// SetSoftwareDir 动态更新 MySQL 安装包目录，使安装包管理中修改的存储位置立即参与后续安装任务选择。
func (s *PackageSelector) SetSoftwareDir(softwareDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.softwareDir = softwareDir
}

func (s *PackageSelector) currentSoftwareDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.softwareDir
}

// Select 根据机器信息从软件目录中选择匹配架构和 glibc 版本的最优 MySQL 安装包。
func (s *PackageSelector) Select(info collectdomain.MachineInfo) (Package, error) {
	return s.SelectVersionArchitecture(info, "", "")
}

// SelectVersionArchitecture 根据独立的 MySQL 版本和 CPU 架构选择最终制品。
// 版本或架构留空时分别自动选择最高兼容版本和目标机器架构；最终仍返回具体
// package_name，以兼容既有 Agent 的下载与解压协议。
func (s *PackageSelector) SelectVersionArchitecture(info collectdomain.MachineInfo, mysqlVersion, architecture string) (Package, error) {
	softwareDir := s.currentSoftwareDir()
	entries, err := os.ReadDir(softwareDir)
	if err != nil {
		return Package{}, err
	}
	targetArch := normalizeArch(info.Arch)
	requestedArch := normalizeArch(architecture)
	if requestedArch == "" {
		requestedArch = targetArch
	}
	if targetArch != "" && requestedArch != targetArch {
		return Package{}, fmt.Errorf("selected architecture %s is not compatible with target machine architecture %s", requestedArch, info.Arch)
	}
	mysqlVersion = strings.TrimSpace(mysqlVersion)
	if mysqlVersion != "" {
		if _, err := validateSupportedMySQLVersion(mysqlVersion); err != nil {
			return Package{}, err
		}
	}
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
		if _, err := validateSupportedMySQLVersion(item.Version); err != nil {
			continue
		}
		if normalizeArch(item.Arch) != requestedArch {
			continue
		}
		if mysqlVersion != "" && item.Version != mysqlVersion {
			continue
		}
		if compareVersion(item.GlibcVersion, targetGlibc) > 0 {
			continue
		}
		item.FullPath = filepath.Join(softwareDir, item.FileName)
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		available := make([]string, 0)
		for _, entry := range entries {
			if item, ok := parsePackage(entry.Name()); ok && normalizeArch(item.Arch) == requestedArch {
				available = append(available, fmt.Sprintf("%s/glibc%d.%d", item.Version, item.GlibcVersion.Major, item.GlibcVersion.Minor))
			}
		}
		sort.Strings(available)
		return Package{}, fmt.Errorf("没有兼容的本地 MySQL 安装包：版本=%s，架构=%s，目标 glibc=%s；当前同架构制品=%s", firstNonEmpty(mysqlVersion, "自动"), requestedArch, info.GlibcVersion, firstNonEmpty(strings.Join(available, "、"), "无"))
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, _ := parseMySQLVersion(candidates[i].Version)
		right, _ := parseMySQLVersion(candidates[j].Version)
		if cmp := compareMySQLVersion(left, right); cmp != 0 {
			return cmp > 0
		}
		return compareVersion(candidates[i].GlibcVersion, candidates[j].GlibcVersion) > 0
	})
	return candidates[0], nil
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

// SelectNamed 选择用户指定的安装包，并继续校验目标机器架构和 glibc 兼容性。
func (s *PackageSelector) SelectNamed(info collectdomain.MachineInfo, fileName string) (Package, error) {
	fileName = filepath.Base(strings.TrimSpace(fileName))
	if fileName == "" || fileName == "." {
		return Package{}, errors.New("package name is required")
	}
	item, ok := parsePackage(fileName)
	if !ok {
		return Package{}, errors.New("invalid mysql package file name")
	}
	if _, err := validateSupportedMySQLVersion(item.Version); err != nil {
		return Package{}, err
	}
	if normalizeArch(item.Arch) != normalizeArch(info.Arch) {
		return Package{}, fmt.Errorf("mysql package %s is not compatible with target architecture %s", fileName, info.Arch)
	}
	targetGlibc, err := parseVersion(info.GlibcVersion)
	if err != nil {
		return Package{}, err
	}
	if compareVersion(item.GlibcVersion, targetGlibc) > 0 {
		return Package{}, fmt.Errorf("mysql package %s requires glibc %d.%d but target is %s", fileName, item.GlibcVersion.Major, item.GlibcVersion.Minor, info.GlibcVersion)
	}
	softwareDir := s.currentSoftwareDir()
	path := filepath.Join(softwareDir, fileName)
	if _, err := os.Stat(path); err != nil {
		return Package{}, err
	}
	item.FullPath = path
	return item, nil
}

// ListOptions 返回当前 MySQL 目录下可供页面选择的版本和架构信息。
func (s *PackageSelector) ListOptions() ([]PackageOption, error) {
	softwareDir := s.currentSoftwareDir()
	entries, err := os.ReadDir(softwareDir)
	if err != nil {
		return nil, err
	}
	items := make([]PackageOption, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		item, ok := parsePackage(entry.Name())
		if !ok {
			continue
		}
		mysqlVer, err := validateSupportedMySQLVersion(item.Version)
		if err != nil {
			continue
		}
		groups, _ := RuntimeParameterGroupsForVersion(item.Version)
		items = append(items, PackageOption{FileName: item.FileName, Version: item.Version, Arch: normalizeArch(item.Arch), GlibcVersion: fmt.Sprintf("%d.%d", item.GlibcVersion.Major, item.GlibcVersion.Minor), ReleaseTrack: mysqlReleaseTrack(mysqlVer), PTToolsSupported: SupportsPerconaToolkit(item.Version), RuntimeParameterGroups: groups})
	}
	sort.Slice(items, func(i, j int) bool {
		left, _ := parseMySQLVersion(items[i].Version)
		right, _ := parseMySQLVersion(items[j].Version)
		if cmp := compareMySQLVersion(left, right); cmp != 0 {
			return cmp > 0
		}
		return items[i].FileName > items[j].FileName
	})
	return items, nil
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

// PackageVersion returns the server version encoded in a supported package
// file name. It is used by upgrade planning without exposing parser internals.
func PackageVersion(name string) (string, error) {
	item, ok := parsePackage(filepath.Base(strings.TrimSpace(name)))
	if !ok {
		return "", errors.New("invalid mysql package file name")
	}
	return item.Version, nil
}

// PackageArchitecture returns the normalized CPU architecture encoded in a
// supported package file name. It supports backfilling structured instance
// metadata for records created before version/architecture were split.
func PackageArchitecture(name string) (string, error) {
	item, ok := parsePackage(filepath.Base(strings.TrimSpace(name)))
	if !ok {
		return "", errors.New("invalid mysql package file name")
	}
	return normalizeArch(item.Arch), nil
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

// NormalizeArchitecture exposes the canonical architecture names used by
// installation requests, instance metadata and upgrade validation.
func NormalizeArchitecture(v string) string { return normalizeArch(v) }
