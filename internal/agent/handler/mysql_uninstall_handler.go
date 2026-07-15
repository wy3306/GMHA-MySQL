package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	agentcore "gmha/internal/agent/core"
	"gmha/internal/agent/mysqlcheck"
	taskdomain "gmha/internal/domain/task"
)

// MySQLUninstallHandler 是 MySQL 卸载任务处理器，负责在代理主机上安全卸载 MySQL 实例。
type MySQLUninstallHandler struct {
	installDir string
}

// NewMySQLUninstallHandler 创建一个新的 MySQL 卸载任务处理器实例。
func NewMySQLUninstallHandler(installDir string) *MySQLUninstallHandler {
	return &MySQLUninstallHandler{installDir: strings.TrimSpace(installDir)}
}

// Type 返回该处理器处理的任务类型。
func (h *MySQLUninstallHandler) Type() string {
	return string(taskdomain.TypeMySQLUninstall)
}

// Handle 执行 MySQL 卸载任务，包括停止服务、禁用自启、删除 systemd 文件、清理数据目录和安装目录等步骤。
func (h *MySQLUninstallHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	var spec taskdomain.MySQLUninstallSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return err
	}
	runner := &mysqlInstallRunner{
		ctx:        ctx,
		task:       task,
		reporter:   reporter,
		installDir: h.installDir,
		spec: taskdomain.MySQLInstallSpec{
			Port:            spec.Port,
			MySQLUser:       spec.MySQLUser,
			InstanceDir:     spec.InstanceDir,
			DataDir:         spec.DataDir,
			BinlogDir:       spec.BinlogDir,
			RedoDir:         spec.RedoDir,
			UndoDir:         spec.UndoDir,
			TmpDir:          spec.TmpDir,
			BaseDir:         spec.BaseDir,
			PackageName:     spec.PackageName,
			SystemdUnitName: spec.SystemdUnitName,
			MyCnfPath:       spec.MyCnfPath,
			SocketPath:      spec.SocketPath,
		},
	}
	return runner.runUninstall(spec)
}

func (r *mysqlInstallRunner) runUninstall(spec taskdomain.MySQLUninstallSpec) error {
	commands, err := BuildMySQLUninstallCommands(spec)
	if err != nil {
		return err
	}
	for i, command := range commands {
		if err := r.runShellStep(r.task.Steps[i], command.Title, command.Command); err != nil {
			return err
		}
	}
	if err := mysqlcheck.RemoveInstance(filepath.Join(r.installDir, mysqlcheck.DefaultConfigFile), spec.Port, spec.SocketPath); err != nil {
		return err
	}
	return nil
}

// MySQLUninstallCommand 是 Agent 任务与 Manager SSH 回退通道共用的卸载步骤。
type MySQLUninstallCommand struct {
	Title   string
	Command string
}

// BuildMySQLUninstallCommands 构建经过路径安全校验的 MySQL 卸载命令。
// Manager 在 Agent 不在线时通过 SSH 执行同一组命令，避免两条清理链路行为漂移。
func BuildMySQLUninstallCommands(spec taskdomain.MySQLUninstallSpec) ([]MySQLUninstallCommand, error) {
	if spec.SystemdUnitName == "" {
		spec.SystemdUnitName = "mysqld"
	}
	if err := validateUninstallSpec(spec); err != nil {
		return nil, err
	}
	return []MySQLUninstallCommand{
		{Title: "停止 MySQL", Command: stopMySQLCommand(spec)},
		{Title: "取消开机自启", Command: fmt.Sprintf("systemctl disable %s 2>/dev/null || true", shellEscape(spec.SystemdUnitName))},
		{Title: "删除 systemd 管理文件", Command: fmt.Sprintf("rm -f /etc/systemd/system/%s.service", shellEscape(spec.SystemdUnitName))},
		{Title: "删除实例数据目录", Command: removeInstancePathsCommand(spec)},
		{Title: "删除临时安装包", Command: removePackageCommand(spec)},
		{Title: "删除安装目录和软链接", Command: uninstallBaseDirCommand(spec)},
		{Title: "删除配置文件", Command: removeExtraPathsCommand(spec)},
		{Title: "刷新 systemd", Command: "systemctl daemon-reload; systemctl reset-failed 2>/dev/null || true"},
		{Title: "验证卸载结果", Command: verifyUninstallCommand(spec)},
	}, nil
}

func validateUninstallSpec(spec taskdomain.MySQLUninstallSpec) error {
	paths := map[string]string{
		"instance_dir": spec.InstanceDir,
		"data_dir":     spec.DataDir,
		"binlog_dir":   spec.BinlogDir,
		"redo_dir":     spec.RedoDir,
		"undo_dir":     spec.UndoDir,
		"tmp_dir":      spec.TmpDir,
		"base_dir":     spec.BaseDir,
	}
	for label, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." && label != "instance_dir" && label != "base_dir" {
			continue
		}
		switch path {
		case "", ".", "/", "/data", "/var", "/usr", "/usr/local", "/opt", "/home", "/tmp":
			return fmt.Errorf("refuse to uninstall mysql with unsafe %s: %s", label, path)
		}
	}
	return nil
}

func removeInstancePathsCommand(spec taskdomain.MySQLUninstallSpec) string {
	paths := uniqueUninstallPaths(spec)
	args := make([]string, 0, len(paths))
	for _, path := range paths {
		args = append(args, shellEscape(path))
	}
	return "rm -rf -- " + strings.Join(args, " ")
}

func uniqueUninstallPaths(spec taskdomain.MySQLUninstallSpec) []string {
	values := []string{spec.InstanceDir, spec.DataDir, spec.BinlogDir, spec.RedoDir, spec.UndoDir, spec.TmpDir}
	seen := make(map[string]struct{}, len(values))
	paths := make([]string, 0, len(values))
	for _, value := range values {
		path := filepath.Clean(strings.TrimSpace(value))
		if path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func stopMySQLCommand(spec taskdomain.MySQLUninstallSpec) string {
	return fmt.Sprintf("systemctl stop %s 2>/dev/null || true", shellEscape(spec.SystemdUnitName))
}

func removePackageCommand(spec taskdomain.MySQLUninstallSpec) string {
	name := filepath.Base(strings.TrimSpace(spec.PackageName))
	if name == "" || name == "." || name == "/" {
		return "true"
	}
	return fmt.Sprintf("rm -f -- %s", shellEscape(filepath.Join("/tmp", name)))
}

func uninstallBaseDirCommand(spec taskdomain.MySQLUninstallSpec) string {
	baseDir := filepath.Clean(spec.BaseDir)
	installDir := mysqlPackageInstallDir(baseDir, spec.PackageName)
	if installDir == "" {
		return fmt.Sprintf(
			"target=''; if [ -L %s ]; then target=$(readlink -f %s 2>/dev/null || true); rm -f -- %s; elif [ -d %s ] && [ -z \"$(ls -A %s)\" ]; then rmdir -- %s; fi; if [ -n \"$target\" ] && [ \"$target\" != %s ] && [ -d \"$target\" ]; then rm -rf -- \"$target\"; fi",
			shellEscape(baseDir),
			shellEscape(baseDir),
			shellEscape(baseDir),
			shellEscape(baseDir),
			shellEscape(baseDir),
			shellEscape(baseDir),
			shellEscape(baseDir),
		)
	}
	return fmt.Sprintf(
		"if [ -L %s ]; then rm -f -- %s; elif [ -d %s ] && [ -z \"$(ls -A %s)\" ]; then rmdir -- %s; fi; if [ -n %s ]; then rm -rf -- %s; fi",
		shellEscape(baseDir),
		shellEscape(baseDir),
		shellEscape(baseDir),
		shellEscape(baseDir),
		shellEscape(baseDir),
		shellEscape(installDir),
		shellEscape(installDir),
	)
}

func removeExtraPathsCommand(spec taskdomain.MySQLUninstallSpec) string {
	paths := []string{spec.MyCnfPath}
	paths = append(paths, spec.ExtraPaths...)
	args := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			args = append(args, shellEscape(filepath.Clean(path)))
		}
	}
	if len(args) == 0 {
		return "true"
	}
	return "rm -f -- " + strings.Join(args, " ")
}

func verifyUninstallCommand(spec taskdomain.MySQLUninstallSpec) string {
	baseDir := filepath.Clean(spec.BaseDir)
	installDir := mysqlPackageInstallDir(baseDir, spec.PackageName)
	checks := make([]string, 0, 8)
	for _, path := range uniqueUninstallPaths(spec) {
		checks = append(checks, "test ! -e "+shellEscape(path))
	}
	checks = append(checks, "test ! -e /etc/systemd/system/"+shellEscape(spec.SystemdUnitName)+".service")
	if installDir == "" {
		checks = append(checks, "test ! -e "+shellEscape(baseDir))
		return strings.Join(checks, " && ")
	}
	checks = append(checks, "test ! -e "+shellEscape(baseDir), "test ! -e "+shellEscape(installDir))
	return strings.Join(checks, " && ")
}

func mysqlPackageInstallDir(baseDir, packageName string) string {
	name := strings.TrimSuffix(filepath.Base(packageName), ".tar.xz")
	name = strings.TrimSuffix(name, ".tgz")
	name = strings.TrimSuffix(name, ".tar.gz")
	if strings.TrimSpace(name) == "" || name == "." {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(baseDir)), name)
}
