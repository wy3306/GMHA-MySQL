package app

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmha/internal/buildinfo"
)

type UpgradeStep struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type UpgradeJob struct {
	ID             string        `json:"id"`
	Component      string        `json:"component"`
	Targets        []string      `json:"targets"`
	PackageName    string        `json:"package_name"`
	CurrentVersion string        `json:"current_version"`
	TargetVersion  string        `json:"target_version"`
	Status         string        `json:"status"`
	Progress       int           `json:"progress"`
	Steps          []UpgradeStep `json:"steps"`
	Error          string        `json:"error,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type UpgradeOverview struct {
	ManagerVersion  string                `json:"manager_version"`
	AgentTotal      int                   `json:"agent_total"`
	AgentVersions   []UpgradeVersionCount `json:"agent_versions"`
	ManagerPackages []UpgradePackageView  `json:"manager_packages"`
	AgentPackages   []UpgradePackageView  `json:"agent_packages"`
	Storage         UpgradeStorageInfo    `json:"storage"`
}

type UpgradeVersionCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

type UpgradePackageView struct {
	PackageItem
	Relation         string `json:"relation"`
	UpgradeableCount int    `json:"upgradeable_count"`
	CurrentCount     int    `json:"current_count"`
	DowngradeCount   int    `json:"downgrade_count"`
	UnknownCount     int    `json:"unknown_count"`
}

type UpgradeStorageInfo struct {
	Root                 string `json:"root"`
	ManagerPackageDir    string `json:"manager_package_dir"`
	AgentPackageDir      string `json:"agent_package_dir"`
	PackageIndex         string `json:"package_index"`
	JobState             string `json:"job_state"`
	ManagerExecutable    string `json:"manager_executable"`
	ManagerBackupPattern string `json:"manager_backup_pattern"`
	AgentInstallPattern  string `json:"agent_install_pattern"`
	AgentBackupPattern   string `json:"agent_backup_pattern"`
}

type UpgradeService struct {
	mu        sync.RWMutex
	jobs      map[string]UpgradeJob
	statePath string
	packages  *PackageService
	agents    *AgentService
	runtime   *ManagerRuntimeService
}

func NewUpgradeService(statePath string, packages *PackageService, agents *AgentService, managerRuntime *ManagerRuntimeService) *UpgradeService {
	s := &UpgradeService{statePath: statePath, packages: packages, agents: agents, runtime: managerRuntime, jobs: make(map[string]UpgradeJob)}
	_ = s.load()
	s.reconcileManagerRestart()
	return s
}

func (s *UpgradeService) Overview(ctx context.Context) (UpgradeOverview, error) {
	agents, err := s.agents.ListViews(ctx)
	if err != nil {
		return UpgradeOverview{}, err
	}
	managerPackages, err := s.packages.List("gmha-manager", "")
	if err != nil {
		return UpgradeOverview{}, err
	}
	agentPackages, err := s.packages.List("gmha-agent", "")
	if err != nil {
		return UpgradeOverview{}, err
	}
	managerVersion := componentVersion(buildinfo.CurrentVersion())
	versionCounts := make(map[string]int)
	for _, agent := range agents {
		version := componentVersion(agent.Version)
		if version == "" {
			version = "未知"
		}
		versionCounts[version]++
	}
	versions := make([]UpgradeVersionCount, 0, len(versionCounts))
	for version, count := range versionCounts {
		versions = append(versions, UpgradeVersionCount{Version: version, Count: count})
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Version == "未知" {
			return false
		}
		if versions[j].Version == "未知" {
			return true
		}
		comparison, ok := compareComponentVersions(versions[i].Version, versions[j].Version)
		return ok && comparison > 0
	})
	managerViews := make([]UpgradePackageView, 0, len(managerPackages))
	for _, item := range managerPackages {
		item.Version = componentVersion(item.Version)
		relation := versionRelation(managerVersion, item.Version)
		managerViews = append(managerViews, UpgradePackageView{PackageItem: item, Relation: relation})
	}
	agentViews := make([]UpgradePackageView, 0, len(agentPackages))
	for _, item := range agentPackages {
		item.Version = componentVersion(item.Version)
		view := UpgradePackageView{PackageItem: item, Relation: "mixed"}
		for version, count := range versionCounts {
			comparison, ok := compareComponentVersions(version, item.Version)
			if !ok {
				view.UnknownCount += count
			} else if comparison < 0 {
				view.UpgradeableCount += count
			} else if comparison == 0 {
				view.CurrentCount += count
			} else {
				view.DowngradeCount += count
			}
		}
		agentViews = append(agentViews, view)
	}
	settings := s.packages.Settings()
	executable, _ := os.Executable()
	storage := UpgradeStorageInfo{
		Root: settings.StoragePath, ManagerPackageDir: filepath.Join(settings.StoragePath, "gmha-manager"), AgentPackageDir: filepath.Join(settings.StoragePath, "gmha-agent"),
		PackageIndex: filepath.Join(settings.StoragePath, packageIndexName), JobState: s.statePath, ManagerExecutable: executable,
		ManagerBackupPattern: executable + ".backup-<version>", AgentInstallPattern: "<Agent InstallDir>/agentd", AgentBackupPattern: "<Agent InstallDir>/agentd.backup-<version>",
	}
	return UpgradeOverview{ManagerVersion: managerVersion, AgentTotal: len(agents), AgentVersions: versions, ManagerPackages: managerViews, AgentPackages: agentViews, Storage: storage}, nil
}

func (s *UpgradeService) List() []UpgradeJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]UpgradeJob, 0, len(s.jobs))
	for _, item := range s.jobs {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (s *UpgradeService) Get(id string) (UpgradeJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.jobs[id]
	return item, ok
}

func (s *UpgradeService) StartAgentUpgrade(packageName string, targets []string) (UpgradeJob, error) {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" || len(targets) == 0 {
		return UpgradeJob{}, errors.New("安装包和至少一个目标 Agent 必填")
	}
	item, path, err := s.resolvePackage("gmha-agent", packageName)
	if err != nil {
		return UpgradeJob{}, err
	}
	for _, ip := range targets {
		view, ok, viewErr := s.agents.GetViewByIP(context.Background(), ip)
		if viewErr != nil {
			return UpgradeJob{}, viewErr
		}
		if !ok {
			return UpgradeJob{}, fmt.Errorf("Agent %s 不存在", ip)
		}
		comparison, comparable := compareComponentVersions(view.Version, item.Version)
		if !comparable {
			return UpgradeJob{}, fmt.Errorf("Agent %s 当前版本 %q 无法与目标版本 %s 比较", ip, view.Version, item.Version)
		}
		if comparison == 0 {
			return UpgradeJob{}, fmt.Errorf("Agent %s 已经是目标版本 %s", ip, item.Version)
		}
		if comparison > 0 {
			return UpgradeJob{}, fmt.Errorf("禁止将 Agent %s 从 %s 降级到 %s", ip, componentVersion(view.Version), item.Version)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return UpgradeJob{}, err
	}
	if len(content) < 4 {
		return UpgradeJob{}, errors.New("Agent 安装包为空或不是有效二进制")
	}
	if err := validateAgentBinary(content, item.Arch); err != nil {
		return UpgradeJob{}, err
	}
	job := s.newJob("agent", targets, item, "mixed", []string{"升级前检查", "校验安装包", "备份并替换 Agent", "重启 Agent", "心跳与版本后检"})
	go s.runAgent(job.ID, item.Version, item.Arch, content)
	return job, nil
}

func (s *UpgradeService) StartManagerUpgrade(packageName string) (UpgradeJob, error) {
	item, path, err := s.resolvePackage("gmha-manager", strings.TrimSpace(packageName))
	if err != nil {
		return UpgradeJob{}, err
	}
	comparison, comparable := compareComponentVersions(buildinfo.CurrentVersion(), item.Version)
	if !comparable {
		return UpgradeJob{}, fmt.Errorf("当前 Manager 版本 %q 无法与目标版本 %s 比较", buildinfo.CurrentVersion(), item.Version)
	}
	if comparison == 0 {
		return UpgradeJob{}, errors.New("目标版本与当前 Manager 版本相同")
	}
	if comparison > 0 {
		return UpgradeJob{}, fmt.Errorf("禁止将 Manager 从 %s 降级到 %s", componentVersion(buildinfo.CurrentVersion()), item.Version)
	}
	job := s.newJob("manager", []string{"local"}, item, buildinfo.CurrentVersion(), []string{"运行环境预检", "安装包与版本校验", "备份当前 Manager", "原子替换程序", "重启与健康后检"})
	go s.runManager(job.ID, path, item.Arch)
	return job, nil
}

// StartManagerRebuild recompiles the Manager kernel from a local source tree,
// validates the candidate, atomically installs it and then uses the same
// restart/health-check path as a package upgrade.
func (s *UpgradeService) StartManagerRebuild(sourceDir string) (UpgradeJob, error) {
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		sourceDir = "."
	}
	absolute, err := filepath.Abs(sourceDir)
	if err != nil {
		return UpgradeJob{}, err
	}
	if info, err := os.Stat(filepath.Join(absolute, "go.mod")); err != nil || info.IsDir() {
		return UpgradeJob{}, errors.New("源码目录中找不到 go.mod")
	}
	s.mu.RLock()
	for _, existing := range s.jobs {
		if (existing.Component == "manager" || existing.Component == "manager-build") && (existing.Status == "pending" || existing.Status == "running") {
			s.mu.RUnlock()
			return UpgradeJob{}, errors.New("已有 Manager 升级或重编译任务正在执行")
		}
	}
	s.mu.RUnlock()
	pkg := PackageItem{Name: "本地源码重编译", Version: buildinfo.CurrentVersion(), Arch: runtime.GOARCH}
	job := s.newJob("manager-build", []string{"local"}, pkg, buildinfo.CurrentVersion(), []string{"源码与工具链预检", "编译 Manager 内核", "候选程序自检", "备份并原子安装", "重启与健康后检"})
	go s.runManagerRebuild(job.ID, absolute)
	return job, nil
}

func (s *UpgradeService) runManagerRebuild(id, sourceDir string) {
	s.step(id, 0, "running", "检查 Go 工具链、源码模块和当前程序写权限")
	goPath, err := exec.LookPath("go")
	if err != nil {
		s.fail(id, 0, errors.New("未找到 Go 工具链，无法重编译 Manager"))
		return
	}
	exePath, err := os.Executable()
	if err != nil {
		s.fail(id, 0, err)
		return
	}
	info, err := os.Stat(exePath)
	if err != nil {
		s.fail(id, 0, err)
		return
	}
	s.step(id, 0, "success", fmt.Sprintf("源码 %s；工具链 %s", sourceDir, goPath))

	candidate := exePath + ".rebuild.candidate"
	_ = os.Remove(candidate)
	s.step(id, 1, "running", "执行 go build -trimpath ./cmd/gmha")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goPath, "build", "-trimpath", "-o", candidate, "./cmd/gmha")
	command.Dir = sourceDir
	output, err := command.CombinedOutput()
	if err != nil {
		s.fail(id, 1, fmt.Errorf("Manager 编译失败: %s", strings.TrimSpace(string(output))))
		return
	}
	defer os.Remove(candidate)
	if err := os.Chmod(candidate, info.Mode().Perm()|0o111); err != nil {
		s.fail(id, 1, err)
		return
	}
	s.step(id, 1, "success", "Manager 内核编译完成")

	s.step(id, 2, "running", "执行候选程序 --version 并检查可执行性")
	versionOutput, err := exec.Command(candidate, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(versionOutput)) == "" {
		s.fail(id, 2, fmt.Errorf("候选程序自检失败: %s", strings.TrimSpace(string(versionOutput))))
		return
	}
	s.step(id, 2, "success", "候选程序自检通过，版本 "+strings.TrimSpace(string(versionOutput)))

	backup := exePath + ".backup-rebuild-" + time.Now().UTC().Format("20060102T150405")
	s.step(id, 3, "running", "备份当前程序并原子安装重编译内核")
	if err := copyExecutable(exePath, backup, info.Mode()); err != nil {
		s.fail(id, 3, err)
		return
	}
	if err := os.Rename(candidate, exePath); err != nil {
		s.fail(id, 3, err)
		return
	}
	s.step(id, 3, "success", "新内核已安装；回退文件 "+backup)

	s.step(id, 4, "running", "重启 Manager 并等待健康检查")
	if _, err := s.runtime.RestartCurrentProcess(jobRuntimeConfig(s.runtime)); err != nil {
		_ = copyExecutable(backup, exePath, info.Mode())
		s.fail(id, 4, fmt.Errorf("重启失败，已恢复旧程序: %w", err))
	}
}

func (s *UpgradeService) resolvePackage(category, name string) (PackageItem, string, error) {
	if name == "" {
		return PackageItem{}, "", errors.New("package_name is required")
	}
	items, err := s.packages.List(category, "")
	if err != nil {
		return PackageItem{}, "", err
	}
	for _, item := range items {
		if item.Name == name {
			verified, verifyErr := s.packages.Verify(category, name)
			if verifyErr != nil {
				return PackageItem{}, "", fmt.Errorf("安装包 SHA-256 校验失败: %w", verifyErr)
			}
			item = verified
			if strings.TrimSpace(item.Version) == "" {
				return PackageItem{}, "", errors.New("安装包缺少版本元数据，请按 gmha-Vx.y.z-<os>-<arch> 命名或上传时填写版本")
			}
			if category == "gmha-manager" || category == "gmha-agent" {
				item.Version = componentVersion(item.Version)
			}
			path, err := s.packages.Open(category, name)
			return item, path, err
		}
	}
	return PackageItem{}, "", os.ErrNotExist
}

func validateAgentBinary(content []byte, packageArch string) error {
	file, err := elf.NewFile(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("Agent 安装包不是有效的 Linux ELF 二进制: %w", err)
	}
	actual := ""
	switch file.Machine {
	case elf.EM_X86_64:
		actual = "x86_64"
	case elf.EM_AARCH64:
		actual = "aarch64"
	}
	if actual == "" {
		return fmt.Errorf("不支持的 Agent 二进制架构 %s", file.Machine)
	}
	if packageArch != "未识别" && normalizeComponentArch(packageArch) != actual {
		return fmt.Errorf("Agent 文件实际架构 %s 与安装包元数据 %s 不一致", actual, packageArch)
	}
	return nil
}

func (s *UpgradeService) newJob(component string, targets []string, pkg PackageItem, current string, names []string) UpgradeJob {
	now := time.Now().UTC()
	steps := make([]UpgradeStep, len(names))
	for i, name := range names {
		steps[i] = UpgradeStep{Name: name, Status: "pending"}
	}
	job := UpgradeJob{ID: fmt.Sprintf("upgrade-%d", now.UnixNano()), Component: component, Targets: append([]string(nil), targets...), PackageName: pkg.Name, CurrentVersion: current, TargetVersion: pkg.Version, Status: "pending", Steps: steps, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	s.jobs[job.ID] = job
	_ = s.saveLocked()
	s.mu.Unlock()
	return job
}

func (s *UpgradeService) runAgent(id, version, packageArch string, binary []byte) {
	s.step(id, 0, "running", "检查目标在线状态、SSH 权限与当前版本")
	job, _ := s.Get(id)
	for _, ip := range job.Targets {
		view, ok, err := s.agents.GetViewByIP(context.Background(), ip)
		if err != nil || !ok || (strings.ToLower(view.HeartbeatState) != "online" && strings.ToLower(view.InstallState) != "online") {
			s.fail(id, 0, fmt.Errorf("%s 升级前检查失败：Agent 不在线", ip))
			return
		}
		machine, found, err := s.agents.resolveMachineByIP(context.Background(), ip)
		if err != nil || !found {
			s.fail(id, 0, fmt.Errorf("%s 升级前检查失败：机器不存在", ip))
			return
		}
		_, targetArch, err := s.agents.detectRemotePlatform(context.Background(), machine)
		if err != nil || (packageArch != "未识别" && normalizeComponentArch(targetArch) != normalizeComponentArch(packageArch)) {
			s.fail(id, 0, fmt.Errorf("%s 架构不兼容：目标 %s，安装包 %s", ip, targetArch, packageArch))
			return
		}
	}
	s.step(id, 0, "success", "目标 Agent 均在线，当前版本已记录")
	s.step(id, 1, "success", fmt.Sprintf("安装包已读取，目标版本 %s", version))
	s.step(id, 2, "running", "逐台备份并原子替换 Agent 二进制")
	for _, ip := range job.Targets {
		if _, err := s.agents.UpgradeByIPBinary(context.Background(), ip, version, binary); err != nil {
			s.fail(id, 2, fmt.Errorf("%s: %w", ip, err))
			return
		}
	}
	s.step(id, 2, "success", fmt.Sprintf("%d 台 Agent 已完成替换", len(job.Targets)))
	s.step(id, 3, "success", "systemd 服务已重启")
	s.step(id, 4, "running", "核对新鲜心跳与上报版本")
	for _, ip := range job.Targets {
		view, ok, err := s.agents.GetViewByIP(context.Background(), ip)
		if err != nil || !ok || !strings.EqualFold(view.Version, version) {
			s.fail(id, 4, fmt.Errorf("%s 版本后检失败：上报 %s，期望 %s", ip, view.Version, version))
			return
		}
	}
	s.step(id, 4, "success", "所有 Agent 心跳在线且版本匹配")
	s.complete(id)
}

func (s *UpgradeService) runManager(id, packagePath, packageArch string) {
	s.step(id, 0, "running", fmt.Sprintf("检查操作系统 %s、架构 %s 与写权限", runtime.GOOS, runtime.GOARCH))
	if packageArch != "未识别" && normalizeComponentArch(packageArch) != normalizeComponentArch(runtime.GOARCH) {
		s.fail(id, 0, fmt.Errorf("Manager 架构不兼容：本机 %s，安装包 %s", runtime.GOARCH, packageArch))
		return
	}
	exePath, err := os.Executable()
	if err != nil {
		s.fail(id, 0, err)
		return
	}
	info, err := os.Stat(exePath)
	if err != nil {
		s.fail(id, 0, err)
		return
	}
	s.step(id, 0, "success", "当前程序路径与写入条件正常")
	s.step(id, 1, "running", "执行候选 Manager --version")
	candidate := exePath + ".upgrade.candidate"
	if err := copyExecutable(packagePath, candidate, info.Mode()); err != nil {
		s.fail(id, 1, err)
		return
	}
	defer os.Remove(candidate)
	cmd := exec.Command(candidate, "--version")
	output, err := cmd.CombinedOutput()
	job, _ := s.Get(id)
	if err != nil || !strings.EqualFold(strings.TrimSpace(string(output)), job.TargetVersion) {
		s.fail(id, 1, fmt.Errorf("候选程序版本校验失败：%s", strings.TrimSpace(string(output))))
		return
	}
	s.step(id, 1, "success", "候选程序可执行且版本匹配")
	backup := exePath + ".backup-" + strings.ReplaceAll(job.CurrentVersion, "/", "_")
	s.step(id, 2, "running", "备份到 "+backup)
	if err := copyExecutable(exePath, backup, info.Mode()); err != nil {
		s.fail(id, 2, err)
		return
	}
	s.step(id, 2, "success", "当前 Manager 已备份")
	s.step(id, 3, "running", "写入临时文件并原子替换")
	if err := os.Rename(candidate, exePath); err != nil {
		s.fail(id, 3, err)
		return
	}
	s.step(id, 3, "success", "Manager 程序已替换，可通过备份回退")
	s.step(id, 4, "running", "正在启动新版本并等待健康检查")
	if _, err := s.runtime.RestartCurrentProcess(jobRuntimeConfig(s.runtime)); err != nil {
		_ = copyExecutable(backup, exePath, info.Mode())
		s.fail(id, 4, fmt.Errorf("重启失败，已恢复旧程序：%w", err))
		return
	}
}

func componentVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.HasPrefix(version, "V") {
		return version
	}
	if strings.HasPrefix(version, "v") {
		return "V" + version[1:]
	}
	return "V" + version
}

func compareComponentVersions(current, target string) (int, bool) {
	left, leftOK := parseComponentVersion(current)
	right, rightOK := parseComponentVersion(target)
	if !leftOK || !rightOK {
		return 0, false
	}
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for i := 0; i < length; i++ {
		var leftPart, rightPart int
		if i < len(left) {
			leftPart = left[i]
		}
		if i < len(right) {
			rightPart = right[i]
		}
		if leftPart < rightPart {
			return -1, true
		}
		if leftPart > rightPart {
			return 1, true
		}
	}
	return 0, true
}

func parseComponentVersion(value string) ([]int, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "V"), "v"))
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if value == "" || len(parts) < 2 {
		return nil, false
	}
	parsed := make([]int, len(parts))
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		parsed[index] = number
	}
	return parsed, true
}

func versionRelation(current, target string) string {
	comparison, ok := compareComponentVersions(current, target)
	if !ok {
		return "unknown"
	}
	if comparison < 0 {
		return "upgrade"
	}
	if comparison > 0 {
		return "downgrade"
	}
	return "current"
}

func normalizeComponentArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

func jobRuntimeConfig(r *ManagerRuntimeService) ManagerRuntimeConfig {
	status, err := r.GetStatus(context.Background())
	if err != nil {
		return ManagerRuntimeConfig{}
	}
	return status.Config
}

func copyExecutable(source, target string, mode os.FileMode) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, content, mode.Perm()|0o111); err != nil {
		return err
	}
	return nil
}

func (s *UpgradeService) step(id string, index int, status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	now := time.Now().UTC()
	if index >= 0 && index < len(job.Steps) {
		step := job.Steps[index]
		step.Status, step.Message = status, message
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		if status == "success" || status == "failed" {
			step.FinishedAt = now
		}
		job.Steps[index] = step
		job.Progress = index * 100 / len(job.Steps)
		if status == "running" {
			job.Status = "running"
		}
	}
	job.UpdatedAt = now
	s.jobs[id] = job
	_ = s.saveLocked()
}

func (s *UpgradeService) fail(id string, index int, err error) {
	s.step(id, index, "failed", err.Error())
	s.mu.Lock()
	job := s.jobs[id]
	job.Status, job.Error, job.UpdatedAt = "failed", err.Error(), time.Now().UTC()
	s.jobs[id] = job
	_ = s.saveLocked()
	s.mu.Unlock()
}
func (s *UpgradeService) complete(id string) {
	s.mu.Lock()
	job := s.jobs[id]
	job.Status, job.Progress, job.UpdatedAt = "success", 100, time.Now().UTC()
	s.jobs[id] = job
	_ = s.saveLocked()
	s.mu.Unlock()
}

func (s *UpgradeService) reconcileManagerRestart() {
	s.mu.RLock()
	ids := make([]string, 0)
	for id, job := range s.jobs {
		if (job.Component == "manager" || job.Component == "manager-build") && job.Status == "running" && len(job.Steps) > 4 && job.Steps[4].Status == "running" && strings.EqualFold(job.TargetVersion, buildinfo.CurrentVersion()) {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	for _, id := range ids {
		go s.verifyManagerAfterRestart(id)
	}
}

func (s *UpgradeService) verifyManagerAfterRestart(id string) {
	status, err := s.runtime.GetStatus(context.Background())
	if err != nil {
		s.fail(id, 4, err)
		return
	}
	healthURL, err := managerHealthURL(status.Config.ManagerHTTPAddr)
	if err != nil {
		s.fail(id, 4, err)
		return
	}
	statusURL := strings.TrimSuffix(healthURL, "/healthz") + "/manager/status"
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		resp, requestErr := client.Get(statusURL)
		if requestErr == nil {
			content, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			var remote ManagerRuntimeStatus
			if resp.StatusCode == http.StatusOK && json.Unmarshal(content, &remote) == nil {
				job, _ := s.Get(id)
				if remote.Running && strings.EqualFold(remote.Version, job.TargetVersion) {
					s.step(id, 4, "success", "新版本 Manager 健康检查通过，运行版本 "+remote.Version)
					s.complete(id)
					return
				}
			}
		}
		time.Sleep(time.Second)
	}
	s.fail(id, 4, errors.New("新版本 Manager 未在 45 秒内通过健康与版本后检，请使用备份程序回退"))
}

func (s *UpgradeService) load() error {
	content, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var items []UpgradeJob
	if err := json.Unmarshal(content, &items); err != nil {
		return err
	}
	for _, item := range items {
		s.jobs[item.ID] = item
	}
	return nil
}
func (s *UpgradeService) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		return err
	}
	items := make([]UpgradeJob, 0, len(s.jobs))
	for _, item := range s.jobs {
		items = append(items, item)
	}
	content, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath)
}
