package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmha/internal/buildinfo"
)

var agentBuildMu sync.Mutex

// NextAgentVersion increments the highest published release, avoiding reuse of
// a version whose bytes may already be running on managed machines.
func NextAgentVersion(packages *PackageService) (string, error) {
	items, err := packages.List("gmha-agent", "")
	if err != nil {
		return "", err
	}
	versions := []string{buildinfo.CurrentVersion()}
	for _, item := range items {
		versions = append(versions, item.Version)
	}
	// Release archives produced by build-release also reserve their version.
	if paths, _ := filepath.Glob("dist/gmha-agent-*-linux-*.bin"); len(paths) > 0 {
		for _, path := range paths {
			versions = append(versions, detectPackageVersion(filepath.Base(path)))
		}
	}
	parts, ok := parseComponentVersion(highestComponentVersion(versions...))
	if !ok {
		return "", fmt.Errorf("cannot determine Agent release version")
	}
	for len(parts) < 3 {
		parts = append(parts, 0)
	}
	parts[len(parts)-1]++
	values := make([]string, len(parts))
	for i, p := range parts {
		values[i] = strconv.Itoa(p)
	}
	return "V" + strings.Join(values, "."), nil
}

// BuildAndPublishAgent builds both supported platforms before publishing either.
// Packages are written via the repository's atomic, checksummed upload path.
func BuildAndPublishAgent(ctx context.Context, sourceDir string, packages *PackageService) ([]PackageItem, error) {
	agentBuildMu.Lock()
	defer agentBuildMu.Unlock()
	lock := filepath.Join(packages.Settings().StoragePath, ".agent-build.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return nil, fmt.Errorf("Agent 构建锁不可用（已有构建或上次异常退出）: %w", err)
	}
	defer os.Remove(lock)
	version, err := NextAgentVersion(packages)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "gmha-agent-release-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	arches := []string{"amd64", "arm64"}
	for _, arch := range arches {
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w -X gmha/internal/buildinfo.Version="+version, "-o", filepath.Join(dir, arch), "./cmd/agent")
		cmd.Dir = sourceDir
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("build Agent %s: %w: %s", arch, err, output)
		}
	}
	result := make([]PackageItem, 0, 2)
	for _, arch := range arches {
		f, err := os.Open(filepath.Join(dir, arch))
		if err != nil {
			return result, err
		}
		item, err := packages.SaveUploadWithMetadata("gmha-agent", normalizeComponentArch(arch), "gmha-agent-"+version+"-linux-"+arch+".bin", version, "源码构建发布；包含 Agent 最新修复", f)
		f.Close()
		if err != nil {
			return result, err
		}
		result = append(result, item)
	}
	return result, nil
}

func agentSourceAvailable() bool {
	_, err := findRepoRoot()
	if err != nil {
		return false
	}
	_, err = exec.LookPath("go")
	return err == nil
}

func (s *UpgradeService) StartAgentBuild() (UpgradeJob, error) {
	root, err := findRepoRoot()
	if err != nil {
		return UpgradeJob{}, err
	}
	if !agentSourceAvailable() {
		return UpgradeJob{}, fmt.Errorf("当前部署没有 Agent 源码或 Go 工具链，请上传已构建的 Agent 制品")
	}
	s.mu.Lock()
	for _, job := range s.jobs {
		if job.Component == "agent-build" && (job.Status == "pending" || job.Status == "running") {
			s.mu.Unlock()
			return UpgradeJob{}, fmt.Errorf("Agent 正在构建，请等待当前任务完成")
		}
	}
	// Reserve while holding the job lock; building happens in the background.
	now := time.Now().UTC()
	job := UpgradeJob{ID: fmt.Sprintf("upgrade-%d", now.UnixNano()), Component: "agent-build", Status: "pending", CreatedAt: now, UpdatedAt: now, Steps: []UpgradeStep{{Name: "构建并发布双架构 Agent", Status: "pending"}}}
	s.jobs[job.ID] = job
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return UpgradeJob{}, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.step(job.ID, 0, "running", "分配新版本号，构建 Linux AMD64 / ARM64 并同步到制品库")
		items, err := BuildAndPublishAgent(ctx, root, s.packages)
		if err != nil {
			s.fail(job.ID, 0, err)
			return
		}
		s.mu.Lock()
		current := s.jobs[job.ID]
		current.TargetVersion = items[0].Version
		s.jobs[job.ID] = current
		_ = s.saveLocked()
		s.mu.Unlock()
		s.step(job.ID, 0, "success", "已发布 "+items[0].Version+"，可选择机器推送升级")
		s.complete(job.ID)
	}()
	return job, nil
}
