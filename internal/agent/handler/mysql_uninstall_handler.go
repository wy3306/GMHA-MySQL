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
	if spec.SystemdUnitName == "" {
		spec.SystemdUnitName = "mysqld"
	}
	if err := validateUninstallSpec(spec); err != nil {
		return err
	}
	steps := []func(taskdomain.DispatchStep) error{
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "停止 MySQL", stopMySQLCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "取消开机自启", fmt.Sprintf("systemctl disable %s 2>/dev/null || true", shellEscape(spec.SystemdUnitName)))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "删除 systemd 管理文件", fmt.Sprintf("rm -f /etc/systemd/system/%s.service", shellEscape(spec.SystemdUnitName)))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "删除实例数据目录", fmt.Sprintf("rm -rf -- %s", shellEscape(filepath.Clean(spec.InstanceDir))))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "删除临时安装包", removePackageCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "删除安装目录和软链接", uninstallBaseDirCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "删除配置文件", removeExtraPathsCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "刷新 systemd", "systemctl daemon-reload; systemctl reset-failed 2>/dev/null || true")
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "验证卸载结果", verifyUninstallCommand(spec))
		},
	}
	for i, fn := range steps {
		if err := fn(r.task.Steps[i]); err != nil {
			return err
		}
	}
	if err := mysqlcheck.RemoveInstance(filepath.Join(r.installDir, mysqlcheck.DefaultConfigFile), spec.Port, spec.SocketPath); err != nil {
		return err
	}
	return nil
}

func validateUninstallSpec(spec taskdomain.MySQLUninstallSpec) error {
	for label, path := range map[string]string{
		"instance_dir": spec.InstanceDir,
		"base_dir":     spec.BaseDir,
	} {
		path = filepath.Clean(strings.TrimSpace(path))
		switch path {
		case "", ".", "/", "/data", "/usr", "/usr/local":
			return fmt.Errorf("refuse to uninstall mysql with unsafe %s: %s", label, path)
		}
	}
	return nil
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
	if installDir == "" {
		return fmt.Sprintf(
			"test ! -e %s && test ! -e %s && test ! -e /etc/systemd/system/%s.service",
			shellEscape(spec.InstanceDir),
			shellEscape(baseDir),
			shellEscape(spec.SystemdUnitName),
		)
	}
	return fmt.Sprintf(
		"test ! -e %s && test ! -e %s && test ! -e /etc/systemd/system/%s.service && test ! -e %s",
		shellEscape(spec.InstanceDir),
		shellEscape(baseDir),
		shellEscape(spec.SystemdUnitName),
		shellEscape(installDir),
	)
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
