// Package core 提供代理核心运行时组件，包括命令执行、任务分发、心跳上报、WebSocket 任务接收等功能。
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CommandRunner 是命令执行器，支持通过 systemd-run 或进程组方式执行 Shell 命令，并处理超时和取消。
type CommandRunner struct {
	preferSystemd bool
}

// NewCommandRunner 创建一个新的命令执行器，自动检测是否可用 systemd-run。
func NewCommandRunner() *CommandRunner {
	return &CommandRunner{preferSystemd: canUseSystemdRun()}
}

// RunShell 执行 Shell 命令，优先使用 systemd-run，不可用时回退到进程组执行方式。
func (r *CommandRunner) RunShell(ctx context.Context, taskID, stepName, command string) (string, error) {
	return r.RunShellWithOutput(ctx, taskID, stepName, command, nil)
}

// RunShellWithOutput executes a command and reports complete stdout/stderr
// lines while it is running. Long workflows such as pt-online-schema-change
// use this to publish copy percentage without waiting for the process to exit.
func (r *CommandRunner) RunShellWithOutput(ctx context.Context, taskID, stepName, command string, onOutput func(string)) (string, error) {
	if r != nil && r.preferSystemd {
		output, err := r.runWithSystemd(ctx, taskID, stepName, command, onOutput)
		if err == nil {
			return output, nil
		}
		// Some older distributions lack systemd-run --pipe/--wait. Fall back to
		// process-group execution so tasks still work, but keep the real error in
		// output for diagnostics if the fallback also fails.
		fallbackOutput, fallbackErr := runShellInProcessGroupWithOutput(ctx, command, onOutput)
		if fallbackErr != nil && strings.TrimSpace(output) != "" {
			return joinCommandOutput(output, fallbackOutput), fallbackErr
		}
		return fallbackOutput, fallbackErr
	}
	return runShellInProcessGroupWithOutput(ctx, command, onOutput)
}

func (r *CommandRunner) runWithSystemd(ctx context.Context, taskID, stepName, command string, onOutput func(string)) (string, error) {
	unit := systemdUnitName(taskID, stepName)
	args := []string{
		"--quiet",
		"--collect",
		"--wait",
		"--pipe",
		"--unit", unit,
		"--property", "CPUAccounting=yes",
		"--property", "MemoryAccounting=yes",
		"--property", "KillMode=mixed",
		"--property", "TimeoutStopSec=15s",
		"/bin/sh", "-lc", command,
	}
	cmd := exec.Command("systemd-run", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	onOutput = serializedOutputCallback(onOutput)
	stdoutStream, stderrStream := newCommandOutputWriter(&stdout, onOutput), newCommandOutputWriter(&stderr, onOutput)
	cmd.Stdout = stdoutStream
	cmd.Stderr = stderrStream
	if err := cmd.Start(); err != nil {
		return joinCommandOutput(stdout.String(), stderr.String()), err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		stdoutStream.Flush()
		stderrStream.Flush()
		return joinCommandOutput(stdout.String(), stderr.String()), err
	case <-ctx.Done():
		_ = exec.Command("systemctl", "kill", "--kill-who=all", unit).Run()
		_ = exec.Command("systemctl", "stop", unit).Run()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err := <-done:
			stdoutStream.Flush()
			stderrStream.Flush()
			return joinCommandOutput(stdout.String(), stderr.String()), err
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err := <-done
			stdoutStream.Flush()
			stderrStream.Flush()
			return joinCommandOutput(stdout.String(), stderr.String()), err
		}
	}
}

func runShellInProcessGroup(ctx context.Context, command string) (string, error) {
	return runShellInProcessGroupWithOutput(ctx, command, nil)
}

func runShellInProcessGroupWithOutput(ctx context.Context, command string, onOutput func(string)) (string, error) {
	cmd := exec.Command("sh", "-lc", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	onOutput = serializedOutputCallback(onOutput)
	stdoutStream, stderrStream := newCommandOutputWriter(&stdout, onOutput), newCommandOutputWriter(&stderr, onOutput)
	cmd.Stdout = stdoutStream
	cmd.Stderr = stderrStream
	if err := cmd.Start(); err != nil {
		return joinCommandOutput(stdout.String(), stderr.String()), err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		stdoutStream.Flush()
		stderrStream.Flush()
		return joinCommandOutput(stdout.String(), stderr.String()), err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err := <-done:
			stdoutStream.Flush()
			stderrStream.Flush()
			return joinCommandOutput(stdout.String(), stderr.String()), err
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err := <-done
			stdoutStream.Flush()
			stderrStream.Flush()
			return joinCommandOutput(stdout.String(), stderr.String()), err
		}
	}
}

type commandOutputWriter struct {
	target   *bytes.Buffer
	callback func(string)
	pending  []byte
	mu       sync.Mutex
}

func newCommandOutputWriter(target *bytes.Buffer, callback func(string)) *commandOutputWriter {
	return &commandOutputWriter{target: target, callback: callback}
}

func serializedOutputCallback(callback func(string)) func(string) {
	if callback == nil {
		return nil
	}
	var mu sync.Mutex
	return func(line string) {
		mu.Lock()
		defer mu.Unlock()
		callback(line)
	}
}

func (w *commandOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.target.Write(data)
	w.pending = append(w.pending, data...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		w.emit(w.pending[:index])
		w.pending = w.pending[index+1:]
	}
	return n, err
}

func (w *commandOutputWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(w.pending)
		w.pending = nil
	}
}

func (w *commandOutputWriter) emit(data []byte) {
	if w.callback == nil {
		return
	}
	if line := strings.TrimSpace(string(data)); line != "" {
		w.callback(line)
	}
}

func canUseSystemdRun() bool {
	if os.Geteuid() != 0 {
		return false
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	return true
}

func systemdUnitName(taskID, stepName string) string {
	name := "gmha-task-" + sanitizeUnitPart(taskID) + "-" + sanitizeUnitPart(stepName)
	if len(name) > 180 {
		name = name[:180]
	}
	return strings.TrimRight(name, "-") + ".service"
}

var unitPartPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeUnitPart(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	v = unitPartPattern.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-_")
	if v == "" {
		return "unknown"
	}
	return v
}

func joinCommandOutput(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}

// IsContextCanceled 判断错误是否由上下文取消或超时引起。
func IsContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// CommandErrorWithOutput 将命令执行错误和输出合并为一个错误信息。
func CommandErrorWithOutput(err error, output string) error {
	if strings.TrimSpace(output) == "" {
		return err
	}
	return fmt.Errorf("%v: %s", err, output)
}
