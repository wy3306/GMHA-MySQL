package app

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	backupdomain "gmha/internal/domain/backup"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

//go:embed templates/xtrabackup_backup.sh
var xtrabackupBackupScript string

//go:embed templates/xtrabackup_restore.sh
var xtrabackupRestoreScript string

//go:embed templates/bin2sql_flashback.sh
var bin2sqlFlashbackScript string

type RestoreOptions struct {
	Confirmation      string
	Mode              string
	BackupPath        string
	RestoreTime       time.Time
	MySQLUser         string
	MySQLPassword     string
	RepairReplication bool
	ApplyFlashback    bool
	Database          string
	Tables            []string
	OutputDir         string
}

type BackupService struct {
	repo     backupdomain.Repository
	tasks    *TaskService
	machines machinedomain.Repository
	mysql    interface {
		Get(context.Context, string, int) (mysqlapp.Instance, bool, error)
		List(context.Context) ([]mysqlapp.Instance, error)
	}
	cancel context.CancelFunc
	mu     sync.Mutex
}

// ClusterBackupItem records the independently submitted backup for a policy in
// a multi-cluster run. A policy remains the source of truth for credentials,
// retention and schedule settings; this operation only triggers it now.
type ClusterBackupItem struct {
	Cluster  string `json:"cluster"`
	PolicyID string `json:"policy_id"`
	Policy   string `json:"policy"`
	RunID    string `json:"run_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ClusterBackupResult struct {
	Created int                 `json:"created"`
	Failed  int                 `json:"failed"`
	Items   []ClusterBackupItem `json:"items"`
}

func NewBackupService(repo backupdomain.Repository, tasks *TaskService, machines machinedomain.Repository, mysql interface {
	Get(context.Context, string, int) (mysqlapp.Instance, bool, error)
	List(context.Context) ([]mysqlapp.Instance, error)
}) *BackupService {
	return &BackupService{repo: repo, tasks: tasks, machines: machines, mysql: mysql}
}

func (s *BackupService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.scheduleLoop(ctx)
}

func (s *BackupService) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
}

func (s *BackupService) SavePolicy(ctx context.Context, p backupdomain.Policy) (backupdomain.Policy, error) {
	if p.ID == "" {
		p.ID = newBackupID("backup-policy")
	}
	if existing, ok, err := s.repo.GetPolicy(ctx, p.ID); err != nil {
		return p, err
	} else if ok {
		if p.MySQLPassword == "" {
			p.MySQLPassword = existing.MySQLPassword
		}
		p.CreatedAt = existing.CreatedAt
		p.LastRunAt = existing.LastRunAt
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Cluster) == "" {
		return p, errors.New("策略名称和集群不能为空")
	}
	if p.Port <= 0 {
		p.Port = 3306
	}
	if p.RetryCount < 0 || p.RetryCount > 5 {
		return p, errors.New("失败重试次数必须在 0 到 5 之间；五次后当日备份判定失败")
	}
	if p.RetryIntervalSeconds <= 0 {
		p.RetryIntervalSeconds = 60
	}
	if p.BackupLocation == "" {
		p.BackupLocation = "/data/gmha/backups"
	}
	if !filepath.IsAbs(p.BackupLocation) {
		return p, errors.New("备份位置必须是目标机器上的绝对路径")
	}
	if p.MySQLUser == "" {
		p.MySQLUser = "backup"
	}
	if p.BackupType == "" {
		p.BackupType = backupdomain.TypeFull
	}
	if p.BackupType != backupdomain.TypeFull && p.BackupType != backupdomain.TypeIncremental {
		return p, errors.New("备份类型只能是全量或增量")
	}
	if p.DiskUsageThreshold == 0 {
		p.DiskUsageThreshold = 95
	}
	if p.DiskUsageThreshold < 1 || p.DiskUsageThreshold > 99 {
		return p, errors.New("磁盘使用率阈值必须在 1% 到 99% 之间")
	}
	if p.MachineID == "" {
		instance, err := s.defaultClusterInstance(ctx, p.Cluster)
		if err != nil {
			return p, err
		}
		p.MachineID, p.Port = instance.MachineID, instance.Port
	}
	m, ok, err := s.machines.GetByID(ctx, p.MachineID)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, errors.New("备份机器不存在")
	}
	if m.Cluster != p.Cluster {
		return p, errors.New("备份机器不属于所选集群")
	}
	_, ok, err = s.mysql.Get(ctx, p.MachineID, p.Port)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, errors.New("目标机器上没有登记对应端口的 MySQL 实例")
	}
	if p.StartAt.IsZero() {
		return p, errors.New("备份发起时间不能为空")
	}
	p.Weekdays = normalizeWeekdays(p.Weekdays)
	if p.WeekdayBackupTypes == nil {
		p.WeekdayBackupTypes = map[string]string{}
	}
	for _, day := range p.Weekdays {
		key := fmt.Sprint(day)
		kind := p.WeekdayBackupTypes[key]
		if kind == "" {
			p.WeekdayBackupTypes[key] = p.BackupType
		} else if kind != backupdomain.TypeFull && kind != backupdomain.TypeIncremental {
			return p, errors.New("每周备份类型只能是全量或增量")
		}
	}
	switch p.ScheduleType {
	case backupdomain.ScheduleWeekly:
		if len(p.Weekdays) == 0 {
			return p, errors.New("按周备份至少选择一个星期")
		}
	case backupdomain.ScheduleCustom:
		if p.IntervalMinutes < 1 {
			return p, errors.New("自定义备份的间隔不能小于 1 分钟")
		}
	case backupdomain.ScheduleOnce:
	default:
		return p, errors.New("不支持的备份时间类型")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	p.NextRunAt = nextBackupTime(p, now)
	if p.Enabled && p.NextRunAt.IsZero() {
		return p, errors.New("无法根据当前配置计算下一次备份时间")
	}
	if err = s.repo.SavePolicy(ctx, p); err != nil {
		return p, err
	}
	p.MySQLPassword = ""
	return p, nil
}

func (s *BackupService) ListPolicies(ctx context.Context, cluster string) ([]backupdomain.Policy, error) {
	return s.repo.ListPolicies(ctx, cluster)
}
func (s *BackupService) DeletePolicy(ctx context.Context, id string) error {
	return s.repo.DeletePolicy(ctx, id)
}

func (s *BackupService) RunPolicy(ctx context.Context, id string) (backupdomain.Run, error) {
	p, ok, err := s.repo.GetPolicy(ctx, id)
	if err != nil {
		return backupdomain.Run{}, err
	}
	if !ok {
		return backupdomain.Run{}, errors.New("备份策略不存在")
	}
	return s.run(ctx, p)
}

// RunClusters triggers all enabled backup policies belonging to the requested
// clusters. This avoids copying sensitive policy credentials into the web
// request while still allowing one operator action to back up multiple
// clusters.
func (s *BackupService) RunClusters(ctx context.Context, clusters []string) (ClusterBackupResult, error) {
	selected := make(map[string]bool, len(clusters))
	for _, cluster := range clusters {
		if cluster = strings.TrimSpace(cluster); cluster != "" {
			selected[cluster] = true
		}
	}
	if len(selected) == 0 {
		return ClusterBackupResult{}, errors.New("at least one cluster is required")
	}
	result := ClusterBackupResult{}
	for cluster := range selected {
		policies, err := s.repo.ListPolicies(ctx, cluster)
		if err != nil {
			return ClusterBackupResult{}, err
		}
		for _, policy := range policies {
			if !policy.Enabled {
				continue
			}
			item := ClusterBackupItem{Cluster: cluster, PolicyID: policy.ID, Policy: policy.Name}
			run, err := s.RunPolicy(ctx, policy.ID)
			if err != nil {
				item.Error = err.Error()
				result.Failed++
			} else {
				item.RunID, item.TaskID = run.ID, run.TaskID
				result.Created++
			}
			result.Items = append(result.Items, item)
		}
	}
	if len(result.Items) == 0 {
		return ClusterBackupResult{}, errors.New("selected clusters have no enabled backup policies")
	}
	return result, nil
}

func (s *BackupService) run(ctx context.Context, p backupdomain.Policy) (backupdomain.Run, error) {
	m, ok, err := s.machines.GetByID(ctx, p.MachineID)
	if err != nil {
		return backupdomain.Run{}, err
	}
	if !ok {
		return backupdomain.Run{}, errors.New("备份机器不存在")
	}
	inst, ok, err := s.mysql.Get(ctx, p.MachineID, p.Port)
	if err != nil {
		return backupdomain.Run{}, err
	}
	if !ok {
		return backupdomain.Run{}, errors.New("MySQL 实例不存在")
	}
	run := backupdomain.Run{ID: newBackupID("backup-run"), PolicyID: p.ID, Cluster: p.Cluster, MachineID: p.MachineID, MachineName: m.Name, MachineIP: m.IP, Port: p.Port, BackupType: p.BackupType, Status: backupdomain.RunPending, IncludeBinlog: p.IncludeBinlog, CreatedAt: time.Now().UTC()}
	basePath := ""
	if p.BackupType == backupdomain.TypeIncremental {
		base, err := s.latestIncrementalBase(ctx, p)
		if err != nil {
			return backupdomain.Run{}, err
		}
		run.BaseRunID, basePath = base.ID, base.BackupPath
	}
	run.BackupPath = filepath.Join(p.BackupLocation, safePathPart(p.Cluster), fmt.Sprintf("%s_%d", safePathPart(m.IP), p.Port), run.CreatedAt.Format("20060102_150405")+"_"+run.ID)
	command := renderRemoteScript(xtrabackupBackupScript, "gmha-xtrabackup-backup", []string{"--target-dir", run.BackupPath, "--backup-type", p.BackupType, "--incremental-basedir", basePath, "--disk-usage-threshold", fmt.Sprint(p.DiskUsageThreshold), "--replication-lag-wait", "30", "--port", fmt.Sprint(p.Port), "--socket", inst.SocketPath, "--user", p.MySQLUser, "--password-base64", base64.StdEncoding.EncodeToString([]byte(p.MySQLPassword)), "--retry-count", fmt.Sprint(p.RetryCount), "--retry-interval", fmt.Sprint(p.RetryIntervalSeconds), "--include-binlog", fmt.Sprint(p.IncludeBinlog), "--binlog-dir", inst.BinlogDir})
	backupName := "MySQL 全量备份"
	if p.BackupType == backupdomain.TypeIncremental {
		backupName = "MySQL 增量备份"
	}
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, p.MachineID, command, ExecTaskOptions{
		Operation: "mysql_backup_" + p.BackupType, DisplayName: backupName,
		StepName: "执行 XtraBackup " + backupName, Port: p.Port,
	})
	if err != nil {
		return backupdomain.Run{}, err
	}
	run.TaskID = detail.Task.ID
	if err = s.repo.SaveRun(ctx, run); err != nil {
		return backupdomain.Run{}, err
	}
	return run, nil
}

func (s *BackupService) ListRuns(ctx context.Context, cluster string, limit int) ([]backupdomain.Run, error) {
	runs, err := s.repo.ListRuns(ctx, cluster, limit)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if d, e := s.tasks.GetTaskDetail(ctx, runs[i].TaskID); e == nil {
			runs[i].Status = string(d.Task.Status)
			for _, event := range d.Events {
				message := strings.TrimSpace(event.Content)
				if message == "" {
					continue
				}
				runs[i].Logs = append(runs[i].Logs, backupdomain.Log{Time: event.CreatedAt, Level: string(event.EventType), Message: message})
				if event.EventType == "error" {
					runs[i].LastError = message
				}
			}
		}
		if m, ok, e := s.machines.GetByID(ctx, runs[i].MachineID); e == nil && ok {
			runs[i].MachineName = m.Name
			runs[i].MachineIP = m.IP
		}
	}
	return runs, nil
}

func (s *BackupService) Restore(ctx context.Context, runID string, opts RestoreOptions) (TaskDetail, error) {
	if opts.Mode == "" {
		opts.Mode = "physical"
	}
	expected := "RESTORE " + runID
	if opts.Mode == "flashback" {
		expected = "FLASHBACK " + runID
	}
	if opts.Confirmation != expected {
		return TaskDetail{}, errors.New("恢复确认内容不匹配")
	}
	run, ok, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !ok {
		return TaskDetail{}, errors.New("备份记录不存在")
	}
	if opts.Mode != "flashback" {
		if d, e := s.tasks.GetTaskDetail(ctx, run.TaskID); e != nil || d.Task.Status != "success" {
			return TaskDetail{}, errors.New("仅执行成功的备份可以恢复")
		}
	}
	inst, ok, err := s.mysql.Get(ctx, run.MachineID, run.Port)
	if err != nil {
		return TaskDetail{}, err
	}
	if !ok {
		return TaskDetail{}, errors.New("目标 MySQL 实例不存在")
	}
	if opts.MySQLUser == "" {
		opts.MySQLUser = "root"
	}
	if !opts.RestoreTime.IsZero() && opts.RestoreTime.After(time.Now().Add(time.Minute)) {
		return TaskDetail{}, errors.New("恢复时间不能晚于当前时间")
	}
	if opts.Mode == "flashback" {
		if opts.RestoreTime.IsZero() {
			return TaskDetail{}, errors.New("数据闪回必须选择恢复时间")
		}
		if opts.OutputDir == "" {
			opts.OutputDir = "/data/gmha/recovery"
		}
		if !filepath.IsAbs(opts.OutputDir) {
			return TaskDetail{}, errors.New("闪回文件位置必须是绝对路径")
		}
		args := []string{"--port", fmt.Sprint(run.Port), "--socket", inst.SocketPath, "--user", opts.MySQLUser, "--password-base64", base64.StdEncoding.EncodeToString([]byte(opts.MySQLPassword)), "--restore-time", opts.RestoreTime.In(time.Local).Format("2006-01-02 15:04:05"), "--output-dir", opts.OutputDir, "--database", opts.Database, "--tables", strings.Join(opts.Tables, ","), "--apply", fmt.Sprint(opts.ApplyFlashback)}
		command := renderRemoteScript(bin2sqlFlashbackScript, "gmha-bin2sql-flashback", args)
		displayName := "生成 MySQL 闪回 SQL"
		if opts.ApplyFlashback {
			displayName = "执行 MySQL 数据闪回"
		}
		detail, err := s.tasks.CreateExecTaskWithOptions(ctx, run.MachineID, command, ExecTaskOptions{
			Operation: "mysql_flashback", DisplayName: displayName, StepName: displayName, Port: run.Port,
		})
		if err == nil {
			_ = s.repo.SetRestoreTask(ctx, runID, detail.Task.ID)
		}
		return detail, err
	}
	if opts.Mode != "physical" && opts.Mode != "point_in_time" {
		return TaskDetail{}, errors.New("不支持的恢复模式")
	}
	chain, err := s.restoreChain(ctx, run)
	if err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(opts.BackupPath) != "" && opts.BackupPath != run.BackupPath {
		if !filepath.IsAbs(opts.BackupPath) {
			return TaskDetail{}, errors.New("恢复文件位置必须是绝对路径")
		}
		chain = []backupdomain.Run{{BackupPath: opts.BackupPath, BackupType: backupdomain.TypeFull}}
	}
	args := []string{"--full-dir", chain[0].BackupPath}
	for _, item := range chain[1:] {
		args = append(args, "--incremental-dir", item.BackupPath)
	}
	if opts.Mode == "point_in_time" && opts.RestoreTime.IsZero() {
		return TaskDetail{}, errors.New("按时间点恢复必须选择恢复时间")
	}
	binlogBase := run.BackupPath
	if strings.TrimSpace(opts.BackupPath) != "" {
		binlogBase = opts.BackupPath
	}
	binlogDir := filepath.Join(binlogBase, "gmha-binlog")
	args = append(args, "--recovery-mode", opts.Mode, "--restore-time", formatRestoreTime(opts.RestoreTime), "--binlog-dir", binlogDir, "--port", fmt.Sprint(run.Port), "--socket", inst.SocketPath, "--db-user", opts.MySQLUser, "--db-password-base64", base64.StdEncoding.EncodeToString([]byte(opts.MySQLPassword)), "--repair-replication", fmt.Sprint(opts.RepairReplication), "--data-dir", inst.DataDir, "--mysql-os-user", inst.MySQLUser, "--systemd-unit", inst.SystemdUnit)
	command := renderRemoteScript(xtrabackupRestoreScript, "gmha-xtrabackup-restore", args)
	displayName := "MySQL 全量物理恢复"
	if opts.Mode == "point_in_time" {
		displayName = "MySQL 按时间点恢复"
	}
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, run.MachineID, command, ExecTaskOptions{
		Operation: "mysql_restore_" + opts.Mode, DisplayName: displayName,
		StepName: "执行 " + displayName, Port: run.Port,
	})
	if err == nil {
		_ = s.repo.SetRestoreTask(ctx, runID, detail.Task.ID)
	}
	return detail, err
}

func formatRestoreTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.In(time.Local).Format("2006-01-02 15:04:05")
}

func (s *BackupService) defaultClusterInstance(ctx context.Context, cluster string) (mysqlapp.Instance, error) {
	instances, err := s.mysql.List(ctx)
	if err != nil {
		return mysqlapp.Instance{}, err
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return mysqlapp.Instance{}, err
	}
	allowed := map[string]bool{}
	for _, m := range machines {
		if m.Cluster == cluster {
			allowed[m.ID] = true
		}
	}
	for _, instance := range instances {
		if allowed[instance.MachineID] {
			return instance, nil
		}
	}
	return mysqlapp.Instance{}, errors.New("集群中没有可用于备份的 MySQL 实例")
}

func (s *BackupService) latestIncrementalBase(ctx context.Context, p backupdomain.Policy) (backupdomain.Run, error) {
	runs, err := s.repo.ListRuns(ctx, p.Cluster, 500)
	if err != nil {
		return backupdomain.Run{}, err
	}
	for _, run := range runs {
		if run.MachineID != p.MachineID || run.Port != p.Port {
			continue
		}
		detail, err := s.tasks.GetTaskDetail(ctx, run.TaskID)
		if err == nil && detail.Task.Status == "success" && (run.BackupType == backupdomain.TypeFull || run.BackupType == backupdomain.TypeIncremental) {
			return run, nil
		}
	}
	return backupdomain.Run{}, errors.New("增量备份前必须先完成一次同实例的全量备份")
}

func (s *BackupService) restoreChain(ctx context.Context, selected backupdomain.Run) ([]backupdomain.Run, error) {
	reversed := []backupdomain.Run{selected}
	current := selected
	for current.BackupType == backupdomain.TypeIncremental {
		if current.BaseRunID == "" {
			return nil, errors.New("增量备份缺少基础备份记录")
		}
		base, ok, err := s.repo.GetRun(ctx, current.BaseRunID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("增量备份链不完整")
		}
		if base.MachineID != selected.MachineID || base.Port != selected.Port {
			return nil, errors.New("增量备份链实例不一致")
		}
		reversed = append(reversed, base)
		current = base
		if len(reversed) > 100 {
			return nil, errors.New("增量备份链过长")
		}
	}
	if current.BackupType != backupdomain.TypeFull {
		return nil, errors.New("增量备份链没有全量备份起点")
	}
	chain := make([]backupdomain.Run, len(reversed))
	for i := range reversed {
		chain[len(reversed)-1-i] = reversed[i]
	}
	return chain, nil
}

func (s *BackupService) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx)
		}
	}
}
func (s *BackupService) runDue(ctx context.Context) {
	policies, err := s.repo.ListDuePolicies(ctx, time.Now().UTC())
	if err != nil {
		log.Printf("backup scheduler: %v", err)
		return
	}
	for _, p := range policies {
		now := time.Now().UTC()
		if p.ScheduleType == backupdomain.ScheduleWeekly {
			if kind := p.WeekdayBackupTypes[fmt.Sprint(int(now.In(time.Local).Weekday()))]; kind != "" {
				p.BackupType = kind
			}
		}
		next := nextBackupTime(p, now.Add(time.Second))
		enabled := p.ScheduleType != backupdomain.ScheduleOnce
		if err = s.repo.UpdatePolicySchedule(ctx, p.ID, now, next, enabled); err != nil {
			log.Printf("backup schedule update %s: %v", p.ID, err)
			continue
		}
		if _, err = s.run(ctx, p); err != nil {
			log.Printf("backup policy %s: %v", p.ID, err)
		}
	}
}

func nextBackupTime(p backupdomain.Policy, after time.Time) time.Time {
	start := p.StartAt.In(time.Local)
	localAfter := after.In(time.Local)
	switch p.ScheduleType {
	case backupdomain.ScheduleOnce:
		if start.After(localAfter) {
			return start.UTC()
		}
	case backupdomain.ScheduleCustom:
		if p.IntervalMinutes < 1 {
			return time.Time{}
		}
		if start.After(localAfter) {
			return start.UTC()
		}
		d := time.Duration(p.IntervalMinutes) * time.Minute
		steps := localAfter.Sub(start)/d + 1
		return start.Add(steps * d).UTC()
	case backupdomain.ScheduleWeekly:
		searchAfter := localAfter
		if start.After(searchAfter) {
			searchAfter = start.Add(-time.Nanosecond)
		}
		allowed := map[int]bool{}
		for _, d := range p.Weekdays {
			allowed[d] = true
		}
		for offset := 0; offset <= 7; offset++ {
			day := searchAfter.AddDate(0, 0, offset)
			candidate := time.Date(day.Year(), day.Month(), day.Day(), start.Hour(), start.Minute(), start.Second(), 0, time.Local)
			if allowed[int(candidate.Weekday())] && candidate.After(searchAfter) {
				return candidate.UTC()
			}
		}
	}
	return time.Time{}
}
func renderRemoteScript(script, prefix string, args []string) string {
	id := newBackupID(prefix)
	path := "/tmp/" + id + ".sh"
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	var b strings.Builder
	fmt.Fprintf(&b, "umask 077; printf %%s %s | base64 -d > %s; chmod 700 %s; ", backupShellQuote(encoded), backupShellQuote(path), backupShellQuote(path))
	b.WriteString(backupShellQuote(path))
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(backupShellQuote(arg))
	}
	fmt.Fprintf(&b, "; rc=$?; rm -f %s; exit $rc", backupShellQuote(path))
	return b.String()
}
func backupShellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
func safePathPart(v string) string {
	v = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			return r
		}
		return '_'
	}, v)
	return strings.Trim(v, "_")
}
func newBackupID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%x", prefix, time.Now().UnixMilli(), b[:])
}
func normalizeWeekdays(days []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, d := range days {
		if d >= 0 && d <= 6 && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Ints(out)
	return out
}
