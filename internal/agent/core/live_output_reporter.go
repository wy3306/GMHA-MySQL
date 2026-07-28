package core

import (
	"fmt"
	"strings"
	"sync"
	"time"

	taskdomain "gmha/internal/domain/task"
)

const (
	liveOutputFlushInterval = 750 * time.Millisecond
	liveOutputChunkBytes    = 8 * 1024
	liveOutputPreviewRunes  = 140
)

// LiveOutputReporter batches command output into small task events while a
// command is still running. The first line is published immediately; noisy
// commands are then throttled so package-manager output does not create one
// websocket message per line.
type LiveOutputReporter struct {
	reporter  *Reporter
	taskID    string
	step      taskdomain.DispatchStep
	progress  int
	startedAt time.Time

	mu        sync.Mutex
	buffer    strings.Builder
	lastFlush time.Time
	lineCount int
	emitted   bool
}

func NewLiveOutputReporter(reporter *Reporter, taskID string, step taskdomain.DispatchStep, progress int, startedAt time.Time) *LiveOutputReporter {
	return &LiveOutputReporter{
		reporter:  reporter,
		taskID:    taskID,
		step:      step,
		progress:  progress,
		startedAt: startedAt,
	}
}

// Add accepts one complete stdout/stderr line.
func (r *LiveOutputReporter) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	r.mu.Lock()
	if r.buffer.Len() > 0 {
		r.buffer.WriteByte('\n')
	}
	r.buffer.WriteString(line)
	r.lineCount++
	now := time.Now()
	shouldFlush := !r.emitted ||
		r.buffer.Len() >= liveOutputChunkBytes ||
		(!r.lastFlush.IsZero() && now.Sub(r.lastFlush) >= liveOutputFlushInterval)
	if !shouldFlush {
		r.mu.Unlock()
		return
	}
	content := r.takeLocked(now)
	r.mu.Unlock()
	r.publish(content, line)
}

// Flush publishes any output that remains buffered after the process exits.
func (r *LiveOutputReporter) Flush() {
	r.mu.Lock()
	if r.buffer.Len() == 0 {
		r.mu.Unlock()
		return
	}
	content := r.takeLocked(time.Now())
	lastLine := lastNonEmptyLine(content)
	r.mu.Unlock()
	r.publish(content, lastLine)
}

func (r *LiveOutputReporter) LineCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lineCount
}

func (r *LiveOutputReporter) Emitted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.emitted
}

func (r *LiveOutputReporter) SuccessMessage(fallback string) string {
	count := r.LineCount()
	if count == 0 {
		return fallback + "（无命令输出）"
	}
	return fmt.Sprintf("%s（共输出 %d 行）", fallback, count)
}

func (r *LiveOutputReporter) takeLocked(now time.Time) string {
	content := r.buffer.String()
	r.buffer.Reset()
	r.lastFlush = now
	r.emitted = true
	return content
}

func (r *LiveOutputReporter) publish(content, lastLine string) {
	if r == nil || r.reporter == nil || strings.TrimSpace(content) == "" {
		return
	}
	preview := truncateRunes(strings.Join(strings.Fields(lastLine), " "), liveOutputPreviewRunes)
	message := "正在执行"
	if preview != "" {
		message += " · " + preview
	}
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.taskID,
		Status:      taskdomain.StatusRunning,
		Progress:    r.progress,
		CurrentStep: r.step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    r.step.ID,
			StepNo:    r.step.StepNo,
			StepName:  r.step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   message,
			StartedAt: &r.startedAt,
		},
		Event: &taskdomain.Event{
			TaskID:    r.taskID,
			StepID:    r.step.ID,
			EventType: taskdomain.EventLog,
			Content:   content,
		},
	})
}

func lastNonEmptyLine(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
