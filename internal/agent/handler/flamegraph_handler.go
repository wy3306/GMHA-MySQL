package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "gmha/internal/agent/core"
	flamegraphdomain "gmha/internal/domain/flamegraph"
	taskdomain "gmha/internal/domain/task"
)

// FlameGraphHandler performs Linux-native stack sampling. It deliberately
// emits folded stacks instead of SVG: Manager can persist the compact source
// data and every browser can render/search/zoom it without server-side tools.
type FlameGraphHandler struct{}

func NewFlameGraphHandler() *FlameGraphHandler { return &FlameGraphHandler{} }
func (h *FlameGraphHandler) Type() string      { return string(taskdomain.TypeFlameGraph) }

func (h *FlameGraphHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	var spec taskdomain.FlameGraphSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return fmt.Errorf("decode flame graph spec: %w", err)
	}
	if err := validateFlameGraphSpec(spec); err != nil {
		return err
	}
	step := firstStep(task.Steps)
	started := time.Now().UTC()
	_ = reporter.Report(taskdomain.ReportEnvelope{
		TaskID: task.ID, Status: taskdomain.StatusRunning, Progress: 5, CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepRunning, Message: "正在采集 Linux 调用栈", StartedAt: &started},
	})

	var (
		folded map[string]int64
		used   string
		err    error
	)
	if spec.Backend != flamegraphdomain.BackendProcFS {
		folded, err = collectPerfStacks(ctx, spec)
		if err == nil {
			used = flamegraphdomain.BackendPerf
		}
	}
	if err != nil || spec.Backend == flamegraphdomain.BackendProcFS {
		perfErr := err
		if spec.Backend == flamegraphdomain.BackendPerf {
			return fmt.Errorf("perf sampling failed: %w", err)
		}
		folded, err = collectProcStacks(ctx, spec)
		if err != nil {
			return fmt.Errorf("stack sampling failed (perf: %v; procfs: %w)", errOrUnavailable(perfErr, spec.Backend), err)
		}
		used = flamegraphdomain.BackendProcFS
	}
	text, samples, stackCount := encodeFoldedStacks(folded)
	if samples == 0 {
		return errors.New("sampling completed but no stack samples were readable")
	}
	result, _ := json.Marshal(taskdomain.FlameGraphResult{
		ProfileID: spec.ProfileID, Backend: used, SampleCount: samples,
		StackCount: stackCount, FoldedStacks: text,
	})
	finished := time.Now().UTC()
	message := fmt.Sprintf("采集完成：%d 个样本，%d 条唯一调用栈，后端 %s", samples, stackCount, used)
	return reporter.Report(taskdomain.ReportEnvelope{
		TaskID: task.ID, Status: taskdomain.StatusSuccess, Progress: 100, CurrentStep: step.StepName, Result: result,
		Step:  &taskdomain.StepReport{StepID: step.ID, StepNo: step.StepNo, StepName: step.StepName, Status: taskdomain.StepSuccess, Message: message, StartedAt: &started, FinishedAt: &finished},
		Event: &taskdomain.Event{TaskID: task.ID, StepID: step.ID, EventType: taskdomain.EventInfo, Content: message},
	})
}

func validateFlameGraphSpec(spec taskdomain.FlameGraphSpec) error {
	if strings.TrimSpace(spec.ProfileID) == "" {
		return errors.New("profile_id is required")
	}
	switch spec.TargetType {
	case flamegraphdomain.TargetSystem:
	case flamegraphdomain.TargetPID:
		if pid, err := strconv.Atoi(strings.TrimSpace(spec.Target)); err != nil || pid <= 0 {
			return errors.New("PID target must be a positive integer")
		}
	case flamegraphdomain.TargetProcess:
		if strings.TrimSpace(spec.Target) == "" {
			return errors.New("process target is required")
		}
	default:
		return errors.New("target_type must be system, pid, or process")
	}
	if spec.DurationSec < 1 || spec.DurationSec > 600 {
		return errors.New("duration_seconds must be between 1 and 600")
	}
	if spec.FrequencyHz < 1 || spec.FrequencyHz > 999 {
		return errors.New("frequency_hz must be between 1 and 999")
	}
	switch spec.Backend {
	case "", flamegraphdomain.BackendAuto, flamegraphdomain.BackendPerf, flamegraphdomain.BackendProcFS:
	default:
		return errors.New("backend must be auto, perf, or procfs")
	}
	return nil
}

func collectPerfStacks(ctx context.Context, spec taskdomain.FlameGraphSpec) (map[string]int64, error) {
	perf, err := exec.LookPath("perf")
	if err != nil {
		for _, candidate := range []string{"/opt/gmha-tools/flamegraph/bin/perf", "/usr/lib/linux-tools/perf"} {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				perf, err = candidate, nil
				break
			}
		}
		if err != nil {
			return nil, errors.New("perf executable is not installed")
		}
	}
	pid, err := resolveFlameGraphPID(spec)
	if err != nil {
		return nil, err
	}
	dataFile, err := os.CreateTemp("", "gmha-perf-*.data")
	if err != nil {
		return nil, err
	}
	path := dataFile.Name()
	_ = dataFile.Close()
	defer os.Remove(path)

	args := []string{"record", "-q", "-F", strconv.Itoa(spec.FrequencyHz), "-g", "--call-graph", "fp", "-o", path}
	if spec.TargetType == flamegraphdomain.TargetSystem {
		args = append(args, "-a")
	} else {
		args = append(args, "-p", strconv.Itoa(pid))
	}
	args = append(args, "--", "sleep", strconv.Itoa(spec.DurationSec))
	record := exec.CommandContext(ctx, perf, args...)
	record.Env = append(os.Environ(), "LC_ALL=C")
	if output, runErr := record.CombinedOutput(); runErr != nil {
		return nil, fmt.Errorf("%v: %s", runErr, strings.TrimSpace(string(output)))
	}
	script := exec.CommandContext(ctx, perf, "script", "-i", path)
	script.Env = append(os.Environ(), "LC_ALL=C")
	output, runErr := script.Output()
	if runErr != nil {
		return nil, fmt.Errorf("perf script: %w", runErr)
	}
	stacks := parsePerfScript(output)
	if len(stacks) == 0 {
		return nil, errors.New("perf returned no readable stack samples; frame pointers or symbols may be unavailable")
	}
	return stacks, nil
}

func collectProcStacks(ctx context.Context, spec taskdomain.FlameGraphSpec) (map[string]int64, error) {
	if spec.TargetType == flamegraphdomain.TargetSystem {
		return nil, errors.New("procfs fallback requires a PID or process target")
	}
	pid, err := resolveFlameGraphPID(spec)
	if err != nil {
		return nil, err
	}
	commBytes, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	processName := sanitizeFrame(strings.TrimSpace(string(commBytes)))
	if processName == "" {
		processName = "pid-" + strconv.Itoa(pid)
	}
	stackPath := filepath.Join("/proc", strconv.Itoa(pid), "stack")
	interval := time.Second / time.Duration(spec.FrequencyHz)
	if interval < 5*time.Millisecond {
		interval = 5 * time.Millisecond
	}
	deadline := time.NewTimer(time.Duration(spec.DurationSec) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	stacks := make(map[string]int64)
	readOnce := func() error {
		raw, readErr := os.ReadFile(stackPath)
		if readErr != nil {
			return readErr
		}
		frames := parseProcStack(raw)
		if len(frames) == 0 {
			return nil
		}
		frames = append([]string{"process:" + processName}, frames...)
		stacks[strings.Join(frames, ";")]++
		return nil
	}
	if err := readOnce(); err != nil {
		return nil, fmt.Errorf("read %s: %w (root or CAP_SYS_PTRACE may be required)", stackPath, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return stacks, nil
		case <-ticker.C:
			_ = readOnce() // A short-lived process may disappear near the end.
		}
	}
}

func resolveFlameGraphPID(spec taskdomain.FlameGraphSpec) (int, error) {
	if spec.TargetType == flamegraphdomain.TargetSystem {
		return 0, nil
	}
	if spec.TargetType == flamegraphdomain.TargetPID {
		pid, err := strconv.Atoi(strings.TrimSpace(spec.Target))
		if err != nil || pid <= 0 {
			return 0, errors.New("invalid PID")
		}
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err != nil {
			return 0, fmt.Errorf("PID %d does not exist", pid)
		}
		return pid, nil
	}
	target := strings.ToLower(strings.TrimSpace(spec.Target))
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	pids := make([]int, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr == nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	for _, pid := range pids {
		base := filepath.Join("/proc", strconv.Itoa(pid))
		comm, _ := os.ReadFile(filepath.Join(base, "comm"))
		cmdline, _ := os.ReadFile(filepath.Join(base, "cmdline"))
		name := strings.ToLower(strings.TrimSpace(string(comm)))
		command := strings.ToLower(strings.ReplaceAll(string(cmdline), "\x00", " "))
		if name == target || strings.Contains(command, target) {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("process %q was not found", spec.Target)
}

func parsePerfScript(raw []byte) map[string]int64 {
	result := make(map[string]int64)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var frames []string
	process := ""
	flush := func() {
		if len(frames) == 0 {
			process = ""
			return
		}
		stack := make([]string, 0, len(frames)+1)
		if process != "" {
			stack = append(stack, "process:"+process)
		}
		for i := len(frames) - 1; i >= 0; i-- {
			stack = append(stack, frames[i])
		}
		result[strings.Join(stack, ";")]++
		frames, process = nil, ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			flush()
			fields := strings.Fields(line)
			if len(fields) > 0 {
				process = sanitizeFrame(fields[0])
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		frame := fields[1]
		if offset := strings.LastIndex(frame, "+0x"); offset > 0 {
			frame = frame[:offset]
		}
		frame = sanitizeFrame(frame)
		if frame != "" {
			frames = append(frames, frame)
		}
	}
	flush()
	return result
}

func parseProcStack(raw []byte) []string {
	var leafFirst []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		frame := fields[1]
		if offset := strings.IndexByte(frame, '+'); offset > 0 {
			frame = frame[:offset]
		}
		frame = sanitizeFrame(frame)
		if frame != "" {
			leafFirst = append(leafFirst, frame)
		}
	}
	out := make([]string, 0, len(leafFirst))
	for i := len(leafFirst) - 1; i >= 0; i-- {
		out = append(out, leafFirst[i])
	}
	return out
}

func sanitizeFrame(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, ";", ":")
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}

func encodeFoldedStacks(stacks map[string]int64) (string, int64, int) {
	type foldedEntry struct {
		stack string
		count int64
	}
	entries := make([]foldedEntry, 0, len(stacks))
	for stack, count := range stacks {
		if count > 0 && strings.TrimSpace(stack) != "" {
			entries = append(entries, foldedEntry{stack: stack, count: count})
		}
	}
	// Preserve the hottest stacks first when the WebSocket-safe 8 MiB result
	// limit is reached. A pathological high-cardinality profile remains useful
	// instead of exhausting Agent or Manager memory during report delivery.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].stack < entries[j].stack
	})
	var samples int64
	var output strings.Builder
	kept := 0
	for _, entry := range entries {
		line := fmt.Sprintf("%s %d\n", entry.stack, entry.count)
		if output.Len() > 0 && output.Len()+len(line) > 8<<20 {
			break
		}
		output.WriteString(line)
		samples += entry.count
		kept++
	}
	return output.String(), samples, kept
}

func errOrUnavailable(perfErr error, backend string) error {
	if perfErr != nil {
		return perfErr
	}
	if backend == flamegraphdomain.BackendProcFS {
		return errors.New("perf was disabled by request")
	}
	return errors.New("perf unavailable")
}
