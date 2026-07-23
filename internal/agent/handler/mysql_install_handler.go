package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcore "gmha/internal/agent/core"
	"gmha/internal/agent/mysqlcheck"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// MySQLInstallHandler 是 MySQL 安装任务处理器，负责在代理主机上自动化部署 MySQL 实例。
type MySQLInstallHandler struct {
	managerHTTPAddr string
	installDir      string
	client          *http.Client
}

// NewMySQLInstallHandler 创建一个新的 MySQL 安装任务处理器实例。
func NewMySQLInstallHandler(managerHTTPAddr string, installDir string) *MySQLInstallHandler {
	return &MySQLInstallHandler{
		managerHTTPAddr: strings.TrimRight(managerHTTPAddr, "/"),
		installDir:      strings.TrimSpace(installDir),
		client:          &http.Client{Timeout: 10 * time.Minute},
	}
}

// Type 返回该处理器处理的任务类型。
func (h *MySQLInstallHandler) Type() string {
	return string(taskdomain.TypeMySQLInstall)
}

// Handle 执行 MySQL 安装任务，包括环境检查、依赖安装、包下载解压、初始化、启动、账号创建等步骤。
func (h *MySQLInstallHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	var spec taskdomain.MySQLInstallSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return err
	}

	managerHTTPAddr := agentcore.ManagerHTTPAddrFromContext(ctx)
	if managerHTTPAddr == "" {
		managerHTTPAddr = h.managerHTTPAddr
	}
	runner := &mysqlInstallRunner{
		ctx:        ctx,
		task:       task,
		reporter:   reporter,
		spec:       spec,
		client:     h.client,
		baseURL:    managerHTTPAddr,
		installDir: h.installDir,
		stepStarts: make(map[string]time.Time),
		runner:     agentcore.NewCommandRunner(),
	}
	return runner.run()
}

type mysqlInstallRunner struct {
	ctx        context.Context
	task       taskdomain.DispatchTask
	reporter   *agentcore.Reporter
	spec       taskdomain.MySQLInstallSpec
	client     *http.Client
	baseURL    string
	installDir string

	stepStarts map[string]time.Time
	runner     *agentcore.CommandRunner
	topology   *taskdomain.MySQLTopologySpec
}

func (r *mysqlInstallRunner) run() error {
	packagePath := filepath.Join("/tmp", fmt.Sprintf("gmha-mysql-%d-%s", r.spec.Port, filepath.Base(r.spec.PackageName)))
	steps := []func(taskdomain.DispatchStep) error{
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "检查安装环境", r.checkEnvCommand())
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "关闭防火墙和 SELinux", disableFirewallSELinuxCommand())
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "卸载 MariaDB", uninstallMariaDBCommand())
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "安装依赖", installDependenciesCommand())
		},
		func(step taskdomain.DispatchStep) error {
			return r.optimizeLimitsStep(step)
		},
		func(step taskdomain.DispatchStep) error {
			return r.optimizeSysctlStep(step)
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "创建 MySQL 管理用户", fmt.Sprintf("id %s >/dev/null 2>&1 || useradd -r -s /sbin/nologin %s", shellEscape(r.spec.MySQLUser), shellEscape(r.spec.MySQLUser)))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "创建目录", fmt.Sprintf("mkdir -p %s %s %s %s %s %s && chown -R %s:%s %s", shellEscape(r.spec.DataDir), shellEscape(r.spec.BinlogDir), shellEscape(r.spec.RedoDir), shellEscape(r.spec.UndoDir), shellEscape(r.spec.TmpDir), shellEscape(filepath.Dir(filepath.Clean(r.spec.BaseDir))), shellEscape(r.spec.MySQLUser), shellEscape(r.spec.MySQLUser), shellEscape(r.spec.InstanceDir)))
		},
		func(step taskdomain.DispatchStep) error { return r.downloadPackageStep(step, packagePath) },
		func(step taskdomain.DispatchStep) error {
			return r.extractPackageStep(step, packagePath)
		},
		func(step taskdomain.DispatchStep) error {
			return r.createSymlinkStep(step)
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "检查 MySQL 二进制", fmt.Sprintf("%s/bin/mysql -V && %s/bin/mysqld --version", shellEscape(r.spec.BaseDir), shellEscape(r.spec.BaseDir)))
		},
		func(step taskdomain.DispatchStep) error {
			return r.writeFileStep(step, r.spec.EnvFilePath, r.spec.EnvContent)
		},
		func(step taskdomain.DispatchStep) error {
			return r.writeFileStep(step, r.spec.MyCnfPath, r.spec.MyCnfContent)
		},
		func(step taskdomain.DispatchStep) error {
			return r.writeFileStep(step, "/etc/systemd/system/"+r.spec.SystemdUnitName+".service", r.spec.SystemdContent)
		},
	}
	if r.spec.MemoryAllocator == "tcmalloc" {
		steps = append(steps, func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "安装并启用 tcmalloc", configureTCMallocCommand(r.spec.BaseDir, r.spec.SystemdUnitName))
		})
	}
	steps = append(steps,
		func(step taskdomain.DispatchStep) error {
			validate := mysqlConfigValidationCommand(filepath.Join(r.spec.BaseDir, "bin", "mysqld"), r.spec.MyCnfPath, r.spec.Version)
			initialize := fmt.Sprintf("%s/bin/mysqld --defaults-file=%s --initialize-insecure --user=%s", shellEscape(r.spec.BaseDir), shellEscape(r.spec.MyCnfPath), shellEscape(r.spec.MySQLUser))
			return r.runShellStep(step, "校验配置并初始化 MySQL", validate+" && "+initialize)
		},
		func(step taskdomain.DispatchStep) error {
			prefix := ""
			if r.spec.MemoryAllocator != "tcmalloc" {
				// A previous installation may have opted into tcmalloc. Selecting the
				// system allocator must actively remove that persistent drop-in.
				prefix = fmt.Sprintf("rm -f -- /etc/systemd/system/%s.service.d/allocator.conf; ", shellEscape(strings.TrimSuffix(r.spec.SystemdUnitName, ".service")))
			}
			return r.runShellStep(step, "启动 MySQL", prefix+fmt.Sprintf("systemctl daemon-reload && systemctl enable %s && systemctl restart %s", shellEscape(r.spec.SystemdUnitName), shellEscape(r.spec.SystemdUnitName)))
		},
		func(step taskdomain.DispatchStep) error {
			return r.waitMySQLReadyStep(step, "")
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "设置 root 密码", r.setRootPasswordCommand())
		},
		func(step taskdomain.DispatchStep) error {
			command := fmt.Sprintf("%s/bin/mysqladmin --connect-timeout=5 --socket=%s -uroot -p%s ping", shellEscape(r.spec.BaseDir), shellEscape(r.spec.SocketPath), shellEscape(r.spec.RootPassword))
			if r.spec.MemoryAllocator == "tcmalloc" {
				command += fmt.Sprintf(" && pid=$(systemctl show -p MainPID --value %s); test \"${pid:-0}\" -gt 1 && grep -q libtcmalloc_minimal /proc/$pid/maps", shellEscape(r.spec.SystemdUnitName))
			}
			return r.runShellStep(step, "验证 MySQL", command)
		},
	)
	if r.spec.InstallPTTools {
		steps = append(steps, func(step taskdomain.DispatchStep) error {
			ptURL := managerResourceURL(r.baseURL, r.spec.PTToolsPackageDownloadURL, "/api/v1/packages/percona-toolkit/")
			return r.runShellStep(step, "安装 Percona Toolkit", installPTToolsCommand(r.spec.BaseDir, r.spec.PTToolsPackageName, ptURL))
		})
	}
	if r.spec.InstallXtraBackup {
		steps = append(steps, func(step taskdomain.DispatchStep) error {
			xbkURL := managerResourceURL(r.baseURL, r.spec.XtraBackupPackageDownloadURL, "/api/v1/packages/xtrabackup/")
			return r.runShellStep(step, "安装 Percona XtraBackup", installXtraBackupCommand(r.spec.Version, r.spec.XtraBackupPackageName, xbkURL))
		})
	}
	steps = append(steps,
		func(step taskdomain.DispatchStep) error {
			return r.initAccountsStep(step)
		},
		func(step taskdomain.DispatchStep) error {
			return r.ensureHeartbeatTableStep(step)
		},
		func(step taskdomain.DispatchStep) error {
			return r.setupAgentCollectConfigStep(step)
		},
	)
	if len(r.task.Steps) != len(steps) {
		return fmt.Errorf("mysql_install step mismatch: task has %d steps, handler expects %d; recreate task with current manager", len(r.task.Steps), len(steps))
	}

	for i, fn := range steps {
		step := r.task.Steps[i]
		if err := fn(step); err != nil {
			return err
		}
	}
	return nil
}

func (r *mysqlInstallRunner) downloadPackageStep(step taskdomain.DispatchStep, packagePath string) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "正在下载本地 MySQL 安装包",
			StartedAt: &startedAt,
		},
		Event: &taskdomain.Event{TaskID: r.task.ID, StepID: step.ID, EventType: taskdomain.EventInfo, Content: "开始下载 MySQL 安装包"},
	})
	downloadURL := managerResourceURL(r.baseURL, r.spec.PackageDownloadURL, "/api/v1/software/mysql/")
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return r.failStep(step, err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return r.failStep(step, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r.failStep(step, fmt.Errorf("download mysql package failed: %s", resp.Status))
	}
	file, err := os.Create(packagePath)
	if err != nil {
		return r.failStep(step, err)
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return r.failStep(step, err)
	}
	info, err := file.Stat()
	if err != nil {
		return r.failStep(step, err)
	}
	if info.Size() == 0 {
		return r.failStep(step, fmt.Errorf("downloaded mysql package is empty: %s", packagePath))
	}
	return r.successStep(step, fmt.Sprintf("安装包已下载到 %s", packagePath), fmt.Sprintf("package_size=%d bytes", info.Size()))
}

// managerResourceURL rebases Manager-hosted resources onto the endpoint used
// by the active task connection. This also repairs old queued tasks whose spec
// contains an absolute URL pointing at a stale Manager interface.
func managerResourceURL(managerHTTPAddr, rawURL, managedPathPrefix string) string {
	base := strings.TrimRight(strings.TrimSpace(managerHTTPAddr), "/")
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err == nil && strings.HasPrefix(parsed.Path, managedPathPrefix) {
		return base + parsed.EscapedPath() + querySuffix(parsed.RawQuery)
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}
	return base + "/" + strings.TrimLeft(rawURL, "/")
}

func querySuffix(query string) string {
	if query == "" {
		return ""
	}
	return "?" + query
}

func (r *mysqlInstallRunner) extractPackageStep(step taskdomain.DispatchStep, packagePath string) error {
	installDir := r.packageInstallDir()
	command := fmt.Sprintf(
		"test -s %s && mkdir -p %s && rm -rf %s && mkdir -p %s && tar -xf %s -C %s --strip-components=1 && test -x %s/bin/mysqld && chown -R %s:%s %s",
		shellEscape(packagePath),
		shellEscape(filepath.Dir(installDir)),
		shellEscape(installDir),
		shellEscape(installDir),
		shellEscape(packagePath),
		shellEscape(installDir),
		shellEscape(installDir),
		shellEscape(r.spec.MySQLUser),
		shellEscape(r.spec.MySQLUser),
		shellEscape(installDir),
	)
	return r.runShellStep(step, "解压 MySQL", command)
}

func mysqlConfigValidationCommand(mysqldPath, configPath, version string) string {
	mysqld := shellEscape(mysqldPath)
	config := shellEscape(configPath)
	return fmt.Sprintf("if %s --no-defaults --verbose --help 2>/dev/null | grep -q -- '--validate-config'; then %s --defaults-file=%s --validate-config; else %s --defaults-file=%s --verbose --help >/dev/null; fi", mysqld, mysqld, config, mysqld, config)
}

func (r *mysqlInstallRunner) createSymlinkStep(step taskdomain.DispatchStep) error {
	cleanBaseDir := filepath.Clean(r.spec.BaseDir)
	installDir := r.packageInstallDir()
	command := fmt.Sprintf(
		"if [ -L %s ] || [ ! -e %s ]; then ln -sfn %s %s; elif [ -d %s ] && [ -z \"$(ls -A %s)\" ]; then rmdir %s && ln -sfn %s %s; else echo 'base_dir exists and is not an empty directory or symlink' >&2; exit 1; fi; test -x %s/bin/mysqld",
		shellEscape(cleanBaseDir),
		shellEscape(cleanBaseDir),
		shellEscape(installDir),
		shellEscape(cleanBaseDir),
		shellEscape(cleanBaseDir),
		shellEscape(cleanBaseDir),
		shellEscape(cleanBaseDir),
		shellEscape(installDir),
		shellEscape(cleanBaseDir),
		shellEscape(cleanBaseDir),
	)
	return r.runShellStep(step, "创建软链接", command)
}

func (r *mysqlInstallRunner) checkEnvCommand() string {
	service := r.spec.SystemdUnitName + ".service"
	if r.spec.SystemdUnitName == "" {
		service = "mysqld.service"
	}
	instanceDir := filepath.Clean(r.spec.InstanceDir)
	dataDir := filepath.Clean(r.spec.DataDir)
	baseDir := filepath.Clean(r.spec.BaseDir)
	baseParent := filepath.Dir(baseDir)
	installDir := r.packageInstallDir()
	return strings.Join([]string{
		`test "$(id -u)" = "0" || { echo 'mysql install must run as root'; exit 1; }`,
		`command -v tar >/dev/null 2>&1 || { echo 'missing tar'; exit 1; }`,
		`command -v systemctl >/dev/null 2>&1 || { echo 'missing systemctl'; exit 1; }`,
		`command -v apt-get >/dev/null 2>&1 || command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1 || { echo 'missing supported package manager: apt-get/dnf/yum'; exit 1; }`,
		fmt.Sprintf(`if systemctl list-unit-files %s 2>/dev/null | awk '{print $1}' | grep -qx %s || [ -e %s ] || [ -e %s ]; then echo 'mysql systemd unit already exists, uninstall before reinstall'; exit 1; fi`, shellEscape(service), shellEscape(service), shellEscape("/etc/systemd/system/"+service), shellEscape("/usr/lib/systemd/system/"+service)),
		fmt.Sprintf(`if command -v ss >/dev/null 2>&1 && ss -ltnH 2>/dev/null | awk '{print $4}' | grep -Eq '(^|:)%d$'; then echo 'mysql port %d is already listening'; exit 1; fi`, r.spec.Port, r.spec.Port),
		fmt.Sprintf(`if [ -d %s ]; then echo 'mysql data directory is already initialized, uninstall before reinstall'; exit 1; fi`, shellEscape(filepath.Join(dataDir, "mysql"))),
		fmt.Sprintf(`if [ -L %s ] && [ "$(readlink -f %s 2>/dev/null || true)" != %s ]; then echo 'base_dir symlink is already used by another mysql instance'; exit 1; fi`, shellEscape(baseDir), shellEscape(baseDir), shellEscape(installDir)),
		fmt.Sprintf(`if [ -e %s ] && [ ! -L %s ]; then if [ -d %s ] && [ -z "$(find %s -mindepth 1 -maxdepth 1 2>/dev/null | head -n 1)" ]; then :; else echo 'base_dir exists and is not an empty directory or symlink'; exit 1; fi; fi`, shellEscape(baseDir), shellEscape(baseDir), shellEscape(baseDir), shellEscape(baseDir)),
		fmt.Sprintf(`mkdir -p %s %s`, shellEscape(instanceDir), shellEscape(baseParent)),
		fmt.Sprintf(`test -w %s || { echo 'instance parent is not writable'; exit 1; }`, shellEscape(filepath.Dir(instanceDir))),
		fmt.Sprintf(`avail_mb=$(df -Pm %s | awk 'NR==2 {print $4}'); test "${avail_mb:-0}" -ge 1024 || { echo 'not enough disk space, require at least 1024MB free'; exit 1; }`, shellEscape(instanceDir)),
	}, "; ")
}

func disableFirewallSELinuxCommand() string {
	return strings.Join([]string{
		`if systemctl list-unit-files firewalld.service >/dev/null 2>&1; then systemctl stop firewalld 2>/dev/null || true; systemctl disable firewalld 2>/dev/null || true; fi`,
		`if command -v ufw >/dev/null 2>&1; then ufw --force disable 2>/dev/null || true; systemctl stop ufw 2>/dev/null || true; systemctl disable ufw 2>/dev/null || true; fi`,
		`if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce 2>/dev/null)" != "Disabled" ]; then setenforce 0 2>/dev/null || true; fi`,
		`if [ -f /etc/selinux/config ]; then sed -ri 's/^[[:space:]]*SELINUX=.*/SELINUX=disabled/' /etc/selinux/config; fi`,
		`echo 'firewall and selinux disabled when present'`,
	}, "; ")
}

func installDependenciesCommand() string {
	return strings.Join([]string{
		`run_with_timeout() { if command -v timeout >/dev/null 2>&1; then timeout 300 "$@"; else "$@"; fi; }`,
		`missing=""`,
		`if command -v apt-get >/dev/null 2>&1; then for p in xz-utils libncurses6 numactl openssl perl curl ca-certificates; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"; done; dpkg -s libaio1 >/dev/null 2>&1 || dpkg -s libaio1t64 >/dev/null 2>&1 || missing="$missing libaio1"; if [ -n "$missing" ]; then DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends $missing libaio-dev || { DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get update && DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends xz-utils libaio1t64 libaio-dev libncurses6 numactl openssl perl curl ca-certificates; }; else echo "dependencies already installed"; fi; dpkg -s libncurses5 >/dev/null 2>&1 || DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends libncurses5 >/dev/null 2>&1 || true`,
		`elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then rpm -qa >/dev/null || { echo "rpmdb is broken, run: rpm --rebuilddb"; exit 1; }; pm=dnf; command -v dnf >/dev/null 2>&1 || pm=yum; for p in xz libaio numactl-libs openssl perl curl ca-certificates; do rpm -q "$p" >/dev/null 2>&1 || missing="$missing $p"; done; rpm -q libaio-devel >/dev/null 2>&1 || missing="$missing libaio-devel"; if ! rpm -q ncurses-compat-libs >/dev/null 2>&1 && ! rpm -q ncurses >/dev/null 2>&1; then missing="$missing ncurses-compat-libs"; fi; if [ -n "$missing" ]; then run_with_timeout $pm -y install $missing || run_with_timeout $pm -y install xz libaio libaio-devel numactl-libs ncurses openssl perl curl ca-certificates; else echo "dependencies already installed"; fi`,
		`fi`,
		`if ! ldconfig -p 2>/dev/null | grep -q "libaio.so.1 "; then for p in /usr/lib/*/libaio.so.1t64 /lib/*/libaio.so.1t64; do if [ -e "$p" ]; then ln -sfn "$p" "$(dirname "$p")/libaio.so.1"; fi; done; ldconfig 2>/dev/null || true; fi`,
	}, "; ")
}

// installPTToolsCommand 安装 Percona Toolkit，并验证核心 pt 命令可用。
func installPTToolsCommand(mysqlBaseDir, packageName, packageURL string) string {
	return strings.Join([]string{
		fmt.Sprintf(`mysql_version=$(%s/bin/mysql --version 2>/dev/null | sed -nE 's/.*(Distrib|Ver)[[:space:]]+([0-9]+\.[0-9]+).*/\2/p' | head -1)`, shellEscape(mysqlBaseDir)),
		`case "$mysql_version" in 5.7) min_pt_version=3.5.0 ;; 8.*|9.*) min_pt_version=3.7.1 ;; *) echo "unsupported MySQL version for automatic PT installation: $mysql_version" >&2; exit 1 ;; esac`,
		`download() { if command -v curl >/dev/null 2>&1; then curl -fsSLo "$2" "$1"; elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"; else echo "curl or wget is required" >&2; return 1; fi; }`,
		fmt.Sprintf(`pt_package_name=%s`, shellEscape(packageName)),
		fmt.Sprintf(`pt_package_url=%s`, shellEscape(packageURL)),
		`[ -n "$pt_package_name" ] && [ -n "$pt_package_url" ] || { echo "local Percona Toolkit package is not configured" >&2; exit 1; }`,
		`pt_archive="/tmp/$pt_package_name"; pt_stage=/tmp/gmha-percona-toolkit; pt_install=/opt/gmha-tools/percona-toolkit`,
		`download "$pt_package_url" "$pt_archive"`,
		`test -s "$pt_archive" && rm -rf "$pt_stage" && mkdir -p "$pt_stage" && tar -xzf "$pt_archive" -C "$pt_stage"`,
		`pt_source=$(find "$pt_stage" -type f -path '*/bin/pt-table-sync' -print -quit); [ -n "$pt_source" ] || { echo "invalid offline PT bundle: bin/pt-table-sync not found" >&2; exit 1; }; pt_source=$(dirname "$(dirname "$pt_source")")`,
		`pt_packages=$(find "$pt_stage" -type d -name packages -print -quit); if [ -n "$pt_packages" ]; then debs=$(find "$pt_packages" -type f -name '*.deb' -print); rpms=$(find "$pt_packages" -type f -name '*.rpm' -print); apks=$(find "$pt_packages" -type f -name '*.apk' -print); archpkgs=$(find "$pt_packages" -type f \( -name '*.pkg.tar.zst' -o -name '*.pkg.tar.xz' \) -print); if [ -n "$debs" ] && command -v dpkg >/dev/null 2>&1; then dpkg -i $debs; elif [ -n "$rpms" ] && command -v rpm >/dev/null 2>&1; then rpm -Uvh --replacepkgs $rpms; elif [ -n "$apks" ] && command -v apk >/dev/null 2>&1; then apk add --no-network --allow-untrusted $apks; elif [ -n "$archpkgs" ] && command -v pacman >/dev/null 2>&1; then pacman -U --noconfirm $archpkgs; fi; fi`,
		`command -v perl >/dev/null 2>&1 || { echo "offline PT bundle does not contain a Perl package compatible with this Linux distribution" >&2; exit 1; }`,
		`rm -rf "$pt_install" && mkdir -p "$(dirname "$pt_install")" && cp -a "$pt_source" "$pt_install"`,
		`pt_perl5lib="$pt_install/vendor/perl5:$pt_install/lib/perl5:$pt_install/lib"; export PERL5LIB="$pt_perl5lib${PERL5LIB:+:$PERL5LIB}"`,
		`perl -MDBI -MDBD::mysql -MIO::Socket::SSL -MTerm::ReadKey -MDigest::MD5 -MTime::HiRes -e 1 || { echo "offline PT dependencies are incomplete; bundle vendor/perl5 or native packages under packages/" >&2; exit 1; }`,
		`for pt_script in "$pt_install"/bin/pt-*; do [ -f "$pt_script" ] || continue; pt_cmd=$(basename "$pt_script"); printf '#!/bin/sh\nPT_HOME=%s\nexport PERL5LIB="$PT_HOME/vendor/perl5:$PT_HOME/lib/perl5:$PT_HOME/lib${PERL5LIB:+:$PERL5LIB}"\nexec perl "$PT_HOME/bin/%s" "$@"\n' "$pt_install" "$pt_cmd" > "/usr/local/bin/$pt_cmd" && chmod 0755 "/usr/local/bin/$pt_cmd"; done`,
		`hash -r`,
		`pt_version=$(pt-table-sync --version 2>/dev/null | sed -nE 's/.* ([0-9]+\.[0-9]+\.[0-9]+).*/\1/p' | head -1)`,
		`[ -n "$pt_version" ] || { echo "unable to determine Percona Toolkit version" >&2; exit 1; }`,
		`[ "$(printf '%s\n%s\n' "$min_pt_version" "$pt_version" | sort -V | head -1)" = "$min_pt_version" ] || { echo "Percona Toolkit $pt_version is incompatible; MySQL $mysql_version requires >= $min_pt_version" >&2; exit 1; }`,
		`for pt_cmd in pt-table-sync pt-table-checksum pt-online-schema-change pt-archiver pt-query-digest pt-replica-restart; do command -v "$pt_cmd" >/dev/null 2>&1 && "$pt_cmd" --version >/dev/null || { echo "Percona Toolkit validation failed: $pt_cmd" >&2; exit 1; }; done`,
		`pt-table-sync --version; pt-table-checksum --version; pt-online-schema-change --version; pt-archiver --version; pt-query-digest --version`,
	}, "; ")
}

func configureTCMallocCommand(mysqlBaseDir, systemdUnit string) string {
	dropIn := "/etc/systemd/system/" + strings.TrimSuffix(systemdUnit, ".service") + ".service.d/allocator.conf"
	return strings.Join([]string{
		`run_with_timeout() { if command -v timeout >/dev/null 2>&1; then timeout 600 "$@"; else "$@"; fi; }`,
		`if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get update; DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends libgoogle-perftools4 || DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends libgoogle-perftools4t64`,
		`elif command -v dnf >/dev/null 2>&1; then run_with_timeout dnf -y install gperftools-libs`,
		`elif command -v yum >/dev/null 2>&1; then run_with_timeout yum -y install gperftools-libs`,
		`else echo "unsupported package manager for tcmalloc" >&2; exit 1; fi`,
		`tcmalloc_lib=$(ldconfig -p 2>/dev/null | awk '/libtcmalloc_minimal\.so/{print $NF; exit}')`,
		`[ -r "$tcmalloc_lib" ] || { echo "libtcmalloc_minimal was not installed" >&2; exit 1; }`,
		fmt.Sprintf(`LD_PRELOAD="$tcmalloc_lib" %s/bin/mysqld --version >/dev/null`, shellEscape(mysqlBaseDir)),
		fmt.Sprintf(`mkdir -p %s && printf '[Service]\nEnvironment="LD_PRELOAD=%%s"\n' "$tcmalloc_lib" > %s`, shellEscape(filepath.Dir(dropIn)), shellEscape(dropIn)),
		`systemctl daemon-reload`,
		`echo "tcmalloc configured: $tcmalloc_lib"`,
	}, "; ")
}

func installXtraBackupCommand(mysqlVersion, packageName, packageURL string) string {
	return strings.Join([]string{
		fmt.Sprintf(`mysql_series=$(printf '%%s' %s | sed -nE 's/^([0-9]+\.[0-9]+).*/\1/p')`, shellEscape(mysqlVersion)),
		`case "$mysql_series" in 5.7) xbk_series=2.4 ;; *) xbk_series="$mysql_series" ;; esac`,
		fmt.Sprintf(`xbk_package_name=%s`, shellEscape(packageName)),
		fmt.Sprintf(`xbk_package_url=%s`, shellEscape(packageURL)),
		`[ -n "$mysql_series" ] && [ -n "$xbk_package_name" ] && [ -n "$xbk_package_url" ] || { echo "XtraBackup package metadata is incomplete" >&2; exit 1; }`,
		`case "$xbk_package_name" in *xtrabackup-"$xbk_series".*|*XtraBackup-"$xbk_series".*) ;; *) echo "XtraBackup package $xbk_package_name does not match MySQL $mysql_series (required XtraBackup $xbk_series)" >&2; exit 1 ;; esac`,
		`run_with_timeout() { if command -v timeout >/dev/null 2>&1; then timeout 900 "$@"; else "$@"; fi; }`,
		`download() { if command -v curl >/dev/null 2>&1; then curl -fsSLo "$2" "$1"; elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"; else echo "curl or wget is required" >&2; return 1; fi; }`,
		`if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get update; DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends libev4 libgcrypt20 libcurl4 openssl zlib1g rsync lz4 zstd libaio1 || DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y install --no-install-recommends libev4 libgcrypt20 libcurl4t64 openssl zlib1g rsync lz4 zstd libaio1t64`,
		`elif command -v dnf >/dev/null 2>&1; then run_with_timeout dnf -y install libev libgcrypt libcurl openssl-libs zlib libaio rsync lz4 zstd`,
		`elif command -v yum >/dev/null 2>&1; then run_with_timeout yum -y install libev libgcrypt libcurl openssl-libs zlib libaio rsync lz4 zstd`,
		`else echo "unsupported package manager for XtraBackup dependencies" >&2; exit 1; fi`,
		`xbk_archive="/tmp/$xbk_package_name"; xbk_stage=/tmp/gmha-xtrabackup-install; xbk_root=/opt/gmha-tools`,
		`download "$xbk_package_url" "$xbk_archive" && test -s "$xbk_archive"`,
		`rm -rf "$xbk_stage" && mkdir -p "$xbk_stage" "$xbk_root" && tar -xzf "$xbk_archive" -C "$xbk_stage"`,
		`xbk_bin=$(find "$xbk_stage" -type f -path '*/bin/xtrabackup' -perm -u+x -print -quit); [ -n "$xbk_bin" ] || { echo "xtrabackup binary not found in archive" >&2; exit 1; }`,
		`xbk_source=$(dirname "$(dirname "$xbk_bin")"); xbk_version=$("$xbk_bin" --version 2>&1 | sed -nE 's/.*version ([0-9]+\.[0-9]+\.[0-9]+[^ ]*).*/\1/p' | head -1); [ -n "$xbk_version" ] || xbk_version="$xbk_series"`,
		`xbk_install="$xbk_root/percona-xtrabackup-$xbk_version"; rm -rf "$xbk_install" && mv "$xbk_source" "$xbk_install"`,
		`for xbk_cmd in xtrabackup xbstream xbcloud xbcloud_osenv xbcrypt; do if [ -x "$xbk_install/bin/$xbk_cmd" ]; then ln -sfn "$xbk_install/bin/$xbk_cmd" "/usr/local/bin/$xbk_cmd"; fi; done`,
		`hash -r; command -v xtrabackup >/dev/null; command -v xbstream >/dev/null; ! ldd "$xbk_install/bin/xtrabackup" 2>/dev/null | grep -q 'not found'`,
		`xtrabackup --version 2>&1 | grep -E "version $xbk_series\\.|based on MySQL server $mysql_series\\." >/dev/null || { echo "installed XtraBackup $xbk_series does not match MySQL $mysql_series" >&2; exit 1; }`,
		`xtrabackup --version; echo "XtraBackup dependencies and binaries verified"`,
	}, "; ")
}

func uninstallMariaDBCommand() string {
	return strings.Join([]string{
		`run_with_timeout() { if command -v timeout >/dev/null 2>&1; then timeout 300 "$@"; else "$@"; fi; }`,
		`systemctl stop mariadb 2>/dev/null || true`,
		`systemctl disable mariadb 2>/dev/null || true`,
		`if command -v rpm >/dev/null 2>&1; then rpm -qa >/dev/null || { echo "rpmdb is broken, run: rpm --rebuilddb"; exit 1; }; pkgs=$(rpm -qa | grep -Ei "^(mariadb|MariaDB)" || true); if [ -n "$pkgs" ]; then if command -v dnf >/dev/null 2>&1; then run_with_timeout dnf -y remove $pkgs; elif command -v yum >/dev/null 2>&1; then run_with_timeout yum -y remove $pkgs; fi; else echo "no mariadb rpm packages"; fi`,
		`elif command -v dpkg-query >/dev/null 2>&1; then pkgs=$(dpkg-query -W -f="${binary:Package}\n" "mariadb*" 2>/dev/null | grep -v "^$" || true); if [ -n "$pkgs" ]; then DEBIAN_FRONTEND=noninteractive run_with_timeout apt-get -y remove $pkgs; else echo "no mariadb deb packages"; fi`,
		`fi`,
	}, "; ")
}

func (r *mysqlInstallRunner) setRootPasswordCommand() string {
	mysql := filepath.Join(r.spec.BaseDir, "bin", "mysql")
	mysqladmin := filepath.Join(r.spec.BaseDir, "bin", "mysqladmin")
	socket := r.spec.SocketPath
	password := r.spec.RootPassword
	sql := fmt.Sprintf(
		"SET GLOBAL super_read_only=0; SET GLOBAL read_only=0; ALTER USER 'root'@'localhost' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;",
		mysqlSQLEscape(password),
	)
	return fmt.Sprintf(
		"%s --protocol=socket --socket=%s -uroot -e %s || %s --socket=%s -uroot flush-privileges password %s",
		shellEscape(mysql),
		shellEscape(socket),
		shellEscape(sql),
		shellEscape(mysqladmin),
		shellEscape(socket),
		shellEscape(password),
	)
}

func (r *mysqlInstallRunner) waitMySQLReadyStep(step taskdomain.DispatchStep, rootPassword string) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "等待 MySQL 可连接",
			StartedAt: &startedAt,
		},
	})
	command := fmt.Sprintf("%s/bin/mysqladmin --connect-timeout=2 --socket=%s -uroot ping", shellEscape(r.spec.BaseDir), shellEscape(r.spec.SocketPath))
	if rootPassword != "" {
		command = fmt.Sprintf("%s/bin/mysqladmin --connect-timeout=2 --socket=%s -uroot -p%s ping", shellEscape(r.spec.BaseDir), shellEscape(r.spec.SocketPath), shellEscape(rootPassword))
	}
	output, err := r.runShellCommand(step, fmt.Sprintf("for i in $(seq 1 30); do %s >/tmp/gmha-mysql-ready.out 2>&1 && cat /tmp/gmha-mysql-ready.out && exit 0; sleep 1; done; cat /tmp/gmha-mysql-ready.out 2>/dev/null; exit 1", command))
	if err != nil {
		if output == "" {
			output = "MySQL 刚启动暂不可连接"
		}
		return r.failStep(step, fmt.Errorf("连接失败: %s", output))
	}
	return r.successStep(step, "MySQL 已可连接", output)
}

func (r *mysqlInstallRunner) initAccountsStep(step taskdomain.DispatchStep) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "初始化 MySQL 账号",
			StartedAt: &startedAt,
		},
	})
	initializer := mysqlapp.AccountInitializer{
		Socket:       r.spec.SocketPath,
		RootPassword: r.spec.RootPassword,
		Version:      r.spec.Version,
		Timeout:      5 * time.Second,
	}
	if err := initializer.WaitReady(r.ctx, 30, time.Second); err != nil {
		result := mysqlapp.AccountInitResult{
			Enabled:   true,
			Success:   false,
			Retryable: true,
			Summary:   "连接 MySQL 失败: " + err.Error(),
			Items: []mysqlapp.AccountInitItemResult{{
				Enabled:   true,
				Success:   false,
				Retryable: true,
				Error:     "连接失败: " + err.Error(),
			}},
		}
		return r.finishAccountInitStep(step, result)
	}
	result := initializer.Initialize(r.ctx, mysqlAccountSpecsFromDomain(r.spec.Accounts))
	return r.finishAccountInitStep(step, result)
}

func (r *mysqlInstallRunner) ensureHeartbeatTableStep(step taskdomain.DispatchStep) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "创建心跳表",
			StartedAt: &startedAt,
		},
	})

	instance := r.getHeartbeatInstance()
	if err := mysqlcheck.EnsureInstance(r.ctx, instance); err != nil {
		return r.failStep(step, fmt.Errorf("创建心跳表失败: %w", err))
	}

	return r.successStep(step, "心跳表创建成功", "")
}

func (r *mysqlInstallRunner) setupAgentCollectConfigStep(step taskdomain.DispatchStep) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "下发 Agent 采集配置",
			StartedAt: &startedAt,
		},
	})

	instance := r.getHeartbeatInstance()
	path := filepath.Join(r.installDir, mysqlcheck.DefaultConfigFile)
	if err := mysqlcheck.UpsertInstance(path, instance); err != nil {
		return r.failStep(step, fmt.Errorf("下发采集配置失败: %w", err))
	}

	return r.successStep(step, "采集配置下发成功", "config_path="+path)
}

func (r *mysqlInstallRunner) getHeartbeatInstance() mysqlcheck.InstanceConfig {
	// 默认使用 mha 账号进行采集
	username := "mha"
	password := "3306niubi"

	// 从 spec 中查找 mha 账号配置
	for _, acc := range r.spec.Accounts {
		if acc.Role == "mha" && acc.Enabled {
			username = acc.Username
			password = acc.Password
			break
		}
	}

	return mysqlcheck.InstanceConfig{
		Port:               r.spec.Port,
		Socket:             r.spec.SocketPath,
		Username:           username,
		Password:           password,
		ManagementUsername: "root",
		ManagementPassword: r.spec.RootPassword,
		Database:           "gmha",
		SystemdUnit:        r.spec.SystemdUnitName,
		DataDir:            r.spec.DataDir,
		BinlogDir:          r.spec.BinlogDir,
		RedoDir:            r.spec.RedoDir,
		TmpDir:             r.spec.TmpDir,
		UndoDir:            r.spec.UndoDir,
	}
}

func (r *mysqlInstallRunner) finishAccountInitStep(step taskdomain.DispatchStep, result mysqlapp.AccountInitResult) error {
	now := time.Now().UTC()
	startedAt := r.stepStartedAt(step, now)
	report := taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:     step.ID,
			StepNo:     step.StepNo,
			StepName:   step.StepName,
			Status:     taskdomain.StepSuccess,
			Message:    result.Summary,
			StartedAt:  &startedAt,
			FinishedAt: &now,
		},
		Event: &taskdomain.Event{
			TaskID:    r.task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventInfo,
			Content:   accountInitLog(result),
		},
	}
	if !result.Success {
		report.Event.EventType = taskdomain.EventError
	}
	if step.StepNo == len(r.task.Steps) {
		report.Status = taskdomain.StatusSuccess
		report.Progress = 100
		report.Result = r.finalResultWithAccounts(result)
	}
	return r.reporter.Report(report)
}

func mhaHeartbeatAccount(result mysqlapp.AccountInitResult, accounts []taskdomain.MySQLAccountSpec) (taskdomain.MySQLAccountSpec, bool) {
	normalized := mysqlapp.NormalizeAccountSpecs(mysqlAccountSpecsFromDomain(accounts))
	success := make(map[string]bool, len(result.Items))
	for _, item := range result.Items {
		if item.Role == mysqlapp.AccountRoleMHA && item.Enabled && item.Success {
			success[mysqlapp.AccountRoleMHA] = true
		}
	}
	for _, item := range normalized {
		if item.Role != mysqlapp.AccountRoleMHA || !item.Enabled || !success[mysqlapp.AccountRoleMHA] {
			continue
		}
		return taskdomain.MySQLAccountSpec{
			Role:       item.Role,
			Username:   item.Username,
			Password:   item.Password,
			Host:       item.Host,
			Enabled:    item.Enabled,
			Privileges: item.Privileges,
		}, true
	}
	return taskdomain.MySQLAccountSpec{}, false
}

func mysqlAccountSpecsFromDomain(items []taskdomain.MySQLAccountSpec) []mysqlapp.AccountSpec {
	out := make([]mysqlapp.AccountSpec, 0, len(items))
	for _, item := range items {
		out = append(out, mysqlapp.AccountSpec{
			Role:           item.Role,
			Username:       item.Username,
			Password:       item.Password,
			Host:           item.Host,
			Enabled:        item.Enabled,
			ExtendedBackup: item.ExtendedBackup,
			Privileges:     item.Privileges,
		})
	}
	return out
}

func mysqlAccountResultToDomain(result mysqlapp.AccountInitResult) *taskdomain.MySQLAccountInitResult {
	out := &taskdomain.MySQLAccountInitResult{
		Enabled:        result.Enabled,
		Success:        result.Success,
		PartialSuccess: result.PartialSuccess,
		Retryable:      result.Retryable,
		Summary:        result.Summary,
		Items:          make([]taskdomain.MySQLAccountInitItemResult, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		out.Items = append(out.Items, taskdomain.MySQLAccountInitItemResult{
			Role:            item.Role,
			Username:        item.Username,
			Host:            item.Host,
			Enabled:         item.Enabled,
			Skipped:         item.Skipped,
			UserCreated:     item.UserCreated,
			PasswordUpdated: item.PasswordUpdated,
			Granted:         item.Granted,
			Success:         item.Success,
			Retryable:       item.Retryable,
			Error:           item.Error,
			ExecutedSteps:   append([]string(nil), item.ExecutedSteps...),
		})
	}
	return out
}

func accountInitLog(result mysqlapp.AccountInitResult) string {
	data, err := json.Marshal(result)
	if err != nil {
		return result.Summary
	}
	return string(data)
}

func (r *mysqlInstallRunner) packageInstallDir() string {
	name := strings.TrimSuffix(r.spec.PackageName, ".tar.xz")
	name = strings.TrimSuffix(name, ".tgz")
	name = strings.TrimSuffix(name, ".tar.gz")
	if name == "" {
		name = "mysql"
	}
	return filepath.Join(filepath.Dir(filepath.Clean(r.spec.BaseDir)), fmt.Sprintf("%s-%d", name, r.spec.Port))
}

func (r *mysqlInstallRunner) writeFileStep(step taskdomain.DispatchStep, path, content string) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "写入文件",
			StartedAt: &startedAt,
		},
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return r.failStep(step, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return r.failStep(step, err)
	}
	return r.successStep(step, "文件已写入 "+path, "")
}

func (r *mysqlInstallRunner) optimizeLimitsStep(step taskdomain.DispatchStep) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "优化 limits 和 PAM",
			StartedAt: &startedAt,
		},
	})
	if err := os.MkdirAll(filepath.Dir(r.spec.LimitsPath), 0o755); err != nil {
		return r.failStep(step, err)
	}
	if err := os.WriteFile(r.spec.LimitsPath, []byte(r.spec.LimitsContent), 0o644); err != nil {
		return r.failStep(step, err)
	}
	nprocPath := "/etc/security/limits.d/99-gmha-nproc.conf"
	nprocContent := fmt.Sprintf("%s soft nproc 65536\n%s hard nproc 65536\nroot soft nproc unlimited\n", r.spec.MySQLUser, r.spec.MySQLUser)
	if err := os.WriteFile(nprocPath, []byte(nprocContent), 0o644); err != nil {
		return r.failStep(step, err)
	}
	out, err := r.runShellCommand(step, `for f in /etc/pam.d/login /etc/pam.d/common-session /etc/pam.d/common-session-noninteractive; do if [ -f "$f" ] && ! grep -q 'pam_limits.so' "$f"; then printf '\nsession required pam_limits.so\n' >> "$f"; fi; done`)
	if err != nil {
		return r.failStep(step, fmt.Errorf("%v: %s", err, out))
	}
	return r.successStep(step, "limits 和 PAM 已优化", out)
}

func (r *mysqlInstallRunner) optimizeSysctlStep(step taskdomain.DispatchStep) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   "优化 sysctl",
			StartedAt: &startedAt,
		},
	})
	if err := os.MkdirAll(filepath.Dir(r.spec.SysctlPath), 0o755); err != nil {
		return r.failStep(step, err)
	}
	if err := os.WriteFile(r.spec.SysctlPath, []byte(r.spec.SysctlContent), 0o644); err != nil {
		return r.failStep(step, err)
	}
	command := fmt.Sprintf(`while IFS= read -r line; do line="${line%%#*}"; [ -n "$(printf '%%s' "$line" | tr -d '[:space:]')" ] || continue; key="${line%%=*}"; value="${line#*=}"; key="$(printf '%%s' "$key" | xargs)"; value="$(printf '%%s' "$value" | xargs)"; [ -n "$key" ] || continue; sysctl -w "$key=$value" >/dev/null 2>&1 || echo "skip unsupported sysctl $key"; done < %s`, shellEscape(r.spec.SysctlPath))
	out, err := r.runShellCommand(step, command)
	if err != nil {
		return r.failStep(step, fmt.Errorf("%v: %s", err, out))
	}
	return r.successStep(step, "sysctl 已优化", out)
}

func (r *mysqlInstallRunner) runShellStep(step taskdomain.DispatchStep, message, command string) error {
	startedAt := r.markStepStarted(step)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:    step.ID,
			StepNo:    step.StepNo,
			StepName:  step.StepName,
			Status:    taskdomain.StepRunning,
			Message:   message,
			StartedAt: &startedAt,
		},
	})
	output, err := r.runShellCommand(step, command)
	if err != nil {
		if output == "" {
			output = message
		}
		return r.failStep(step, fmt.Errorf("%v: %s", err, output))
	}
	return r.successStep(step, message, output)
}

func (r *mysqlInstallRunner) runShellCommand(step taskdomain.DispatchStep, command string) (string, error) {
	output, err := r.runner.RunShell(r.ctx, r.task.ID, step.StepName, command)
	return mysqlCommandOutput(output, ""), err
}

func (r *mysqlInstallRunner) successStep(step taskdomain.DispatchStep, message, logText string) error {
	now := time.Now().UTC()
	startedAt := r.stepStartedAt(step, now)
	report := taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusRunning,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:     step.ID,
			StepNo:     step.StepNo,
			StepName:   step.StepName,
			Status:     taskdomain.StepSuccess,
			Message:    message,
			StartedAt:  &startedAt,
			FinishedAt: &now,
		},
	}
	if strings.TrimSpace(logText) != "" {
		report.Event = &taskdomain.Event{
			TaskID:    r.task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventLog,
			Content:   strings.TrimSpace(logText),
		}
	}
	if step.StepNo == len(r.task.Steps) {
		report.Status = taskdomain.StatusSuccess
		report.Progress = 100
		report.Result = r.finalResult()
	}
	return r.reporter.Report(report)
}

func (r *mysqlInstallRunner) finalResult() json.RawMessage {
	if r.task.Type == string(taskdomain.TypeMySQLUninstall) {
		resultJSON, _ := json.Marshal(taskdomain.MySQLUninstallResult{
			Port:        r.spec.Port,
			InstanceDir: r.spec.InstanceDir,
			BaseDir:     r.spec.BaseDir,
			SystemdUnit: r.spec.SystemdUnitName,
		})
		return resultJSON
	}
	if r.task.Type == string(taskdomain.TypeMySQLTopology) && r.topology != nil {
		resultJSON, _ := json.Marshal(taskdomain.MySQLTopologyResult{
			Topology: r.topology.Topology,
			Port:     r.topology.Port,
			Node:     r.topology.Node,
		})
		return resultJSON
	}
	resultJSON, _ := json.Marshal(taskdomain.MySQLInstallResult{
		Port:         r.spec.Port,
		ServerID:     r.spec.ServerID,
		MySQLUser:    r.spec.MySQLUser,
		InstanceDir:  r.spec.InstanceDir,
		DataDir:      r.spec.DataDir,
		BinlogDir:    r.spec.BinlogDir,
		RedoDir:      r.spec.RedoDir,
		UndoDir:      r.spec.UndoDir,
		TmpDir:       r.spec.TmpDir,
		BaseDir:      r.spec.BaseDir,
		Profile:      r.spec.Profile,
		PackageName:  r.spec.PackageName,
		Version:      r.spec.Version,
		Architecture: r.spec.Architecture,
		SystemdUnit:  r.spec.SystemdUnitName,
		MyCnfPath:    r.spec.MyCnfPath,
		SocketPath:   r.spec.SocketPath,
	})
	return resultJSON
}

func (r *mysqlInstallRunner) finalResultWithAccounts(accountResult mysqlapp.AccountInitResult) json.RawMessage {
	resultJSON, _ := json.Marshal(taskdomain.MySQLInstallResult{
		Port:         r.spec.Port,
		ServerID:     r.spec.ServerID,
		MySQLUser:    r.spec.MySQLUser,
		InstanceDir:  r.spec.InstanceDir,
		DataDir:      r.spec.DataDir,
		BinlogDir:    r.spec.BinlogDir,
		RedoDir:      r.spec.RedoDir,
		UndoDir:      r.spec.UndoDir,
		TmpDir:       r.spec.TmpDir,
		BaseDir:      r.spec.BaseDir,
		Profile:      r.spec.Profile,
		PackageName:  r.spec.PackageName,
		Version:      r.spec.Version,
		Architecture: r.spec.Architecture,
		SystemdUnit:  r.spec.SystemdUnitName,
		MyCnfPath:    r.spec.MyCnfPath,
		SocketPath:   r.spec.SocketPath,
		AccountInit:  mysqlAccountResultToDomain(accountResult),
	})
	return resultJSON
}

func (r *mysqlInstallRunner) failStep(step taskdomain.DispatchStep, err error) error {
	now := time.Now().UTC()
	startedAt := r.stepStartedAt(step, now)
	_ = r.reporter.Report(taskdomain.ReportEnvelope{
		TaskID:      r.task.ID,
		Status:      taskdomain.StatusFailed,
		Progress:    progress(step.StepNo, len(r.task.Steps)),
		CurrentStep: step.StepName,
		Step: &taskdomain.StepReport{
			StepID:     step.ID,
			StepNo:     step.StepNo,
			StepName:   step.StepName,
			Status:     taskdomain.StepFailed,
			Message:    err.Error(),
			StartedAt:  &startedAt,
			FinishedAt: &now,
		},
		Event: &taskdomain.Event{
			TaskID:    r.task.ID,
			StepID:    step.ID,
			EventType: taskdomain.EventError,
			Content:   err.Error(),
		},
	})
	return agentcore.ReportedTaskError{Err: err}
}

func (r *mysqlInstallRunner) markStepStarted(step taskdomain.DispatchStep) time.Time {
	now := time.Now().UTC()
	if r.stepStarts == nil {
		r.stepStarts = make(map[string]time.Time)
	}
	if startedAt, ok := r.stepStarts[step.ID]; ok {
		return startedAt
	}
	r.stepStarts[step.ID] = now
	return now
}

func (r *mysqlInstallRunner) stepStartedAt(step taskdomain.DispatchStep, fallback time.Time) time.Time {
	if r.stepStarts == nil {
		return fallback
	}
	if startedAt, ok := r.stepStarts[step.ID]; ok {
		return startedAt
	}
	return fallback
}

func progress(stepNo, total int) int {
	if total <= 0 {
		return 0
	}
	if stepNo >= total {
		return 100
	}
	return int(float64(stepNo-1) / float64(total) * 100)
}

func shellEscape(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
}

func mysqlSQLEscape(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `'`, `''`)
}

func mysqlCommandOutput(stdout, stderr string) string {
	parts := make([]string, 0, 2)
	if text := strings.TrimSpace(stdout); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(stderr); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}
