package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	flamegraphdomain "gmha/internal/domain/flamegraph"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
)

type FlameGraphCaptureRequest struct {
	ScheduleID  string `json:"schedule_id,omitempty"`
	MachineID   string `json:"machine_id"`
	TargetType  string `json:"target_type"`
	Target      string `json:"target,omitempty"`
	DurationSec int    `json:"duration_seconds"`
	FrequencyHz int    `json:"frequency_hz"`
	Backend     string `json:"backend"`
}

type FlameGraphService struct {
	repo     flamegraphdomain.Repository
	tasks    *TaskService
	machines machinedomain.Repository
	cancel   context.CancelFunc
	mu       sync.Mutex
	runMu    sync.Mutex
}

func NewFlameGraphService(repo flamegraphdomain.Repository, tasks *TaskService, machines machinedomain.Repository) *FlameGraphService {
	return &FlameGraphService{repo: repo, tasks: tasks, machines: machines}
}

func (s *FlameGraphService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.scheduleLoop(ctx)
}

func (s *FlameGraphService) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
}

func (s *FlameGraphService) Capture(ctx context.Context, req FlameGraphCaptureRequest) (flamegraphdomain.Profile, error) {
	if err := validateFlameGraphCapture(req); err != nil {
		return flamegraphdomain.Profile{}, err
	}
	machine, ok, err := s.machines.GetByID(ctx, strings.TrimSpace(req.MachineID))
	if err != nil {
		return flamegraphdomain.Profile{}, err
	}
	if !ok {
		return flamegraphdomain.Profile{}, errors.New("目标机器不存在")
	}
	now := time.Now().UTC()
	profile := flamegraphdomain.Profile{
		ID: newFlameGraphID("flamegraph"), ScheduleID: strings.TrimSpace(req.ScheduleID),
		Cluster: machine.Cluster, MachineID: machine.ID, MachineName: machine.Name, MachineIP: machine.IP,
		TargetType: req.TargetType, Target: strings.TrimSpace(req.Target), DurationSec: req.DurationSec,
		FrequencyHz: req.FrequencyHz, RequestedTool: req.Backend, Status: flamegraphdomain.StatusPending, CreatedAt: now,
	}
	if err := s.repo.CreateProfile(ctx, profile); err != nil {
		return flamegraphdomain.Profile{}, err
	}
	task, err := s.tasks.CreateFlameGraphTask(ctx, machine.ID, taskdomain.FlameGraphSpec{
		ProfileID: profile.ID, TargetType: profile.TargetType, Target: profile.Target,
		DurationSec: profile.DurationSec, FrequencyHz: profile.FrequencyHz, Backend: profile.RequestedTool,
	})
	if err != nil {
		finished := time.Now().UTC()
		_ = s.repo.CompleteProfile(ctx, profile.ID, flamegraphdomain.StatusFailed, "", 0, 0, "", err.Error(), now, finished)
		profile.Status, profile.Error = flamegraphdomain.StatusFailed, err.Error()
		return profile, err
	}
	profile.TaskID = task.Task.ID
	if err := s.repo.AttachProfileTask(ctx, profile.ID, profile.TaskID); err != nil {
		return flamegraphdomain.Profile{}, err
	}
	if task.Task.Status == taskdomain.StatusFailed {
		failure := "Agent 不支持火焰图任务，请先完成 Agent 离线升级"
		if len(task.Steps) > 0 && strings.TrimSpace(task.Steps[0].Message) != "" {
			failure = task.Steps[0].Message
		}
		finished := time.Now().UTC()
		_ = s.repo.CompleteProfile(ctx, profile.ID, flamegraphdomain.StatusFailed, "", 0, 0, "", failure, now, finished)
		profile.Status, profile.Error = flamegraphdomain.StatusFailed, failure
	}
	return profile, nil
}

func (s *FlameGraphService) SaveFlameGraphTaskResult(ctx context.Context, task taskdomain.Task, report taskdomain.ReportEnvelope, now time.Time) error {
	var spec taskdomain.FlameGraphSpec
	if err := json.Unmarshal(task.SpecJSON, &spec); err != nil {
		return err
	}
	if strings.TrimSpace(spec.ProfileID) == "" {
		return errors.New("flame graph task has no profile_id")
	}
	started := now.Add(-time.Duration(spec.DurationSec) * time.Second)
	if task.StartedAt != nil {
		started = task.StartedAt.UTC()
	}
	if report.Status == taskdomain.StatusFailed {
		failure := strings.TrimSpace(report.Error)
		if failure == "" && report.Step != nil {
			failure = strings.TrimSpace(report.Step.Message)
		}
		return s.repo.CompleteProfile(ctx, spec.ProfileID, flamegraphdomain.StatusFailed, "", 0, 0, "", failure, started, now)
	}
	var result taskdomain.FlameGraphResult
	if err := json.Unmarshal(report.Result, &result); err != nil {
		return fmt.Errorf("decode flame graph result: %w", err)
	}
	if result.ProfileID != "" && result.ProfileID != spec.ProfileID {
		return errors.New("flame graph result profile does not match task")
	}
	if strings.TrimSpace(result.FoldedStacks) == "" || result.SampleCount <= 0 {
		return errors.New("flame graph result contains no samples")
	}
	return s.repo.CompleteProfile(ctx, spec.ProfileID, flamegraphdomain.StatusSuccess, result.Backend,
		result.SampleCount, result.StackCount, result.FoldedStacks, "", started, now)
}

func (s *FlameGraphService) GetProfile(ctx context.Context, id string) (flamegraphdomain.Profile, bool, error) {
	profile, ok, err := s.repo.GetProfile(ctx, strings.TrimSpace(id))
	if err != nil || !ok {
		return profile, ok, err
	}
	s.decorateProfile(ctx, &profile)
	return profile, true, nil
}

func (s *FlameGraphService) ListProfiles(ctx context.Context, cluster string, limit int) ([]flamegraphdomain.Profile, error) {
	items, err := s.repo.ListProfiles(ctx, strings.TrimSpace(cluster), limit)
	if err != nil {
		return nil, err
	}
	for i := range items {
		s.decorateProfile(ctx, &items[i])
	}
	return items, nil
}

func (s *FlameGraphService) DeleteProfile(ctx context.Context, id string) error {
	return s.repo.DeleteProfile(ctx, strings.TrimSpace(id))
}

func (s *FlameGraphService) SaveSchedule(ctx context.Context, schedule flamegraphdomain.Schedule) (flamegraphdomain.Schedule, error) {
	schedule.ID = strings.TrimSpace(schedule.ID)
	if schedule.ID == "" {
		schedule.ID = newFlameGraphID("flamegraph-schedule")
	}
	if existing, ok, err := s.repo.GetSchedule(ctx, schedule.ID); err != nil {
		return schedule, err
	} else if ok {
		schedule.CreatedAt = existing.CreatedAt
		schedule.LastRunAt = existing.LastRunAt
	}
	schedule.Name = strings.TrimSpace(schedule.Name)
	if schedule.Name == "" {
		return schedule, errors.New("任务名称不能为空")
	}
	capture := FlameGraphCaptureRequest{
		MachineID: schedule.MachineID, TargetType: schedule.TargetType, Target: schedule.Target,
		DurationSec: schedule.DurationSec, FrequencyHz: schedule.FrequencyHz, Backend: schedule.Backend,
	}
	if err := validateFlameGraphCapture(capture); err != nil {
		return schedule, err
	}
	machine, ok, err := s.machines.GetByID(ctx, schedule.MachineID)
	if err != nil {
		return schedule, err
	}
	if !ok {
		return schedule, errors.New("目标机器不存在")
	}
	schedule.Cluster = machine.Cluster
	if schedule.StartAt.IsZero() {
		return schedule, errors.New("首次执行时间不能为空")
	}
	switch schedule.ScheduleType {
	case flamegraphdomain.ScheduleOnce, flamegraphdomain.ScheduleDaily:
	case flamegraphdomain.ScheduleInterval:
		if schedule.IntervalMinutes < 1 {
			return schedule, errors.New("循环间隔不能小于 1 分钟")
		}
	default:
		return schedule, errors.New("计划类型必须是 once、interval 或 daily")
	}
	now := time.Now().UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	schedule.UpdatedAt = now
	schedule.NextRunAt = nextFlameGraphRun(schedule, now, false)
	if schedule.Enabled && schedule.NextRunAt.IsZero() {
		return schedule, errors.New("无法计算下一次执行时间")
	}
	if err := s.repo.SaveSchedule(ctx, schedule); err != nil {
		return schedule, err
	}
	return schedule, nil
}

func (s *FlameGraphService) ListSchedules(ctx context.Context, cluster string) ([]flamegraphdomain.Schedule, error) {
	return s.repo.ListSchedules(ctx, strings.TrimSpace(cluster))
}

func (s *FlameGraphService) DeleteSchedule(ctx context.Context, id string) error {
	return s.repo.DeleteSchedule(ctx, strings.TrimSpace(id))
}

func (s *FlameGraphService) RunSchedule(ctx context.Context, id string) (flamegraphdomain.Profile, error) {
	schedule, ok, err := s.repo.GetSchedule(ctx, strings.TrimSpace(id))
	if err != nil {
		return flamegraphdomain.Profile{}, err
	}
	if !ok {
		return flamegraphdomain.Profile{}, errors.New("自动任务不存在")
	}
	return s.Capture(ctx, FlameGraphCaptureRequest{
		ScheduleID: schedule.ID, MachineID: schedule.MachineID, TargetType: schedule.TargetType, Target: schedule.Target,
		DurationSec: schedule.DurationSec, FrequencyHz: schedule.FrequencyHz, Backend: schedule.Backend,
	})
}

func (s *FlameGraphService) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	s.runDueSchedules(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueSchedules(ctx)
		}
	}
}

func (s *FlameGraphService) runDueSchedules(ctx context.Context) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	now := time.Now().UTC()
	items, err := s.repo.ListDueSchedules(ctx, now)
	if err != nil {
		log.Printf("flame graph scheduler: list due schedules: %v", err)
		return
	}
	for _, schedule := range items {
		if _, err := s.RunSchedule(ctx, schedule.ID); err != nil {
			log.Printf("flame graph scheduler: run %s: %v", schedule.ID, err)
		}
		next := nextFlameGraphRun(schedule, now, true)
		enabled := schedule.Enabled && !next.IsZero()
		if err := s.repo.UpdateScheduleRun(ctx, schedule.ID, now, next, enabled); err != nil {
			log.Printf("flame graph scheduler: advance %s: %v", schedule.ID, err)
		}
	}
}

func nextFlameGraphRun(schedule flamegraphdomain.Schedule, now time.Time, afterRun bool) time.Time {
	start := schedule.StartAt.UTC()
	switch schedule.ScheduleType {
	case flamegraphdomain.ScheduleOnce:
		if afterRun || !schedule.LastRunAt.IsZero() {
			return time.Time{}
		}
		if start.After(now) {
			return start
		}
		return now
	case flamegraphdomain.ScheduleInterval:
		interval := time.Duration(schedule.IntervalMinutes) * time.Minute
		if interval <= 0 {
			return time.Time{}
		}
		if start.After(now) {
			return start
		}
		steps := now.Sub(start)/interval + 1
		return start.Add(steps * interval)
	case flamegraphdomain.ScheduleDaily:
		if !afterRun && schedule.LastRunAt.IsZero() && start.After(now) {
			return start
		}
		next := time.Date(now.Year(), now.Month(), now.Day(), start.Hour(), start.Minute(), start.Second(), 0, time.UTC)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		return next
	default:
		return time.Time{}
	}
}

func validateFlameGraphCapture(req FlameGraphCaptureRequest) error {
	switch req.TargetType {
	case flamegraphdomain.TargetSystem:
	case flamegraphdomain.TargetPID:
		pid, err := strconv.Atoi(strings.TrimSpace(req.Target))
		if err != nil || pid <= 0 {
			return errors.New("PID 必须是正整数")
		}
	case flamegraphdomain.TargetProcess:
		if strings.TrimSpace(req.Target) == "" {
			return errors.New("进程名称不能为空")
		}
	default:
		return errors.New("采集目标必须是 system、pid 或 process")
	}
	if strings.TrimSpace(req.MachineID) == "" {
		return errors.New("目标机器不能为空")
	}
	if req.DurationSec < 1 || req.DurationSec > 600 {
		return errors.New("采集时长必须在 1 到 600 秒之间")
	}
	if req.FrequencyHz < 1 || req.FrequencyHz > 999 {
		return errors.New("采样频率必须在 1 到 999 Hz 之间")
	}
	switch req.Backend {
	case "", flamegraphdomain.BackendAuto, flamegraphdomain.BackendPerf, flamegraphdomain.BackendProcFS:
	default:
		return errors.New("采集后端必须是 auto、perf 或 procfs")
	}
	if req.TargetType == flamegraphdomain.TargetSystem && req.Backend == flamegraphdomain.BackendProcFS {
		return errors.New("全系统采集需要 perf，不能使用 procfs 后端")
	}
	return nil
}

func (s *FlameGraphService) decorateProfile(ctx context.Context, profile *flamegraphdomain.Profile) {
	if profile == nil || s.machines == nil {
		return
	}
	if machine, ok, err := s.machines.GetByID(ctx, profile.MachineID); err == nil && ok {
		profile.MachineName, profile.MachineIP = machine.Name, machine.IP
		if profile.Cluster == "" {
			profile.Cluster = machine.Cluster
		}
	}
	if profile.Status != flamegraphdomain.StatusSuccess && profile.Status != flamegraphdomain.StatusFailed && profile.TaskID != "" {
		if detail, err := s.tasks.GetTaskDetail(ctx, profile.TaskID); err == nil {
			profile.Status = string(detail.Task.Status)
			if detail.Task.Status == taskdomain.StatusFailed && profile.Error == "" && len(detail.Steps) > 0 {
				profile.Error = detail.Steps[0].Message
			}
		}
	}
}

func newFlameGraphID(prefix string) string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(suffix[:])
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
