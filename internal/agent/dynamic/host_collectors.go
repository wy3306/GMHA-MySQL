package dynamic

import (
	"bufio"
	"context"
	"errors"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	dyndomain "gmha/internal/domain/dynamic"
)

type builtinFuncCollector struct {
	name     string
	category string
	fn       func(context.Context, dyndomain.CollectTaskSpec) (any, string, error)
}

func builtinFunc(name, category string, fn func(context.Context, dyndomain.CollectTaskSpec) (any, string, error)) DynamicCollector {
	return builtinFuncCollector{name: name, category: category, fn: fn}
}

func (c builtinFuncCollector) Name() string { return c.name }

func (c builtinFuncCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	started := time.Now()
	value, valueType, err := c.fn(ctx, spec)
	if err != nil {
		return metricError(spec, err, time.Since(started).Milliseconds())
	}
	return metricOK(spec, c.category, valueType, value, started)
}

// CPUCollector 是 CPU 使用率采集器，通过读取 /proc/stat 计算 CPU 使用百分比。
type CPUCollector struct {
	mu   sync.Mutex
	prev cpuStat
}

type cpuStat struct {
	total uint64
	idle  uint64
}

// NewCPUCollector 创建一个新的 CPU 使用率采集器实例。
func NewCPUCollector() *CPUCollector { return &CPUCollector{} }
func (c *CPUCollector) Name() string { return "cpu_usage_percent" }

func (c *CPUCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	_ = ctx
	started := time.Now()
	cur, err := readCPUStat()
	if err != nil {
		return metricError(spec, err, time.Since(started).Milliseconds())
	}
	c.mu.Lock()
	prev := c.prev
	c.prev = cur
	c.mu.Unlock()
	if prev.total == 0 || cur.total <= prev.total {
		return metricOK(spec, "host", dyndomain.ValueTypeFloat, float64(0), started)
	}
	totalDelta := cur.total - prev.total
	idleDelta := cur.idle - prev.idle
	usage := 100 * (1 - float64(idleDelta)/float64(totalDelta))
	return metricOK(spec, "host", dyndomain.ValueTypeFloat, round2(usage), started)
}

func readCPUStat() (cpuStat, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuStat{}, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuStat{}, errors.New("invalid /proc/stat cpu line")
	}
	var vals []uint64
	for _, field := range fields[1:] {
		v, _ := strconv.ParseUint(field, 10, 64)
		vals = append(vals, v)
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	return cpuStat{total: total, idle: idle}, nil
}

// AgentCPUCollector samples /proc without spawning a process. Its 15-second
// default interval keeps self-observation overhead negligible.
type AgentCPUCollector struct {
	mu             sync.Mutex
	process, total uint64
}

func NewAgentCPUCollector() *AgentCPUCollector { return &AgentCPUCollector{} }
func (c *AgentCPUCollector) Name() string      { return "agent_cpu_usage_percent" }
func (c *AgentCPUCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	_ = ctx
	started := time.Now()
	proc, total, err := readAgentCPU()
	if err != nil {
		return metricError(spec, err, time.Since(started).Milliseconds())
	}
	c.mu.Lock()
	pp, pt := c.process, c.total
	c.process, c.total = proc, total
	c.mu.Unlock()
	value := 0.0
	if pt > 0 && total > pt && proc >= pp {
		value = 100 * float64(proc-pp) / float64(total-pt)
	}
	return metricOK(spec, "agent", dyndomain.ValueTypeFloat, round2(value), started)
}
func readAgentCPU() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 15 {
		return 0, 0, errors.New("invalid /proc/self/stat")
	}
	proc := parseUint(fields[13]) + parseUint(fields[14])
	cpu, err := readCPUStat()
	if err != nil {
		return 0, 0, err
	}
	return proc, cpu.total, nil
}
func collectAgentRSS(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	_ = ctx
	_ = spec
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return nil, dyndomain.ValueTypeFloat, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return round2(float64(parseUint(fields[1])) / 1024), dyndomain.ValueTypeFloat, nil
			}
		}
	}
	return nil, dyndomain.ValueTypeFloat, errors.New("VmRSS not found")
}

// IOCollector 是磁盘 IO 状态采集器，通过读取 /proc/diskstats 计算 IOPS、吞吐量和繁忙率。
type IOCollector struct {
	mu   sync.Mutex
	prev map[string]diskStat
	at   time.Time
}

type diskStat struct {
	readIOs      uint64
	writeIOs     uint64
	readSectors  uint64
	writeSectors uint64
	ioMS         uint64
}

// NewIOCollector 创建一个新的磁盘 IO 状态采集器实例。
func NewIOCollector() *IOCollector  { return &IOCollector{} }
func (c *IOCollector) Name() string { return "io_status" }

func (c *IOCollector) Collect(ctx context.Context, spec dyndomain.CollectTaskSpec) dyndomain.MetricResult {
	_ = ctx
	started := time.Now()
	cur, err := readDiskStats()
	if err != nil {
		return metricError(spec, err, time.Since(started).Milliseconds())
	}
	now := time.Now()
	c.mu.Lock()
	prev := c.prev
	prevAt := c.at
	c.prev = cur
	c.at = now
	c.mu.Unlock()
	if prev == nil || prevAt.IsZero() {
		return metricOK(spec, "host", dyndomain.ValueTypeMap, map[string]any{}, started)
	}
	seconds := now.Sub(prevAt).Seconds()
	out := map[string]any{}
	for dev, item := range cur {
		p, ok := prev[dev]
		if !ok || seconds <= 0 {
			continue
		}
		out[dev] = map[string]any{
			"read_iops":       round2(float64(item.readIOs-p.readIOs) / seconds),
			"write_iops":      round2(float64(item.writeIOs-p.writeIOs) / seconds),
			"read_bytes_sec":  round2(float64(item.readSectors-p.readSectors) * 512 / seconds),
			"write_bytes_sec": round2(float64(item.writeSectors-p.writeSectors) * 512 / seconds),
			"busy_ratio":      round2(float64(item.ioMS-p.ioMS) / (seconds * 1000)),
		}
	}
	return metricOK(spec, "host", dyndomain.ValueTypeMap, out, started)
}

func readDiskStats() (map[string]diskStat, error) {
	file, err := os.Open("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	out := map[string]diskStat{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		out[name] = diskStat{
			readIOs:      parseUint(fields[3]),
			readSectors:  parseUint(fields[5]),
			writeIOs:     parseUint(fields[7]),
			writeSectors: parseUint(fields[9]),
			ioMS:         parseUint(fields[12]),
		}
	}
	return out, scanner.Err()
}

func collectMemUsage(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	_ = ctx
	_ = spec
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, dyndomain.ValueTypeFloat, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			values[strings.TrimSuffix(fields[0], ":")] = parseUint(fields[1])
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 {
		return nil, dyndomain.ValueTypeFloat, errors.New("MemTotal is zero")
	}
	return round2(100 * (1 - float64(available)/float64(total))), dyndomain.ValueTypeFloat, nil
}

func collectLoadAverage(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	_ = ctx
	_ = spec
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, dyndomain.ValueTypeMap, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, dyndomain.ValueTypeMap, errors.New("invalid /proc/loadavg")
	}
	return map[string]any{"load1": parseFloat(fields[0]), "load5": parseFloat(fields[1]), "load15": parseFloat(fields[2])}, dyndomain.ValueTypeMap, nil
}

func collectNTPOffset(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	_ = spec
	out, err := exec.CommandContext(ctx, "/bin/bash", "-c", "chronyc tracking 2>/dev/null | awk -F: '/System time/ {print $2}' | awk '{print $1, $2}'").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return map[string]any{"offset_ms": nil, "source": "unavailable"}, dyndomain.ValueTypeMap, nil
	}
	fields := strings.Fields(string(out))
	offset := parseFloat(fields[0])
	if len(fields) > 1 && strings.Contains(fields[1], "slow") {
		offset = -offset
	}
	return map[string]any{"offset_ms": round2(offset * 1000), "source": "chronyc"}, dyndomain.ValueTypeMap, nil
}

func collectSSHProbe(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	port := spec.Params["port"]
	if port == "" {
		port = "22"
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	tcpOK := err == nil
	if conn != nil {
		_ = conn.Close()
	}
	serviceOK := false
	if out, err := exec.CommandContext(ctx, "/bin/bash", "-c", "systemctl is-active sshd 2>/dev/null || systemctl is-active ssh 2>/dev/null || pgrep -x sshd >/dev/null && echo active").Output(); err == nil {
		serviceOK = strings.Contains(string(out), "active")
	}
	return map[string]any{"port": port, "tcp_ok": tcpOK, "service_ok": serviceOK, "ok": tcpOK || serviceOK}, dyndomain.ValueTypeMap, nil
}

func collectInodeUsage(ctx context.Context, spec dyndomain.CollectTaskSpec) (any, string, error) {
	_ = ctx
	_ = spec
	mounts, err := mountPoints()
	if err != nil {
		return nil, dyndomain.ValueTypeArray, err
	}
	out := make([]map[string]any, 0, len(mounts))
	for _, mount := range mounts {
		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil || st.Files == 0 {
			continue
		}
		used := st.Files - st.Ffree
		out = append(out, map[string]any{"mount": mount, "used_percent": round2(100 * float64(used) / float64(st.Files))})
	}
	return out, dyndomain.ValueTypeArray, nil
}

func mountPoints() ([]string, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		fs := fields[2]
		if strings.HasPrefix(fs, "proc") || strings.HasPrefix(fs, "sysfs") || strings.HasPrefix(fs, "tmpfs") || fs == "devtmpfs" {
			continue
		}
		out = append(out, fields[1])
	}
	return out, nil
}

func parseUint(s string) uint64   { v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64); return v }
func parseFloat(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }
func round2(v float64) float64    { return math.Round(v*100) / 100 }
