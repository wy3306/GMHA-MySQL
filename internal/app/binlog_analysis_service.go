package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gmha/internal/binloganalyzer"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

const (
	BinlogAnalysisQueued    = "queued"
	BinlogAnalysisRunning   = "running"
	BinlogAnalysisCompleted = "completed"
	BinlogAnalysisFailed    = "failed"
	BinlogAnalysisCanceled  = "canceled"
)

type BinlogAnalysisRequest struct {
	MachineID            string
	Port                 int
	StartTime            time.Time
	EndTime              time.Time
	StartFile            string
	BigTxnMode           string
	BigTxnRowsThreshold  int
	BigTxnBytesThreshold uint64
}

type BinlogAnalysisRequestView struct {
	MachineID            string    `json:"machine_id"`
	MachineName          string    `json:"machine_name"`
	MachineIP            string    `json:"machine_ip"`
	Port                 int       `json:"port"`
	StartTime            time.Time `json:"start_time"`
	EndTime              time.Time `json:"end_time"`
	StartFile            string    `json:"start_file,omitempty"`
	BigTxnMode           string    `json:"big_txn_mode"`
	BigTxnRowsThreshold  int       `json:"big_txn_rows_threshold"`
	BigTxnBytesThreshold uint64    `json:"big_txn_bytes_threshold"`
}

type BinlogAnalysisTask struct {
	ID         string                    `json:"id"`
	Status     string                    `json:"status"`
	Error      string                    `json:"error,omitempty"`
	CreatedAt  time.Time                 `json:"created_at"`
	StartedAt  *time.Time                `json:"started_at,omitempty"`
	FinishedAt *time.Time                `json:"finished_at,omitempty"`
	Request    BinlogAnalysisRequestView `json:"request"`
	Progress   binloganalyzer.Progress   `json:"progress"`
	Summary    *binloganalyzer.Summary   `json:"summary,omitempty"`
	Result     *binloganalyzer.Result    `json:"result,omitempty"`
}

type binlogAnalysisRecord struct {
	task   BinlogAnalysisTask
	cancel context.CancelFunc
}

type binlogAnalyzeFunc func(context.Context, binloganalyzer.Config, func(binloganalyzer.Progress)) (*binloganalyzer.Result, error)

// BinlogAnalysisService runs bounded, read-only analysis jobs from Manager.
// The browser selects a registered instance; credentials are resolved from the
// enabled MHA preset and are never accepted by or returned from the API.
type BinlogAnalysisService struct {
	instances MySQLInstanceRepository
	machines  machinedomain.Repository
	presets   MySQLAccountPresetRepository
	analyze   binlogAnalyzeFunc

	mu      sync.RWMutex
	records map[string]*binlogAnalysisRecord
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewBinlogAnalysisService(instances MySQLInstanceRepository, machines machinedomain.Repository, presets MySQLAccountPresetRepository) *BinlogAnalysisService {
	ctx, cancel := context.WithCancel(context.Background())
	return &BinlogAnalysisService{
		instances: instances, machines: machines, presets: presets,
		analyze: binloganalyzer.Analyze, records: make(map[string]*binlogAnalysisRecord),
		slots: make(chan struct{}, 2), ctx: ctx, cancel: cancel,
	}
}

func (s *BinlogAnalysisService) Create(ctx context.Context, req BinlogAnalysisRequest) (BinlogAnalysisTask, error) {
	instance, machine, err := s.target(ctx, req.MachineID, req.Port)
	if err != nil {
		return BinlogAnalysisTask{}, err
	}
	credential, err := s.credential(ctx)
	if err != nil {
		return BinlogAnalysisTask{}, err
	}
	cfg := binloganalyzer.Config{
		Host: machine.IP, Port: req.Port, User: credential.Username, Password: credential.Password,
		StartFile: strings.TrimSpace(req.StartFile), StartTime: req.StartTime, EndTime: req.EndTime,
		BigTxnMode: req.BigTxnMode, BigTxnRowsThreshold: req.BigTxnRowsThreshold,
		BigTxnBytesThreshold: req.BigTxnBytesThreshold,
	}
	if err := binloganalyzer.ValidateConfig(cfg); err != nil {
		return BinlogAnalysisTask{}, err
	}
	if strings.TrimSpace(instance.Version) == "" {
		return BinlogAnalysisTask{}, errors.New("实例版本尚未上报，暂时无法确认 Binlog 分析兼容性")
	}

	now := time.Now().UTC()
	task := BinlogAnalysisTask{
		ID: newBinlogAnalysisID(), Status: BinlogAnalysisQueued, CreatedAt: now,
		Request: BinlogAnalysisRequestView{
			MachineID: machine.ID, MachineName: machine.Name, MachineIP: machine.IP, Port: req.Port,
			StartTime: req.StartTime, EndTime: req.EndTime, StartFile: cfg.StartFile,
			BigTxnMode: normalizeBinlogMode(req.BigTxnMode), BigTxnRowsThreshold: req.BigTxnRowsThreshold,
			BigTxnBytesThreshold: req.BigTxnBytesThreshold,
		},
		Progress: binloganalyzer.Progress{Phase: BinlogAnalysisQueued, Message: "任务已进入分析队列"},
	}
	taskCtx, cancel := context.WithCancel(s.ctx)
	record := &binlogAnalysisRecord{task: task, cancel: cancel}
	s.mu.Lock()
	s.records[task.ID] = record
	s.trimLocked()
	s.mu.Unlock()
	go s.run(taskCtx, task.ID, cfg, credential.Password)
	return task, nil
}

func (s *BinlogAnalysisService) List() []BinlogAnalysisTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]BinlogAnalysisTask, 0, len(s.records))
	for _, record := range s.records {
		item := record.task
		item.Result = nil
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (s *BinlogAnalysisService) Get(id string) (BinlogAnalysisTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[strings.TrimSpace(id)]
	if !ok {
		return BinlogAnalysisTask{}, false
	}
	return record.task, true
}

func (s *BinlogAnalysisService) Cancel(id string) (BinlogAnalysisTask, error) {
	s.mu.Lock()
	record, ok := s.records[strings.TrimSpace(id)]
	if !ok {
		s.mu.Unlock()
		return BinlogAnalysisTask{}, errors.New("Binlog 分析任务不存在")
	}
	if record.task.Status != BinlogAnalysisQueued && record.task.Status != BinlogAnalysisRunning {
		task := record.task
		s.mu.Unlock()
		return task, fmt.Errorf("状态为 %s 的任务不能取消", task.Status)
	}
	record.cancel()
	now := time.Now().UTC()
	record.task.Status = BinlogAnalysisCanceled
	record.task.FinishedAt = &now
	record.task.Progress.Phase = BinlogAnalysisCanceled
	record.task.Progress.Message = "分析已取消"
	task := record.task
	s.mu.Unlock()
	return task, nil
}

func (s *BinlogAnalysisService) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *BinlogAnalysisService) run(ctx context.Context, id string, cfg binloganalyzer.Config, secret string) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		s.finishCanceled(id)
		return
	}
	startedAt := time.Now().UTC()
	s.mu.Lock()
	record := s.records[id]
	if record == nil || record.task.Status == BinlogAnalysisCanceled {
		s.mu.Unlock()
		return
	}
	record.task.Status = BinlogAnalysisRunning
	record.task.StartedAt = &startedAt
	record.task.Progress = binloganalyzer.Progress{Phase: BinlogAnalysisRunning, Message: "正在建立只读复制连接"}
	s.mu.Unlock()

	result, err := s.analyze(ctx, cfg, func(progress binloganalyzer.Progress) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current := s.records[id]; current != nil && current.task.Status == BinlogAnalysisRunning {
			current.task.Progress = progress
		}
	})
	finishedAt := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	record = s.records[id]
	if record == nil || record.task.Status == BinlogAnalysisCanceled {
		return
	}
	record.task.FinishedAt = &finishedAt
	if err != nil {
		if errors.Is(err, context.Canceled) {
			record.task.Status = BinlogAnalysisCanceled
			record.task.Progress.Phase = BinlogAnalysisCanceled
			record.task.Progress.Message = "分析已取消"
			return
		}
		record.task.Status = BinlogAnalysisFailed
		record.task.Error = safeBinlogError(err, secret)
		record.task.Progress.Phase = BinlogAnalysisFailed
		record.task.Progress.Message = record.task.Error
		return
	}
	record.task.Status = BinlogAnalysisCompleted
	record.task.Progress.Phase = BinlogAnalysisCompleted
	record.task.Progress.Message = "分析完成"
	record.task.Result = result
	if result != nil {
		summary := result.Summary
		record.task.Summary = &summary
	}
}

func (s *BinlogAnalysisService) finishCanceled(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil || record.task.Status == BinlogAnalysisCanceled {
		return
	}
	now := time.Now().UTC()
	record.task.Status = BinlogAnalysisCanceled
	record.task.FinishedAt = &now
	record.task.Progress.Phase = BinlogAnalysisCanceled
	record.task.Progress.Message = "分析已取消"
}

func (s *BinlogAnalysisService) target(ctx context.Context, machineID string, port int) (mysqlapp.Instance, machinedomain.Machine, error) {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return mysqlapp.Instance{}, machinedomain.Machine{}, errors.New("machine_id is required")
	}
	if port < 1 || port > 65535 {
		return mysqlapp.Instance{}, machinedomain.Machine{}, errors.New("port must be between 1 and 65535")
	}
	instance, ok, err := s.instances.Get(ctx, machineID, port)
	if err != nil {
		return mysqlapp.Instance{}, machinedomain.Machine{}, err
	}
	if !ok {
		return mysqlapp.Instance{}, machinedomain.Machine{}, fmt.Errorf("MySQL 实例 %s:%d 未登记", machineID, port)
	}
	machine, ok, err := s.machines.GetByID(ctx, machineID)
	if err != nil {
		return mysqlapp.Instance{}, machinedomain.Machine{}, err
	}
	if !ok || strings.TrimSpace(machine.IP) == "" {
		return mysqlapp.Instance{}, machinedomain.Machine{}, fmt.Errorf("实例 %s:%d 的机器地址不可用", machineID, port)
	}
	return instance, machine, nil
}

func (s *BinlogAnalysisService) credential(ctx context.Context) (mysqlapp.DiagnosticCredential, error) {
	if s.presets == nil {
		return mysqlapp.DiagnosticCredential{}, errors.New("Binlog 分析需要已配置的 MHA 管理账号")
	}
	items, err := s.presets.List(ctx)
	if err != nil {
		return mysqlapp.DiagnosticCredential{}, err
	}
	for _, item := range normalizeMySQLAccountPresets(items) {
		if !item.Enabled || !strings.EqualFold(strings.TrimSpace(item.Role), mysqlapp.AccountRoleMHA) {
			continue
		}
		if strings.TrimSpace(item.Username) != "" && item.Password != "" {
			return mysqlapp.DiagnosticCredential{Username: strings.TrimSpace(item.Username), Password: item.Password}, nil
		}
	}
	return mysqlapp.DiagnosticCredential{}, errors.New("Binlog 分析需要已启用且凭据完整的 MHA 管理账号")
}

func (s *BinlogAnalysisService) trimLocked() {
	if len(s.records) <= 50 {
		return
	}
	type item struct {
		id string
		at time.Time
	}
	var completed []item
	for id, record := range s.records {
		switch record.task.Status {
		case BinlogAnalysisCompleted, BinlogAnalysisFailed, BinlogAnalysisCanceled:
			completed = append(completed, item{id: id, at: record.task.CreatedAt})
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].at.Before(completed[j].at) })
	for len(s.records) > 50 && len(completed) > 0 {
		delete(s.records, completed[0].id)
		completed = completed[1:]
	}
}

func newBinlogAnalysisID() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("binlog-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("binlog-%d-%s", time.Now().Unix(), hex.EncodeToString(suffix[:]))
}

func normalizeBinlogMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), binloganalyzer.BigTransactionBytes) {
		return binloganalyzer.BigTransactionBytes
	}
	return binloganalyzer.BigTransactionRows
}

func safeBinlogError(err error, secret string) string {
	text := strings.TrimSpace(err.Error())
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "******")
	}
	if text == "" {
		return "Binlog 分析失败"
	}
	return text
}
