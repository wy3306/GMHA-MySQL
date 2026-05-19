// Package collect 提供主机静态信息采集能力，包括 CPU、内存、磁盘、网络、操作系统等硬件和系统信息。
package collect

import (
	"context"
	"os"
	"time"

	collectdomain "gmha/internal/collect"
)

// Collector 定义主机信息采集器接口，实现该接口可采集完整的机器信息。
type Collector interface {
	Collect(ctx context.Context) (*collectdomain.MachineInfo, error)
}

// MachineCollector 是主机信息采集器的默认实现，通过系统命令采集各项主机指标。
type MachineCollector struct{}

// NewMachineCollector 创建一个新的主机信息采集器实例。
func NewMachineCollector() *MachineCollector {
	return &MachineCollector{}
}

// Collect 执行完整的主机信息采集，包括主机名、IP、CPU、内存、架构、glibc、操作系统、磁盘、SELinux、防火墙、Swap、NTP 等。
func (c *MachineCollector) Collect(ctx context.Context) (*collectdomain.MachineInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	ips, interfaces, err := collectNetwork(ctx)
	if err != nil {
		return nil, err
	}
	cpuCores, err := collectCPUCores(ctx)
	if err != nil {
		return nil, err
	}
	memoryGB, err := collectMemoryGB(ctx)
	if err != nil {
		return nil, err
	}
	arch, err := collectArch(ctx)
	if err != nil {
		return nil, err
	}
	glibcVersion, err := collectGlibcVersion(ctx)
	if err != nil {
		return nil, err
	}
	osInfo, err := collectOS(ctx)
	if err != nil {
		return nil, err
	}
	diskFreeGB, err := collectDiskFreeGB(ctx)
	if err != nil {
		return nil, err
	}
	selinux, err := collectSELinux(ctx)
	if err != nil {
		return nil, err
	}
	firewall, err := collectFirewall(ctx)
	if err != nil {
		return nil, err
	}
	swapEnabled, err := collectSwapEnabled(ctx)
	if err != nil {
		return nil, err
	}
	ntpEnabled, err := collectNTPEnabled(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	return &collectdomain.MachineInfo{
		Hostname:        hostname,
		IPs:             ips,
		Interfaces:      interfaces,
		CPUCores:        cpuCores,
		MemoryGB:        memoryGB,
		Arch:            arch,
		GlibcVersion:    glibcVersion,
		OS:              osInfo,
		DiskFreeGB:      diskFreeGB,
		SELinux:         selinux,
		Firewall:        firewall,
		SwapEnabled:     swapEnabled,
		NTPEnabled:      ntpEnabled,
		AgentTimeUnixMS: now.UnixMilli(),
	}, nil
}
