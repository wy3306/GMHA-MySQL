package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManagerSourceWatchStatus describes source-mode hot rebuild. Release bundles
// do not contain a source tree, so the watcher stays unavailable there.
type ManagerSourceWatchStatus struct {
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	SourceDir string `json:"source_dir,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// StartManagerSourceWatch watches relevant source files with a stable-content
// fingerprint. Two equal samples are required before a rebuild, which avoids
// compiling half-written editor saves. Build outputs and runtime data are
// excluded so a successful rebuild cannot trigger a loop.
func (s *UpgradeService) StartManagerSourceWatch(sourceDir string) {
	if strings.TrimSpace(sourceDir) == "" {
		root, err := findRepoRoot()
		if err != nil {
			s.setManagerWatchStatus(ManagerSourceWatchStatus{LastError: err.Error()})
			return
		}
		sourceDir = root
	}
	absolute, err := filepath.Abs(sourceDir)
	if err != nil {
		s.setManagerWatchStatus(ManagerSourceWatchStatus{LastError: err.Error()})
		return
	}
	initial, err := managerSourceFingerprint(absolute)
	if err != nil {
		s.setManagerWatchStatus(ManagerSourceWatchStatus{SourceDir: absolute, LastError: err.Error()})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
	}
	s.watchCancel = cancel
	s.watchStatus = ManagerSourceWatchStatus{Available: true, Enabled: true, SourceDir: absolute}
	s.mu.Unlock()
	go s.watchManagerSources(ctx, absolute, initial)
}

func (s *UpgradeService) watchManagerSources(ctx context.Context, sourceDir, baseline string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	pending := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fingerprint, err := managerSourceFingerprint(sourceDir)
			if err != nil {
				s.updateManagerWatchError(err)
				continue
			}
			if fingerprint == baseline {
				pending = ""
				continue
			}
			if fingerprint != pending {
				pending = fingerprint
				continue
			}
			if s.runtime == nil || !s.runtime.IsCurrentProcess() {
				s.updateManagerWatchError(errors.New("Manager 尚未登记为受控进程，暂不自动重启"))
				continue
			}
			if _, err := s.StartManagerRebuild(sourceDir); err != nil {
				s.updateManagerWatchError(err)
				continue
			}
			// Reserve this source state immediately. The build changes only excluded
			// output paths; the replacement process starts with this as its baseline.
			baseline = fingerprint
			pending = ""
			s.updateManagerWatchError(nil)
		}
	}
}

func (s *UpgradeService) ManagerSourceWatchStatus() ManagerSourceWatchStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watchStatus
}

func (s *UpgradeService) setManagerWatchStatus(status ManagerSourceWatchStatus) {
	s.mu.Lock()
	s.watchStatus = status
	s.mu.Unlock()
}

func (s *UpgradeService) updateManagerWatchError(err error) {
	s.mu.Lock()
	if err == nil {
		s.watchStatus.LastError = ""
	} else {
		s.watchStatus.LastError = err.Error()
	}
	s.mu.Unlock()
}

func (s *UpgradeService) Close() {
	s.mu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.mu.Unlock()
}

func managerSourceFingerprint(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if relative != "." && (name == ".git" || name == "dist" || name == "data" || name == "node_modules" || name == ".vite") {
				return filepath.SkipDir
			}
			return nil
		}
		if managerSourceFile(relative) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, _ := filepath.Rel(root, path)
		_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00")
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if len(paths) == 0 {
		return "", errors.New("源码目录中没有可监听的 Manager 文件")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func managerSourceFile(relative string) bool {
	relative = filepath.ToSlash(relative)
	base := filepath.Base(relative)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, ".test.mjs") {
		return false
	}
	if base == "go.mod" || base == "go.sum" || base == "package.json" || strings.HasPrefix(base, "package-lock.") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".go" {
		return strings.HasPrefix(relative, "cmd/gmha/") || strings.HasPrefix(relative, "internal/") || strings.HasPrefix(relative, "pkg/")
	}
	return strings.HasPrefix(relative, "internal/interface/http/frontend/src/") && (ext == ".js" || ext == ".css" || ext == ".vue" || ext == ".html")
}
