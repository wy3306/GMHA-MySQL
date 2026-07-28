// Package linuxcompat defines the Linux distribution contract shared by Agent
// onboarding and database installation.
package linuxcompat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Level string

const (
	LevelSupported   Level = "supported"
	LevelLegacy      Level = "legacy"
	LevelUnsupported Level = "unsupported"
	LevelUnknown     Level = "unknown"
)

type Report struct {
	OS             string   `json:"os"`
	Distribution   string   `json:"distribution"`
	Version        string   `json:"version,omitempty"`
	MajorVersion   int      `json:"major_version,omitempty"`
	Architecture   string   `json:"architecture,omitempty"`
	GlibcVersion   string   `json:"glibc_version,omitempty"`
	Level          Level    `json:"level"`
	CanInstall     bool     `json:"can_install"`
	PackageManager string   `json:"package_manager,omitempty"`
	Summary        string   `json:"summary"`
	Notes          []string `json:"notes,omitempty"`
	Blockers       []string `json:"blockers,omitempty"`
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]+)(?:\.([0-9]+))?`)

// Evaluate returns the product support tier. "legacy" remains installable but
// explicitly warns that the distribution is outside the primary test matrix.
func Evaluate(osName, arch, glibc string) Report {
	rawOS := strings.TrimSpace(osName)
	lower := strings.ToLower(rawOS)
	version, major := extractVersion(rawOS)
	report := Report{
		OS: rawOS, Version: version, MajorVersion: major,
		Architecture: normalizeArch(arch), GlibcVersion: strings.TrimSpace(glibc),
		Level: LevelUnknown, Summary: "未识别的 Linux 发行版，自动安装已阻止。",
		Blockers: []string{"发行版不在 GMHA Linux 兼容矩阵中"},
	}

	switch {
	case strings.Contains(lower, "ubuntu"):
		report.Distribution, report.PackageManager = "Ubuntu", "apt"
		switch {
		case major >= 20:
			report.Level, report.CanInstall, report.Summary = LevelSupported, true, "Ubuntu 20.04/22.04/24.04 属于支持范围。"
			report.Blockers = nil
		case major == 18:
			report.Level, report.CanInstall, report.Summary = LevelLegacy, true, "Ubuntu 18.04 可运行，但属于老版本兼容范围。"
			report.Notes = append(report.Notes, "建议迁移到 Ubuntu 22.04 或 24.04；第三方 PT/XtraBackup 包需单独核对。")
			report.Blockers = nil
		default:
			report.Level, report.Summary = LevelUnsupported, "Ubuntu 18.04 以下版本不支持自动部署。"
			report.Blockers = []string{"systemd、运行库和软件源版本低于当前兼容基线"}
		}
	case strings.Contains(lower, "centos stream"):
		report.Distribution, report.PackageManager = "CentOS Stream", "dnf"
		if major >= 8 {
			report.Level, report.CanInstall, report.Summary = LevelSupported, true, "CentOS Stream 8/9 属于支持范围。"
			report.Blockers = nil
		} else {
			report.Level, report.Summary = LevelUnsupported, "CentOS Stream 8 以下版本不支持。"
		}
	case strings.Contains(lower, "centos"):
		report.Distribution, report.PackageManager = "CentOS Linux", "yum"
		switch {
		case major == 7:
			report.Level, report.CanInstall, report.Summary = LevelLegacy, true, "CentOS 7 可运行，但仅作为 glibc 2.17 老版本兼容目标。"
			report.Notes = append(report.Notes, "CentOS 7 已停止维护；必须使用 glibc 2.17 兼容的 MySQL/工具制品。")
			report.Blockers = nil
		case major >= 8:
			report.Level, report.CanInstall, report.Summary = LevelLegacy, true, "CentOS Linux 8 可运行，但发行版已停止维护。"
			report.Notes = append(report.Notes, "建议改用 CentOS Stream、Rocky Linux 或 AlmaLinux。")
			report.Blockers = nil
		default:
			report.Level, report.Summary = LevelUnsupported, "CentOS 6 及更早版本不支持。"
			report.Blockers = []string{"缺少受支持的 systemd 基线，内核与运行库过旧"}
		}
	case strings.Contains(lower, "rocky"):
		report = supportedRHELFamily(report, "Rocky Linux", major)
	case strings.Contains(lower, "alma"):
		report = supportedRHELFamily(report, "AlmaLinux", major)
	case strings.Contains(lower, "red hat enterprise"), strings.Contains(lower, "rhel"):
		report = supportedRHELFamily(report, "RHEL", major)
	case strings.Contains(lower, "oracle linux"):
		report = supportedRHELFamily(report, "Oracle Linux", major)
	case strings.Contains(lower, "debian"):
		report.Distribution, report.PackageManager = "Debian", "apt"
		switch {
		case major >= 11:
			report.Level, report.CanInstall, report.Summary = LevelSupported, true, "Debian 11/12 属于支持范围。"
			report.Blockers = nil
		case major == 10:
			report.Level, report.CanInstall, report.Summary = LevelLegacy, true, "Debian 10 属于老版本兼容范围。"
			report.Blockers = nil
		default:
			report.Level, report.Summary = LevelUnsupported, "Debian 10 以下版本不支持。"
		}
	case strings.Contains(lower, "amazon linux 2023"):
		report.Distribution, report.PackageManager = "Amazon Linux", "dnf"
		report.Level, report.CanInstall, report.Summary, report.Blockers = LevelSupported, true, "Amazon Linux 2023 属于支持范围。", nil
	case strings.Contains(lower, "amazon linux"):
		report.Distribution, report.PackageManager = "Amazon Linux", "yum"
		report.Level, report.CanInstall, report.Summary, report.Blockers = LevelLegacy, true, "Amazon Linux 2 属于老版本兼容范围。", nil
	}

	if report.Architecture != "" && report.Architecture != "x86_64" && report.Architecture != "aarch64" {
		report.CanInstall = false
		report.Level = LevelUnsupported
		report.Blockers = append(report.Blockers, "仅支持 x86_64/amd64 与 aarch64/arm64 架构")
	}
	if report.GlibcVersion != "" && compareVersion(report.GlibcVersion, "2.17") < 0 {
		report.CanInstall = false
		report.Level = LevelUnsupported
		report.Blockers = append(report.Blockers, "glibc "+report.GlibcVersion+" 低于最低兼容基线 2.17")
	}
	return report
}

func supportedRHELFamily(report Report, name string, major int) Report {
	report.Distribution, report.PackageManager = name, "dnf"
	if major >= 8 {
		report.Level, report.CanInstall, report.Summary, report.Blockers = LevelSupported, true, name+" 8/9 属于支持范围。", nil
	} else if major == 7 {
		report.Level, report.CanInstall, report.Summary, report.Blockers = LevelLegacy, true, name+" 7 属于 glibc 2.17 老版本兼容范围。", nil
		report.PackageManager = "yum"
	} else {
		report.Level, report.Summary = LevelUnsupported, name+" 7 以下版本不支持。"
	}
	return report
}

func extractVersion(value string) (string, int) {
	match := versionPattern.FindStringSubmatch(value)
	if len(match) == 0 {
		return "", 0
	}
	major, _ := strconv.Atoi(match[1])
	version := match[1]
	if len(match) > 2 && match[2] != "" {
		version += "." + match[2]
	}
	return version, major
}

func normalizeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x64", "x86-64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func compareVersion(left, right string) int {
	parse := func(value string) (int, int) {
		match := versionPattern.FindStringSubmatch(value)
		if len(match) == 0 {
			return 0, 0
		}
		major, _ := strconv.Atoi(match[1])
		minor := 0
		if len(match) > 2 {
			minor, _ = strconv.Atoi(match[2])
		}
		return major, minor
	}
	leftMajor, leftMinor := parse(left)
	rightMajor, rightMinor := parse(right)
	if leftMajor != rightMajor {
		if leftMajor < rightMajor {
			return -1
		}
		return 1
	}
	if leftMinor < rightMinor {
		return -1
	}
	if leftMinor > rightMinor {
		return 1
	}
	return 0
}

func (r Report) Error() error {
	if r.CanInstall {
		return nil
	}
	reason := strings.Join(r.Blockers, "；")
	if reason == "" {
		reason = r.Summary
	}
	return fmt.Errorf("Linux 兼容性检查未通过：%s（系统=%s，架构=%s，glibc=%s）", reason, first(r.OS, "未知"), first(r.Architecture, "未知"), first(r.GlibcVersion, "未知"))
}

func first(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
