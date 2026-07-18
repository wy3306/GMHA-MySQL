package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	agenthandler "gmha/internal/agent/handler"
	"gmha/internal/agent/mysqlcheck"
	collectdomain "gmha/internal/collect"
	agentdomain "gmha/internal/domain/agent"
	clusterdomain "gmha/internal/domain/cluster"
	credentialdomain "gmha/internal/domain/credential"
	dynamicdomain "gmha/internal/domain/dynamic"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
	agentusecase "gmha/internal/usecase/agent"
	machineusecase "gmha/internal/usecase/machine"
	taskusecase "gmha/internal/usecase/task"
)

// MachineService 是机器管理服务，负责机器纳管、集群管理、SSH 凭据管理、
// 机器信息采集、静态信息采集和动态指标查询等。
type MachineService struct {
	onboard      *machineusecase.OnboardUsecase
	machineRepo  machinedomain.Repository
	clusterRepo  clusterdomain.Repository
	credRepo     credentialdomain.Repository
	infoRepo     MachineInfoRepository
	staticRepo   StaticInfoRepository
	recoveryRepo machineDataCleaner
	sshClient    machineusecase.SSHClient
	agentSvc     *AgentService
	taskSvc      *TaskService
}

// MachineInfoRepository 定义了机器采集信息的持久化接口。
type MachineInfoRepository interface {
	Save(ctx context.Context, item collectdomain.MachineInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.MachineInfo, bool, error)
}

// StaticInfoRepository 定义了静态信息的持久化接口。
type StaticInfoRepository interface {
	Save(ctx context.Context, item collectdomain.StaticInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.StaticInfo, bool, error)
}

type machineDataCleaner interface {
	DeleteByMachineID(ctx context.Context, machineID string) error
}

type mysqlInstanceMachineCleaner interface {
	List(ctx context.Context) ([]mysqlapp.Instance, error)
	DeleteByMachineID(ctx context.Context, machineID string) error
}

// ClusterCleanupMachineResult 是集群清理时单台机器的清理结果。
type ClusterCleanupMachineResult struct {
	MachineID          string   `json:"machine_id"`
	Name               string   `json:"name"`
	IP                 string   `json:"ip"`
	MySQLUninstallTask []string `json:"mysql_uninstall_tasks,omitempty"`
	MySQLPorts         []int    `json:"mysql_ports,omitempty"`
	AgentUninstalled   bool     `json:"agent_uninstalled"`
	LocalCleaned       bool     `json:"local_cleaned"`
	Error              string   `json:"error,omitempty"`
}

// ClusterCleanupResult 是集群清理的总结果，包含每台机器的清理详情。
type ClusterCleanupResult struct {
	Cluster string                        `json:"cluster"`
	Items   []ClusterCleanupMachineResult `json:"items"`
	Failed  int                           `json:"failed"`
}

// DeleteMachineOptions 控制删除机器前是否同步清理目标机上的运行资源。
type DeleteMachineOptions struct {
	DeleteMySQL  bool   `json:"delete_mysql"`
	DeleteAgent  bool   `json:"delete_agent"`
	DetachOnly   bool   `json:"detach_only"`
	ParentTaskID string `json:"-"`
}

// DeleteMachineResult 描述机器删除过程中完成的远端与本地清理操作。
type DeleteMachineResult struct {
	MachineID           string   `json:"machine_id"`
	MySQLPorts          []int    `json:"mysql_ports,omitempty"`
	MySQLUninstallTask  []string `json:"mysql_uninstall_tasks,omitempty"`
	AgentUninstalled    bool     `json:"agent_uninstalled"`
	AgentVerified       bool     `json:"agent_verified"`
	MySQLVerified       bool     `json:"mysql_verified"`
	LocalCleaned        bool     `json:"local_cleaned"`
	DetachedOnly        bool     `json:"detached_only"`
	MySQLSSHPorts       []int    `json:"mysql_ssh_ports,omitempty"`
	MySQLResidues       []string `json:"mysql_residues,omitempty"`
	MySQLCleanupChannel string   `json:"mysql_cleanup_channel,omitempty"`
	MySQLCleanupTaskID  string   `json:"mysql_cleanup_task_id,omitempty"`
	RelatedTaskIDs      []string `json:"task_ids,omitempty"`
}

type DeleteMachinesItem struct {
	MachineID string              `json:"machine_id"`
	Result    DeleteMachineResult `json:"result"`
	Error     string              `json:"error,omitempty"`
}

type DeleteMachinesResult struct {
	TaskID    string               `json:"task_id"`
	Requested int                  `json:"requested"`
	Succeeded int                  `json:"succeeded"`
	Failed    int                  `json:"failed"`
	Items     []DeleteMachinesItem `json:"items"`
}

// DeleteMachinePrecheck 是删除弹窗使用的只读实机检查结果。平台登记记录与
// 目标机真实状态分别返回，避免把“未登记”错误解释成“没有安装”。
type DeleteMachinePrecheck struct {
	MachineID            string   `json:"machine_id"`
	RegisteredMySQLPorts []int    `json:"registered_mysql_ports"`
	RemoteChecked        bool     `json:"remote_checked"`
	SSHReachable         bool     `json:"ssh_reachable"`
	MySQLDetected        bool     `json:"mysql_detected"`
	MySQLResidues        []string `json:"mysql_residues,omitempty"`
	ProbeChannel         string   `json:"probe_channel,omitempty"`
	ProbeTaskID          string   `json:"probe_task_id,omitempty"`
	Warning              string   `json:"warning,omitempty"`
}

// SSHCredentialView 是 SSH 凭据的展示视图。
type SSHCredentialView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SSHUser   string `json:"ssh_user"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// OnboardPrecheckReport 是写入 Manager 前对目标机器执行的非破坏性检查报告。
type OnboardPrecheckReport struct {
	SSHReachable  bool     `json:"ssh_reachable"`
	RemoteCommand bool     `json:"remote_command"`
	Identity      string   `json:"identity"`
	OS            string   `json:"os"`
	SystemdReady  bool     `json:"systemd_ready"`
	Disk          string   `json:"disk"`
	AgentDetected bool     `json:"agent_detected"`
	MySQLDetected bool     `json:"mysql_detected"`
	MySQLResidues []string `json:"mysql_residues,omitempty"`
	Warning       string   `json:"warning,omitempty"`
}

const mysqlResidueMarker = "__GMHA_MYSQL_RESIDUE__"

func mysqlResidueDetectionCommand() string {
	return `found=0; configs="/etc/my.cnf /etc/mysql/my.cnf"; emit(){ echo '` + mysqlResidueMarker + `'"$1"; found=1; }; for unit in $(systemctl list-unit-files --type=service --no-legend 'mysql*.service' 'mysqld*.service' 'mariadb*.service' 2>/dev/null | awk '{print $1}'); do emit systemd:$unit; fragment=$(systemctl show -p FragmentPath --value "$unit" 2>/dev/null || true); [ -n "$fragment" ] && emit path:$fragment; exec_line=$(systemctl show -p ExecStart --value "$unit" 2>/dev/null || true); cfg=$(printf '%s' "$exec_line" | sed -n 's/.*--defaults-file=\([^ ;}]*\).*/\1/p'); [ -n "$cfg" ] && configs="$configs $cfg"; done; for pid in $(pgrep -x mysqld 2>/dev/null || true); do cmd=$(tr '\000' ' ' < /proc/$pid/cmdline 2>/dev/null || true); emit process:mysqld:$pid:$cmd; cfg=$(printf '%s' "$cmd" | sed -n 's/.*--defaults-file=\([^ ]*\).*/\1/p'); [ -n "$cfg" ] && configs="$configs $cfg"; done; for cfg in $configs; do if [ -f "$cfg" ]; then emit config:$cfg; awk -F= '/^[[:space:]]*(basedir|datadir|log-bin|innodb_log_group_home_dir|innodb_undo_directory|tmpdir)[[:space:]]*=/{key=$1; value=$2; gsub(/^[[:space:]]+|[[:space:]]+$/, "", key); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value); if(value!="") print "` + mysqlResidueMarker + `config-path:" key ":" value}' "$cfg" 2>/dev/null || true; found=1; fi; done; for path in /etc/systemd/system/mysql*.service /etc/systemd/system/mysqld*.service /usr/lib/systemd/system/mysql*.service /usr/lib/systemd/system/mysqld*.service /lib/systemd/system/mysql*.service /lib/systemd/system/mysqld*.service /etc/my.cnf /etc/mysql /usr/local/mysql /data/3306; do if [ -e "$path" ] || [ -L "$path" ]; then emit path:$path; fi; done; if command -v mysqld >/dev/null 2>&1; then emit binary:$(command -v mysqld); fi; [ "$found" = 1 ] && echo MYSQL_PRESENT || echo MYSQL_ABSENT`
}

func parseMySQLResidues(output string) []string {
	var residues []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		if index := strings.Index(line, mysqlResidueMarker); index >= 0 {
			if value := strings.TrimSpace(line[index+len(mysqlResidueMarker):]); value != "" {
				if _, exists := seen[value]; exists {
					continue
				}
				seen[value] = struct{}{}
				residues = append(residues, value)
			}
		}
	}
	return residues
}

func mysqlResidueCleanupCommand() string {
	return `set -eu; stamp=$(date +%Y%m%d%H%M%S); backup=/var/lib/gmha-cleanup-backup/mysql-$stamp; paths=/tmp/gmha-mysql-cleanup-paths-$stamp; : > "$paths"; mkdir -p "$backup"; configs="/etc/my.cnf /etc/mysql/my.cnf"; for pid in $(pgrep -x mysqld 2>/dev/null || true); do cmd=$(tr '\000' ' ' < /proc/$pid/cmdline 2>/dev/null || true); cfg=$(printf '%s' "$cmd" | sed -n 's/.*--defaults-file=\([^ ]*\).*/\1/p'); [ -n "$cfg" ] && configs="$configs $cfg"; done; for unit in $(systemctl list-unit-files --type=service --no-legend 'mysql*.service' 'mysqld*.service' 'mariadb*.service' 2>/dev/null | awk '{print $1}'); do exec_line=$(systemctl show -p ExecStart --value "$unit" 2>/dev/null || true); cfg=$(printf '%s' "$exec_line" | sed -n 's/.*--defaults-file=\([^ ;}]*\).*/\1/p'); [ -n "$cfg" ] && configs="$configs $cfg"; systemctl disable --now "$unit" 2>/dev/null || true; done; for service in mysql mysqld mariadb; do systemctl disable --now "$service" 2>/dev/null || true; done; for cfg in $configs; do if [ -f "$cfg" ]; then printf '%s\n' "$cfg" "$(dirname "$cfg")" >> "$paths"; awk -F= '/^[[:space:]]*(basedir|datadir|log-bin|innodb_log_group_home_dir|innodb_undo_directory|tmpdir)[[:space:]]*=/{value=$2; gsub(/^[[:space:]]+|[[:space:]]+$/, "", value); if(substr(value,1,1)=="/") print value}' "$cfg" >> "$paths" 2>/dev/null || true; fi; done; pgrep -x mysqld >/dev/null 2>&1 && pkill -TERM -x mysqld 2>/dev/null || true; sleep 1; pgrep -x mysqld >/dev/null 2>&1 && pkill -KILL -x mysqld 2>/dev/null || true; for path in /etc/systemd/system/mysql*.service /etc/systemd/system/mysqld*.service /usr/lib/systemd/system/mysql*.service /usr/lib/systemd/system/mysqld*.service /lib/systemd/system/mysql*.service /lib/systemd/system/mysqld*.service /etc/my.cnf /etc/mysql /usr/local/mysql /data/3306 $(sort -u "$paths"); do case "$path" in /|/etc|/usr|/usr/local|/var|/var/lib|/data|/tmp|/home|/opt|/srv|'') continue;; esac; if [ -e "$path" ] || [ -L "$path" ]; then name=$(echo "$path" | sed 's#^/##;s#/#__#g'); target="$backup/$name"; [ -e "$target" ] && target="$target-$(date +%s%N)"; mv "$path" "$target"; fi; done; rm -f "$paths"; systemctl daemon-reload; systemctl reset-failed 2>/dev/null || true`
}

// ClusterView 是集群的展示视图，包含集群名称、描述和所属机器列表。
type ClusterView struct {
	Name        string
	Description string
	Machines    []string
	CreatedAt   string
}

// DynamicMetricsView 是动态指标的展示视图，关联了机器信息和心跳状态。
type DynamicMetricsView struct {
	MachineID       string                       `json:"machine_id"`
	MachineName     string                       `json:"machine_name"`
	MachineIP       string                       `json:"machine_ip"`
	HeartbeatState  string                       `json:"heartbeat_state"`
	LastHeartbeatAt string                       `json:"last_heartbeat_at"`
	Metrics         []dynamicdomain.MetricResult `json:"metrics"`
}

// NewMachineService 创建机器管理服务实例。
func NewMachineService(onboard *machineusecase.OnboardUsecase, machineRepo machinedomain.Repository, clusterRepo clusterdomain.Repository, credRepo credentialdomain.Repository, infoRepo MachineInfoRepository, staticRepo StaticInfoRepository, recoveryRepo machineDataCleaner, sshClient machineusecase.SSHClient, agentSvc *AgentService, taskSvc *TaskService) *MachineService {
	return &MachineService{
		onboard:      onboard,
		machineRepo:  machineRepo,
		clusterRepo:  clusterRepo,
		credRepo:     credRepo,
		infoRepo:     infoRepo,
		staticRepo:   staticRepo,
		recoveryRepo: recoveryRepo,
		sshClient:    sshClient,
		agentSvc:     agentSvc,
		taskSvc:      taskSvc,
	}
}

// Onboard 纳管一台新机器，自动解析 SSH 凭据并执行 SSH 连接和信任建立。
func (s *MachineService) Onboard(ctx context.Context, req machineusecase.OnboardMachineRequest) (machineusecase.OnboardMachineResponse, error) {
	credentialSelector := strings.TrimSpace(req.CredentialID)
	if credentialSelector == "" {
		credentialSelector = strings.TrimSpace(req.CredentialName)
	}
	if credentialSelector != "" {
		cred, ok, err := s.resolveCredential(ctx, credentialSelector)
		if err != nil {
			return machineusecase.OnboardMachineResponse{}, err
		}
		if !ok {
			return machineusecase.OnboardMachineResponse{}, errors.New("ssh credential not found")
		}
		if strings.TrimSpace(req.SSHUser) != "" && strings.TrimSpace(req.SSHUser) != cred.SSHUser {
			return machineusecase.OnboardMachineResponse{}, errors.New("ssh_user does not match selected credential")
		}
		req.SSHUser = cred.SSHUser
		if strings.TrimSpace(req.SSHPassword) == "" {
			req.SSHPassword = cred.SSHPassword
		}
		if strings.TrimSpace(req.SSHPrivateKey) == "" {
			req.SSHPrivateKey = cred.PrivateKey
			req.SSHPassphrase = cred.Passphrase
		}
		req.CredentialID = cred.ID
	}
	resp, err := s.onboard.Execute(ctx, req)
	if err != nil {
		return machineusecase.OnboardMachineResponse{}, err
	}
	if err := s.adoptPreservedComponents(ctx, req, resp.ID); err != nil {
		if req.PreserveAgent && s.agentSvc != nil {
			_, repairErr := s.agentSvc.RetryInstallByIP(ctx, agentusecase.InstallAgentRequest{
				IP: req.IP, SSHUser: req.SSHUser, SSHPassword: req.SSHPassword,
				SSHPrivateKey: req.SSHPrivateKey, SSHPassphrase: req.SSHPassphrase,
			})
			if repairErr == nil {
				return resp, nil
			}
			return resp, fmt.Errorf("机器已登记；保留 Agent 接管失败，自动使用当前 Agent 修复也失败: 接管错误=%v；修复错误=%w", err, repairErr)
		}
		return resp, fmt.Errorf("机器已登记，但保留组件接管失败: %w", err)
	}
	return resp, nil
}

// adoptPreservedComponents 重新登记目标机上选择保留的 Agent 与 MySQL，整个过程不会卸载或删除远端文件。
func (s *MachineService) adoptPreservedComponents(ctx context.Context, req machineusecase.OnboardMachineRequest, machineID string) error {
	if !req.PreserveAgent && !req.PreserveMySQL {
		return nil
	}
	machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("registered machine not found")
	}
	auth := machinedomain.SSHAuth{User: req.SSHUser, Password: req.SSHPassword, PrivateKey: req.SSHPrivateKey, Passphrase: req.SSHPassphrase}
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	installDir := agentdomain.ResolveInstallDir(machine.SSHUser, machine.AgentInstallDir)

	runner, outputSupported := s.sshClient.(interface {
		RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error)
	})
	if req.PreserveAgent && outputSupported {
		output, _ := runner.RunOutput(ctx, endpoint, auth, `systemctl show -p ExecStart --value gmha-agent 2>/dev/null | grep -oE '/[^ ;]+/agentd' | head -1 | xargs dirname`)
		if detected := strings.TrimSpace(string(output)); strings.HasPrefix(detected, "/") && !strings.ContainsAny(detected, "|\r\n") {
			installDir = detected
		}
	}
	if req.PreserveMySQL {
		if !outputSupported {
			return errors.New("SSH client does not support existing MySQL discovery")
		}
		output, runErr := runner.RunOutput(ctx, endpoint, auth, `set +e; for p in /home/gmha/agent/mysql-heartbeat.json /opt/gmha/mysql-heartbeat.json /root/gmha/agent/mysql-heartbeat.json /home/*/gmha/agent/mysql-heartbeat.json; do if test -f "$p"; then printf '__GMHA_MYSQL_CONFIG__%s\n' "$p"; cat "$p"; exit 0; fi; done; exit 0`)
		if runErr != nil {
			return fmt.Errorf("read preserved MySQL config: %w", runErr)
		}
		config, configPath, parseErr := parsePreservedMySQLConfig(output)
		if parseErr != nil {
			return parseErr
		}
		if configPath != "" {
			installDir = strings.TrimSuffix(configPath, "/"+mysqlcheck.DefaultConfigFile)
		}
		if s.taskSvc != nil && s.taskSvc.mysqlInstance != nil {
			for _, instance := range config.Instances {
				if instance.Port <= 0 {
					continue
				}
				if err := s.taskSvc.mysqlInstance.Save(ctx, mysqlapp.Instance{MachineID: machineID, Port: instance.Port, MySQLUser: instance.Username, DataDir: instance.DataDir, BinlogDir: instance.BinlogDir, RedoDir: instance.RedoDir, UndoDir: instance.UndoDir, TmpDir: instance.TmpDir, SystemdUnit: instance.SystemdUnit, SocketPath: instance.Socket, Status: mysqlapp.StatusRunning, UpdatedAt: time.Now().UTC()}); err != nil {
					return fmt.Errorf("adopt MySQL %d: %w", instance.Port, err)
				}
			}
		}
	}
	if req.PreserveAgent {
		if s.agentSvc == nil || s.agentSvc.repo == nil {
			return errors.New("agent service not configured")
		}
		agentVersion := ""
		if outputSupported {
			output, versionErr := runner.RunOutput(ctx, endpoint, auth, shellQuote(strings.TrimSuffix(installDir, "/")+"/agentd")+" --version")
			if versionErr == nil {
				agentVersion, _ = detectAgentVersionOutput(output)
			}
		}
		if _, err := s.agentSvc.repo.Save(ctx, agentdomain.Agent{ID: "agent-" + machineID, MachineID: machineID, InstallDir: installDir, Version: agentVersion, State: agentdomain.StateInstalling}); err != nil {
			return err
		}
		serviceRunner, ok := s.sshClient.(interface {
			Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error
		})
		if !ok {
			return errors.New("SSH client does not support existing Agent restart")
		}
		managerHTTPAddr := ResolveManagerHTTPAddrForTarget(s.agentSvc.managerHTTPAddr, machine.IP)
		managerGRPCAddr := ResolveManagerGRPCAddrForTarget("", s.agentSvc.managerGRPCAddr, machine.IP)
		configPath := strings.TrimSuffix(installDir, "/") + "/agent.yaml"
		updates := [][2]string{
			{"agent_id", "agent-" + machineID},
			{"machine_id", machineID},
			{"machine_ip", machine.IP},
			{"install_dir", installDir},
			{"manager_http_addr", managerHTTPAddr},
			{"manager_grpc_addr", managerGRPCAddr},
		}
		command := "set -eu; config=" + shellQuote(configPath) + "; test -f \"$config\"; cp -f \"$config\" \"$config.bak\""
		for _, update := range updates {
			key, value := update[0], update[1]
			replacement := key + ": " + value
			command += "; if grep -q " + shellQuote("^"+key+":") + " \"$config\"; then sed -i " + shellQuote("s|^"+key+":.*$|"+replacement+"|") + " \"$config\"; else printf '%s\\n' " + shellQuote(replacement) + " >> \"$config\"; fi"
		}
		unit := preservedAgentSystemdUnit(installDir)
		command += "; unit=/etc/systemd/system/gmha-agent.service; if test -f \"$unit\"; then cp -f \"$unit\" \"$unit.bak\"; fi; printf '%s' " + shellQuote(unit) + " > \"$unit\"; chmod 0644 \"$unit\"; systemctl daemon-reload; systemctl reset-failed gmha-agent 2>/dev/null || true; systemctl enable gmha-agent; systemctl restart gmha-agent"
		startedAt := time.Now().UTC()
		restartErr := serviceRunner.Run(ctx, endpoint, auth, command)
		if err := s.confirmPreservedAgentOnline(ctx, runner, endpoint, auth, machineID, startedAt, restartErr); err != nil {
			_ = s.agentSvc.repo.UpdateState(ctx, machineID, agentdomain.StateError, err.Error())
			return err
		}
		_ = s.agentSvc.repo.UpdateState(ctx, machineID, agentdomain.StateOnline, "")
		_ = s.machineRepo.UpdateStatus(ctx, machineID, machinedomain.StatusAgentOnline, "")
	}
	return nil
}

func preservedAgentSystemdUnit(installDir string) string {
	dir := strings.TrimSuffix(installDir, "/")
	return fmt.Sprintf(`[Unit]
Description=GMHA Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s/agentd --config %s/agent.yaml
Restart=always
RestartSec=5s
TimeoutStopSec=15s
KillMode=mixed
SendSIGKILL=yes

[Install]
WantedBy=multi-user.target
`, dir, dir, dir)
}

func (s *MachineService) confirmPreservedAgentOnline(ctx context.Context, runner interface {
	RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error)
}, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth, machineID string, startedAt time.Time, restartErr error) error {
	if s.agentSvc != nil && s.agentSvc.heartbeat != nil {
		if err := s.agentSvc.heartbeat.WaitForFreshHeartbeat(ctx, machineID, startedAt, 20*time.Second); err == nil {
			return nil
		}
	}
	output, statusErr := runner.RunOutput(ctx, endpoint, auth, `state=$(systemctl is-active gmha-agent 2>/dev/null || true); result=$(systemctl show gmha-agent -p Result --value 2>/dev/null || true); code=$(systemctl show gmha-agent -p ExecMainStatus --value 2>/dev/null || true); logs=$(journalctl -u gmha-agent -n 8 --no-pager -o cat 2>/dev/null | tail -c 800 | tr '\n' ';' || true); printf 'state=%s result=%s exit=%s journal=%s' "$state" "$result" "$code" "$logs"`)
	status := strings.TrimSpace(string(output))
	if statusErr == nil && strings.Contains(status, "state=active") {
		return nil
	}
	if status == "" {
		status = "service status unavailable"
	}
	if restartErr != nil {
		return fmt.Errorf("保留的 Agent 重启后未上线（%s；重启命令错误：%v）", status, restartErr)
	}
	return fmt.Errorf("保留的 Agent 重启后未上线（%s）", status)
}

func parsePreservedMySQLConfig(output []byte) (mysqlcheck.Config, string, error) {
	const marker = "__GMHA_MYSQL_CONFIG__"
	text := strings.TrimSpace(string(output))
	index := strings.Index(text, marker)
	if index < 0 {
		return mysqlcheck.Config{}, "", nil
	}
	text = text[index+len(marker):]
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		return mysqlcheck.Config{}, "", errors.New("preserved MySQL config payload is incomplete")
	}
	path := strings.TrimSpace(text[:lineEnd])
	var config mysqlcheck.Config
	if err := json.Unmarshal([]byte(strings.TrimSpace(text[lineEnd+1:])), &config); err != nil {
		return mysqlcheck.Config{}, path, fmt.Errorf("parse preserved MySQL config: %w", err)
	}
	return config, path, nil
}

// PrecheckOnboard 验证凭证和目标机器的基础部署条件，不写入机器记录、不修改远端状态。
func (s *MachineService) PrecheckOnboard(ctx context.Context, req machineusecase.OnboardMachineRequest) (OnboardPrecheckReport, error) {
	if err := req.Validate(); err != nil {
		return OnboardPrecheckReport{}, err
	}
	if selector := strings.TrimSpace(req.CredentialID); selector != "" {
		cred, ok, err := s.resolveCredential(ctx, selector)
		if err != nil {
			return OnboardPrecheckReport{}, err
		}
		if !ok {
			return OnboardPrecheckReport{}, errors.New("ssh credential not found")
		}
		req.SSHUser, req.SSHPassword, req.SSHPrivateKey, req.SSHPassphrase = cred.SSHUser, cred.SSHPassword, cred.PrivateKey, cred.Passphrase
	}
	endpoint := machinedomain.Endpoint{IP: req.IP, SSHPort: req.SSHPort}
	auth := machinedomain.SSHAuth{User: req.SSHUser, Password: req.SSHPassword, PrivateKey: req.SSHPrivateKey, Passphrase: req.SSHPassphrase}
	if err := s.sshClient.TestConnection(ctx, endpoint, auth); err != nil {
		return OnboardPrecheckReport{}, fmt.Errorf("SSH authentication failed: %w", err)
	}
	report := OnboardPrecheckReport{SSHReachable: true}
	runner, ok := s.sshClient.(interface {
		RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error)
	})
	if !ok {
		report.Warning = "当前 SSH 客户端不支持远程预检查"
		return report, nil
	}
	command := `id; uname -srm; test -d /etc/systemd/system && test -w /etc/systemd/system && echo SYSTEMD_READY || echo SYSTEMD_NOT_WRITABLE; df -h / | tail -1; if systemctl cat gmha-agent >/dev/null 2>&1 || test -f /etc/systemd/system/gmha-agent.service || test -d /opt/gmha || test -d /home/gmha/agent || test -d /root/gmha/agent || find /home -maxdepth 3 -type d -path '*/gmha/agent' -print -quit 2>/dev/null | grep -q .; then echo GMHA_AGENT_PRESENT; else echo GMHA_AGENT_ABSENT; fi; ` + mysqlResidueDetectionCommand()
	output, err := runner.RunOutput(ctx, endpoint, auth, command)
	if err != nil {
		report.Warning = fmt.Sprintf("SSH 已认证，但远程命令不可执行: %v", err)
		return report, nil
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		report.Warning = "SSH 已认证，但远端未返回任何命令输出；该账号可能被限制执行 shell 命令，已阻止继续纳管"
		return report, nil
	}
	report.RemoteCommand = true
	lines := strings.Split(text, "\n")
	if len(lines) > 0 {
		report.Identity = lines[0]
	}
	if len(lines) > 1 {
		report.OS = lines[1]
	}
	report.SystemdReady = strings.Contains(text, "SYSTEMD_READY")
	for _, line := range lines {
		if strings.Contains(line, "%") && strings.Contains(line, "/") {
			report.Disk = line
			break
		}
	}
	report.AgentDetected = strings.Contains(text, "GMHA_AGENT_PRESENT")
	report.MySQLResidues = parseMySQLResidues(text)
	report.MySQLDetected = strings.Contains(text, "MYSQL_PRESENT") || len(report.MySQLResidues) > 0
	if !report.SystemdReady {
		report.Warning = "当前 SSH 用户无法写入 systemd 服务目录，Agent 安装需要 root 或具备 sudo 权限的用户"
	}
	return report, nil
}

// CleanupOnboardTarget 清理旧 Agent，并把 MySQL 残留归档后移出安装路径。
func (s *MachineService) CleanupOnboardTarget(ctx context.Context, req machineusecase.OnboardMachineRequest, confirmPhrase string) error {
	if strings.TrimSpace(confirmPhrase) != "CLEAN "+strings.TrimSpace(req.IP) {
		return errors.New("invalid cleanup confirmation phrase")
	}
	if selector := strings.TrimSpace(req.CredentialID); selector != "" {
		cred, ok, err := s.resolveCredential(ctx, selector)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("ssh credential not found")
		}
		req.SSHUser, req.SSHPassword, req.SSHPrivateKey, req.SSHPassphrase = cred.SSHUser, cred.SSHPassword, cred.PrivateKey, cred.Passphrase
	}
	runner, ok := s.sshClient.(interface {
		Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error
	})
	if !ok {
		return errors.New("SSH client does not support remote cleanup")
	}
	auth := machinedomain.SSHAuth{User: req.SSHUser, Password: req.SSHPassword, PrivateKey: req.SSHPrivateKey, Passphrase: req.SSHPassphrase}
	cmd := `set -eu; systemctl disable --now gmha-agent 2>/dev/null || true; rm -f /etc/systemd/system/gmha-agent.service; rm -rf /opt/gmha /home/*/agent; ` + mysqlResidueCleanupCommand()
	return runner.Run(ctx, machinedomain.Endpoint{IP: req.IP, SSHPort: req.SSHPort}, auth, cmd)
}

// CreateSSHCredential 创建 SSH 凭据。
func (s *MachineService) CreateSSHCredential(ctx context.Context, name, sshUser, credentialType, sshPassword, privateKey, passphrase string) (SSHCredentialView, error) {
	if s.credRepo == nil {
		return SSHCredentialView{}, errors.New("ssh credential repository not configured")
	}
	name = strings.TrimSpace(name)
	sshUser = strings.TrimSpace(sshUser)
	if name == "" {
		return SSHCredentialView{}, errors.New("credential name is required")
	}
	if sshUser == "" {
		return SSHCredentialView{}, errors.New("ssh_user is required")
	}
	credentialType = strings.TrimSpace(credentialType)
	if credentialType == "" {
		credentialType = string(credentialdomain.TypePassword)
	}
	if credentialType == string(credentialdomain.TypePassword) && strings.TrimSpace(sshPassword) == "" {
		return SSHCredentialView{}, errors.New("ssh_password is required")
	}
	if credentialType == string(credentialdomain.TypePrivateKey) && strings.TrimSpace(privateKey) == "" {
		return SSHCredentialView{}, errors.New("private_key is required")
	}
	if credentialType != string(credentialdomain.TypePassword) && credentialType != string(credentialdomain.TypePrivateKey) {
		return SSHCredentialView{}, errors.New("credential type must be password or private_key")
	}
	item, err := s.credRepo.Save(ctx, credentialdomain.SSHCredential{
		Name:        name,
		SSHUser:     sshUser,
		Type:        credentialdomain.Type(credentialType),
		SSHPassword: sshPassword,
		PrivateKey:  privateKey,
		Passphrase:  passphrase,
	})
	if err != nil {
		return SSHCredentialView{}, err
	}
	return credentialView(item), nil
}

// AssignCredential 将一条凭据分配给多台已纳管机器，不会触发远端连接或安装操作。
func (s *MachineService) AssignCredential(ctx context.Context, selector string, machineIDs []string) error {
	credential, ok, err := s.resolveCredential(ctx, selector)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("ssh credential not found")
	}
	if len(machineIDs) == 0 {
		return errors.New("at least one machine is required")
	}
	for _, machineID := range machineIDs {
		machine, found, err := s.machineRepo.GetByID(ctx, strings.TrimSpace(machineID))
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("machine %s not found", machineID)
		}
		machine.CredentialID = credential.ID
		machine.SSHUser = credential.SSHUser
		if err := s.machineRepo.UpdateBasics(ctx, machine); err != nil {
			return err
		}
	}
	return nil
}

// ListSSHCredentials 列出所有 SSH 凭据。
func (s *MachineService) ListSSHCredentials(ctx context.Context) ([]SSHCredentialView, error) {
	if s.credRepo == nil {
		return nil, errors.New("ssh credential repository not configured")
	}
	items, err := s.credRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SSHCredentialView, 0, len(items))
	for _, item := range items {
		out = append(out, credentialView(item))
	}
	return out, nil
}

// DeleteSSHCredential 按 ID 或名称删除 SSH 凭据。
func (s *MachineService) DeleteSSHCredential(ctx context.Context, selector string) error {
	if s.credRepo == nil {
		return errors.New("ssh credential repository not configured")
	}
	item, ok, err := s.resolveCredential(ctx, selector)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("ssh credential not found")
	}
	return s.credRepo.Delete(ctx, item.ID)
}

// ListMachines 返回所有机器列表。
func (s *MachineService) ListMachines(ctx context.Context) ([]machinedomain.Machine, error) {
	items, err := s.machineRepo.List(ctx)
	if err != nil || s.agentSvc == nil || s.agentSvc.heartbeat == nil {
		return items, err
	}
	heartbeats := make(map[string]HeartbeatView)
	for _, heartbeat := range s.agentSvc.heartbeat.Snapshot() {
		heartbeats[heartbeat.MachineID] = heartbeat
	}
	for index := range items {
		heartbeat, ok := heartbeats[items[index].ID]
		if !ok {
			continue
		}
		if status, lastError, authoritative := machineStatusFromHeartbeat(heartbeat.CurrentState, heartbeat.LastErrorSummary); authoritative {
			items[index].Status = status
			items[index].LastError = lastError
		}
	}
	return items, nil
}

// GetMachine 返回单台机器的基础信息，用于 Web 详情视图。
func (s *MachineService) GetMachine(ctx context.Context, machineID string) (machinedomain.Machine, bool, error) {
	return s.machineRepo.GetByID(ctx, strings.TrimSpace(machineID))
}

// CreateCluster 创建新集群。
func (s *MachineService) CreateCluster(ctx context.Context, name, description string) error {
	return s.clusterRepo.Create(ctx, clusterdomain.Cluster{Name: name, Description: description})
}

// ListClusters 列出所有集群及其关联的机器信息。
func (s *MachineService) ListClusters(ctx context.Context) ([]ClusterView, error) {
	clusters, err := s.clusterRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	byCluster := make(map[string][]string)
	for _, item := range machines {
		if strings.TrimSpace(item.Cluster) == "" {
			continue
		}
		byCluster[item.Cluster] = append(byCluster[item.Cluster], fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	out := make([]ClusterView, 0, len(clusters))
	for _, item := range clusters {
		out = append(out, ClusterView{
			Name:        item.Name,
			Description: item.Description,
			Machines:    byCluster[item.Name],
			CreatedAt:   item.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, nil
}

// CheckSSHTrust 检查目标机器的 SSH 免密连接是否可用。
func (s *MachineService) CheckSSHTrust(ctx context.Context, ip string, sshPort int, sshUser string) (bool, error) {
	return s.sshClient.CheckTrustConnection(ctx, machinedomain.Endpoint{
		IP:      ip,
		SSHPort: sshPort,
	}, machinedomain.SSHAuth{User: sshUser})
}

// UpdateMachine 更新机器的基本信息（名称、IP、SSH 端口、SSH 用户）。
func (s *MachineService) UpdateMachine(ctx context.Context, machineID, name, ip string, sshPort int, sshUser string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(ip) == "" || strings.TrimSpace(sshUser) == "" || sshPort <= 0 {
		return errors.New("name, ip, ssh_port and ssh_user are required")
	}
	_, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	return s.machineRepo.UpdateBasics(ctx, machinedomain.Machine{
		ID:      machineID,
		Name:    strings.TrimSpace(name),
		IP:      strings.TrimSpace(ip),
		SSHPort: sshPort,
		SSHUser: strings.TrimSpace(sshUser),
	})
}

// DeleteMachine 删除机器，会先卸载 Agent，再清理本地关联数据。
func (s *MachineService) DeleteMachine(ctx context.Context, machineID string) error {
	_, err := s.DeleteMachineWithOptions(ctx, machineID, DeleteMachineOptions{DeleteAgent: true})
	return err
}

// DeleteMachinesWithOptions 把一次批量删除建模为一个业务父任务。每台机器
// 各自作为子任务，机器内部产生的 Agent 探测、卸载和清理任务继续挂在其下。
func (s *MachineService) DeleteMachinesWithOptions(ctx context.Context, machineIDs []string, opts DeleteMachineOptions, concurrency int) (DeleteMachinesResult, error) {
	if s.taskSvc == nil {
		return DeleteMachinesResult{}, errors.New("task service not configured")
	}
	seen := make(map[string]struct{}, len(machineIDs))
	ids := make([]string, 0, len(machineIDs))
	for _, value := range machineIDs {
		id := strings.TrimSpace(value)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return DeleteMachinesResult{}, errors.New("machine_ids is required")
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > 10 {
		concurrency = 10
	}

	parent, err := s.taskSvc.CreateBatchTrackingTask(ctx, "machine_batch_delete", "批量删除机器", fmt.Sprintf("%d 台机器", len(ids)))
	if err != nil {
		return DeleteMachinesResult{}, err
	}
	result := DeleteMachinesResult{TaskID: parent.Task.ID, Requested: len(ids), Items: make([]DeleteMachinesItem, len(ids))}
	childIDs := make([]string, len(ids))
	for i, id := range ids {
		target := id
		if machine, ok, lookupErr := s.machineRepo.GetByID(ctx, id); lookupErr == nil && ok && strings.TrimSpace(machine.Name) != "" {
			target = machine.Name
		}
		child, createErr := s.taskSvc.CreateBatchTrackingTask(ctx, "machine_delete", "删除机器 "+target, id)
		if createErr != nil {
			_ = s.taskSvc.FinalizeBatchTrackingTask(context.WithoutCancel(ctx), parent.Task.ID, i, 1)
			return result, createErr
		}
		childIDs[i] = child.Task.ID
		result.Items[i].MachineID = id
	}
	if err := s.taskSvc.AttachChildTasks(ctx, parent.Task.ID, childIDs); err != nil {
		_ = s.taskSvc.FinalizeBatchTrackingTask(context.WithoutCancel(ctx), parent.Task.ID, 0, len(ids))
		return result, err
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	worker := func() {
		defer wg.Done()
		for index := range jobs {
			childOpts := opts
			childOpts.ParentTaskID = childIDs[index]
			itemResult, deleteErr := s.DeleteMachineWithOptions(ctx, ids[index], childOpts)
			failed := 0
			if deleteErr != nil {
				failed = 1
				result.Items[index].Error = deleteErr.Error()
			}
			result.Items[index].Result = itemResult
			if finalizeErr := s.taskSvc.FinalizeBatchTrackingTask(context.WithoutCancel(ctx), childIDs[index], len(itemResult.RelatedTaskIDs), failed); finalizeErr != nil && result.Items[index].Error == "" {
				result.Items[index].Error = finalizeErr.Error()
				failed = 1
			}
			mu.Lock()
			if failed > 0 {
				result.Failed++
			} else {
				result.Succeeded++
			}
			mu.Unlock()
		}
	}
	workers := concurrency
	if workers > len(ids) {
		workers = len(ids)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := s.taskSvc.FinalizeBatchTrackingTask(context.WithoutCancel(ctx), parent.Task.ID, len(childIDs), 0); err != nil {
		return result, err
	}
	return result, nil
}

// PrecheckDeleteMachine 同时读取 Manager 登记记录和目标机真实状态，不修改远端。
func (s *MachineService) PrecheckDeleteMachine(ctx context.Context, machineID string) (DeleteMachinePrecheck, error) {
	return s.precheckDeleteMachine(ctx, machineID, "")
}

func (s *MachineService) precheckDeleteMachine(ctx context.Context, machineID, parentTaskID string) (DeleteMachinePrecheck, error) {
	machine, ok, err := s.machineRepo.GetByID(ctx, strings.TrimSpace(machineID))
	if err != nil {
		return DeleteMachinePrecheck{}, err
	}
	if !ok {
		return DeleteMachinePrecheck{}, errors.New("machine not found")
	}
	report := DeleteMachinePrecheck{MachineID: machine.ID}
	instancesByMachineID, err := s.mysqlInstancesByMachineID(ctx)
	if err != nil {
		return report, err
	}
	for _, instance := range instancesByMachineID[machine.ID] {
		report.RegisteredMySQLPorts = append(report.RegisteredMySQLPorts, instance.Port)
	}
	if s.canUseAgentTask(ctx, machine.ID) {
		output, taskID, probeErr := s.probeMySQLViaAgent(ctx, machine, parentTaskID)
		if probeErr == nil {
			report.RemoteChecked = true
			report.ProbeChannel = "agent"
			report.ProbeTaskID = taskID
			report.MySQLResidues = parseMySQLResidues(output)
			report.MySQLDetected = strings.Contains(output, "MYSQL_PRESENT") || len(report.MySQLResidues) > 0
			auth, authErr := s.machineSSHAuth(ctx, machine)
			if authErr != nil {
				report.Warning = fmt.Sprintf("Agent 通道正常，但无法加载 SSH 凭证：%v。卸载 Agent 必须使用 SSH", authErr)
			} else if sshErr := s.sshClient.TestConnection(ctx, machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}, auth); sshErr != nil {
				report.Warning = deleteSSHPrecheckError(machine, sshErr).Error()
			} else {
				report.SSHReachable = true
			}
			return report, nil
		}
		report.Warning = fmt.Sprintf("Agent 实机探测失败，正在尝试 SSH：%v", probeErr)
	}
	runner, ok := s.sshClient.(interface {
		RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error)
	})
	if !ok {
		report.Warning = "当前 SSH 客户端不支持远程探测；未登记不代表目标机没有 MySQL"
		return report, nil
	}
	auth, err := s.machineSSHAuth(ctx, machine)
	if err != nil {
		report.Warning = fmt.Sprintf("无法加载 SSH 凭证：%v；未登记不代表目标机没有 MySQL", err)
		return report, nil
	}
	output, err := runner.RunOutput(ctx, machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}, auth, mysqlResidueDetectionCommand())
	if err != nil {
		report.Warning = fmt.Sprintf("目标机实机探测失败：%v；无法据此判定 MySQL 不存在", err)
		return report, nil
	}
	report.SSHReachable = true
	report.RemoteChecked = true
	report.ProbeChannel = "ssh"
	text := string(output)
	report.MySQLResidues = parseMySQLResidues(text)
	report.MySQLDetected = strings.Contains(text, "MYSQL_PRESENT") || len(report.MySQLResidues) > 0
	return report, nil
}

func (s *MachineService) probeMySQLViaAgent(ctx context.Context, machine machinedomain.Machine, parentTaskID string) (string, string, error) {
	if s.taskSvc == nil {
		return "", "", errors.New("task service not configured")
	}
	detail, err := s.taskSvc.CreateExecTaskWithOptions(ctx, machine.IP, mysqlResidueDetectionCommand(), ExecTaskOptions{ParentTaskID: parentTaskID, Operation: "machine_delete_precheck", DisplayName: "删除机器前实机探测", StepName: "检查 MySQL 服务、进程、配置与路径"})
	if err != nil {
		return "", "", err
	}
	finished, err := s.taskSvc.WaitForTask(ctx, detail.Task.ID, 30*time.Second)
	if err != nil {
		return "", detail.Task.ID, err
	}
	_ = s.taskSvc.RedactExecTaskCommand(context.Background(), detail.Task.ID)
	var output strings.Builder
	for _, event := range finished.Events {
		if strings.Contains(event.Content, mysqlResidueMarker) || strings.Contains(event.Content, "MYSQL_PRESENT") || strings.Contains(event.Content, "MYSQL_ABSENT") {
			output.WriteString(event.Content)
			output.WriteByte('\n')
		}
	}
	if finished.Task.Status != taskdomain.StatusSuccess {
		return output.String(), detail.Task.ID, fmt.Errorf("Agent probe task failed: %s", emptyTaskError(finished))
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", detail.Task.ID, errors.New("Agent probe returned no MySQL inspection output")
	}
	return output.String(), detail.Task.ID, nil
}

func (s *MachineService) machineSSHAuth(ctx context.Context, machine machinedomain.Machine) (machinedomain.SSHAuth, error) {
	auth := machinedomain.SSHAuth{User: machine.SSHUser}
	if strings.TrimSpace(machine.CredentialID) != "" && s.credRepo != nil {
		credential, found, err := s.resolveCredential(ctx, machine.CredentialID)
		if err != nil {
			return auth, err
		}
		if found {
			auth = machinedomain.SSHAuth{User: credential.SSHUser, Password: credential.SSHPassword, PrivateKey: credential.PrivateKey, Passphrase: credential.Passphrase}
		}
	}
	if strings.TrimSpace(auth.User) == "" {
		auth.User = "root"
	}
	return auth, nil
}

// DeleteMachineWithOptions 按用户选择先卸载 MySQL、再卸载 Agent，最后删除 Manager 本地记录。
// MySQL 必须先于 Agent 清理，因为卸载任务需要在线 Agent 执行 systemd 与数据目录清理。
func (s *MachineService) DeleteMachineWithOptions(ctx context.Context, machineID string, opts DeleteMachineOptions) (DeleteMachineResult, error) {
	if strings.TrimSpace(machineID) == "" {
		return DeleteMachineResult{}, errors.New("machine id is required")
	}
	machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return DeleteMachineResult{}, err
	}
	if !ok {
		return DeleteMachineResult{}, errors.New("machine not found")
	}
	result := DeleteMachineResult{MachineID: machineID}
	if cluster := strings.TrimSpace(machine.Cluster); cluster != "" {
		return result, fmt.Errorf("machine still belongs to cluster %s; remove it from the cluster before deletion", cluster)
	}
	if opts.DetachOnly {
		if opts.DeleteMySQL || opts.DeleteAgent {
			return result, errors.New("detach_only cannot be combined with remote cleanup options")
		}
		if err := s.cleanupMachineLocalData(ctx, machineID, true); err != nil {
			return result, err
		}
		result.LocalCleaned = true
		result.DetachedOnly = true
		return result, nil
	}
	var deleteSSHAuth machinedomain.SSHAuth
	if opts.DeleteAgent || opts.DeleteMySQL {
		if s.sshClient == nil {
			return result, errors.New("卸载前检查失败：Manager 未配置 SSH 客户端")
		}
		deleteSSHAuth, err = s.machineSSHAuth(ctx, machine)
		if err != nil {
			return result, fmt.Errorf("卸载前检查失败：无法加载目标机 SSH 凭证: %w", err)
		}
		endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
		if err := s.sshClient.TestConnection(ctx, endpoint, deleteSSHAuth); err != nil {
			return result, deleteSSHPrecheckError(machine, err)
		}
	}
	if opts.DeleteMySQL {
		if precheck, precheckErr := s.precheckDeleteMachine(ctx, machineID, opts.ParentTaskID); precheckErr == nil {
			result.MySQLResidues = append(result.MySQLResidues, precheck.MySQLResidues...)
			if precheck.ProbeTaskID != "" {
				result.RelatedTaskIDs = append(result.RelatedTaskIDs, precheck.ProbeTaskID)
			}
		}
		instancesByMachineID, err := s.mysqlInstancesByMachineID(ctx)
		if err != nil {
			return result, err
		}
		for _, instance := range instancesByMachineID[machineID] {
			result.MySQLPorts = append(result.MySQLPorts, instance.Port)
			if s.canUseAgentTask(ctx, machineID) {
				detail, err := s.taskSvc.CreateMySQLUninstallTask(ctx, taskusecase.CreateMySQLUninstallTaskRequest{ParentTaskID: opts.ParentTaskID, Machine: machine.IP, Port: instance.Port})
				if err != nil {
					return result, fmt.Errorf("mysql %d uninstall task create failed: %w", instance.Port, err)
				}
				result.MySQLUninstallTask = append(result.MySQLUninstallTask, detail.Task.ID)
				result.RelatedTaskIDs = append(result.RelatedTaskIDs, detail.Task.ID)
				finished, err := s.taskSvc.WaitForTask(ctx, detail.Task.ID, 2*time.Minute)
				if err != nil {
					return result, fmt.Errorf("mysql %d uninstall wait failed: %w", instance.Port, err)
				}
				if finished.Task.Status != "success" {
					return result, fmt.Errorf("mysql %d uninstall failed: %s", instance.Port, emptyTaskError(finished))
				}
				continue
			}
			if err := s.uninstallMySQLViaSSH(ctx, machine, instance); err != nil {
				return result, fmt.Errorf("mysql %d SSH uninstall failed: %w", instance.Port, err)
			}
			result.MySQLSSHPorts = append(result.MySQLSSHPorts, instance.Port)
		}
		channel, taskID, err := s.cleanupMySQLResidues(ctx, machine, opts.ParentTaskID)
		if err != nil {
			return result, fmt.Errorf("MySQL residual cleanup failed: %w", err)
		}
		result.MySQLCleanupChannel = channel
		result.MySQLCleanupTaskID = taskID
		if taskID != "" {
			result.RelatedTaskIDs = append(result.RelatedTaskIDs, taskID)
		}
		if residues, verifyErr := s.verifyMySQLRemovedViaSSH(ctx, machine); verifyErr != nil {
			result.MySQLResidues = residues
			return result, fmt.Errorf("MySQL cleanup verification failed: %w", verifyErr)
		}
		result.MySQLResidues = nil
		result.MySQLVerified = true
	}
	if opts.DeleteAgent && s.agentSvc != nil {
		if _, err := s.agentSvc.UninstallByIPWithAuth(ctx, machine.IP, deleteSSHAuth); err != nil {
			return result, fmt.Errorf("agent uninstall failed: %w", err)
		}
		result.AgentUninstalled = true
		result.AgentVerified = true
	}
	if err := s.cleanupMachineLocalData(ctx, machineID, true); err != nil {
		return result, err
	}
	result.LocalCleaned = true
	return result, nil
}

func deleteSSHPrecheckError(machine machinedomain.Machine, err error) error {
	detail := err.Error()
	hint := "请检查目标机 SSH 服务、端口、防火墙、安全组以及 Manager 到目标网段的路由"
	if strings.Contains(strings.ToLower(detail), "no route to host") {
		hint = "Manager 无法路由到目标主机；请检查目标机是否开机、网段路由、防火墙或安全组"
	} else if strings.Contains(strings.ToLower(detail), "connection refused") {
		hint = "目标端口拒绝连接；请确认 sshd 已启动且 SSH 端口配置正确"
	} else if strings.Contains(strings.ToLower(detail), "permission denied") || strings.Contains(strings.ToLower(detail), "unable to authenticate") {
		hint = "SSH 认证失败；请在机器与凭证页面更新该机器关联的 SSH 凭证"
	}
	return fmt.Errorf("卸载前检查失败：目标机 %s:%d 的 SSH 通道不可用。%s。原始错误：%s", machine.IP, machine.SSHPort, hint, detail)
}

// AssignMachineCluster 将机器分配到指定集群，分配后自动安装 Agent 并采集静态信息。
func (s *MachineService) AssignMachineCluster(ctx context.Context, machineID, clusterName string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return errors.New("cluster name is required")
	}
	_, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	ok, err = s.clusterRepo.Exists(ctx, clusterName)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.machineRepo.AssignCluster(ctx, machineID, clusterName); err != nil {
		return err
	}
	if s.agentSvc != nil {
		if err := s.agentSvc.EnsureInstalledForMachine(ctx, machineID); err != nil {
			return fmt.Errorf("cluster assigned but auto agent install failed: %w", err)
		}
	}
	if s.taskSvc != nil {
		machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("machine not found")
		}
		if _, err := s.RefreshStaticInfo(ctx, machine.IP); err != nil {
			return fmt.Errorf("cluster assigned but static collect failed: %w", err)
		}
	}
	return nil
}

// UnassignMachineCluster 仅清除机器的集群归属，不删除机器、Agent 或 MySQL 数据。
func (s *MachineService) UnassignMachineCluster(ctx context.Context, machineID string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	if _, ok, err := s.machineRepo.GetByID(ctx, machineID); err != nil {
		return err
	} else if !ok {
		return errors.New("machine not found")
	}
	return s.machineRepo.AssignCluster(ctx, machineID, "")
}

// UpdateCluster 更新集群名称和描述，同时更新关联机器的集群引用。
func (s *MachineService) UpdateCluster(ctx context.Context, oldName, newName, description string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return errors.New("old_name and new_name are required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.clusterRepo.Update(ctx, oldName, clusterdomain.Cluster{Name: newName, Description: description}); err != nil {
		return err
	}
	return s.machineRepo.RebindCluster(ctx, oldName, newName)
}

// DeleteCluster 删除集群，会先清除关联机器的集群字段。
func (s *MachineService) DeleteCluster(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("cluster name is required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.machineRepo.ClearCluster(ctx, name); err != nil {
		return err
	}
	return s.clusterRepo.Delete(ctx, name)
}

// CleanupCluster 清理集群，按顺序卸载每台机器上的 MySQL 实例和 Agent，清理本地数据后删除集群。
func (s *MachineService) CleanupCluster(ctx context.Context, name string) (ClusterCleanupResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ClusterCleanupResult{}, errors.New("cluster name is required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, name)
	if err != nil {
		return ClusterCleanupResult{}, err
	}
	if !ok {
		return ClusterCleanupResult{}, errors.New("cluster not found")
	}
	if s.taskSvc == nil {
		return ClusterCleanupResult{}, errors.New("task service not configured")
	}
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return ClusterCleanupResult{}, err
	}
	instancesByMachineID, err := s.mysqlInstancesByMachineID(ctx)
	if err != nil {
		return ClusterCleanupResult{}, err
	}

	result := ClusterCleanupResult{Cluster: name}
	for _, machine := range machines {
		if machine.Cluster != name {
			continue
		}
		item := ClusterCleanupMachineResult{MachineID: machine.ID, Name: machine.Name, IP: machine.IP}
		for _, instance := range instancesByMachineID[machine.ID] {
			item.MySQLPorts = append(item.MySQLPorts, instance.Port)
			detail, err := s.taskSvc.CreateMySQLUninstallTask(ctx, taskusecase.CreateMySQLUninstallTaskRequest{
				Machine: machine.IP,
				Port:    instance.Port,
			})
			if err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载任务创建失败: %v", instance.Port, err))
				continue
			}
			item.MySQLUninstallTask = append(item.MySQLUninstallTask, detail.Task.ID)
			finished, err := s.taskSvc.WaitForTask(ctx, detail.Task.ID, 2*time.Minute)
			if err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载等待失败: %v", instance.Port, err))
				continue
			}
			if finished.Task.Status != "success" {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载失败: %s", instance.Port, emptyTaskError(finished)))
			}
		}
		if item.Error == "" {
			if _, _, err := s.cleanupMySQLResidues(ctx, machine, ""); err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL 残留清理失败: %v", err))
			}
		}
		if item.Error == "" && s.agentSvc != nil {
			if _, err := s.agentSvc.UninstallByIP(ctx, machine.IP); err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("Agent 卸载失败: %v", err))
			} else {
				item.AgentUninstalled = true
			}
		}
		if item.Error == "" {
			if err := s.cleanupMachineLocalData(ctx, machine.ID, false); err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("本地记录清理失败: %v", err))
			} else {
				item.LocalCleaned = true
			}
		}
		if item.Error != "" {
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Items) == 0 {
		return ClusterCleanupResult{}, fmt.Errorf("cluster %s has no machines", name)
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("cluster cleanup failed for %d machine(s)", result.Failed)
	}
	if err := s.machineRepo.ClearCluster(ctx, name); err != nil {
		return result, err
	}
	if err := s.clusterRepo.Delete(ctx, name); err != nil {
		return result, err
	}
	return result, nil
}

func (s *MachineService) mysqlInstancesByMachineID(ctx context.Context) (map[string][]mysqlapp.Instance, error) {
	out := make(map[string][]mysqlapp.Instance)
	if s.taskSvc == nil || s.taskSvc.mysqlInstance == nil {
		return out, nil
	}
	items, err := s.taskSvc.mysqlInstance.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		out[item.MachineID] = append(out[item.MachineID], item)
	}
	return out, nil
}

func (s *MachineService) canUseAgentTask(ctx context.Context, machineID string) bool {
	if s.taskSvc == nil || s.agentSvc == nil || s.agentSvc.repo == nil {
		return false
	}
	agent, ok, err := s.agentSvc.repo.GetByMachineID(ctx, machineID)
	if err != nil || !ok || agent.State != agentdomain.StateOnline {
		return false
	}
	return s.taskSvc.IsAgentConnected(agent.ID)
}

func (s *MachineService) uninstallMySQLViaSSH(ctx context.Context, machine machinedomain.Machine, instance mysqlapp.Instance) error {
	runner, ok := s.sshClient.(interface {
		Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error
	})
	if !ok {
		return errors.New("SSH client does not support remote commands")
	}
	auth := machinedomain.SSHAuth{User: machine.SSHUser}
	if strings.TrimSpace(machine.CredentialID) != "" && s.credRepo != nil {
		credential, found, err := s.resolveCredential(ctx, machine.CredentialID)
		if err != nil {
			return fmt.Errorf("load SSH credential: %w", err)
		}
		if found {
			auth.User = credential.SSHUser
			auth.Password = credential.SSHPassword
			auth.PrivateKey = credential.PrivateKey
			auth.Passphrase = credential.Passphrase
		}
	}
	if strings.TrimSpace(auth.User) == "" {
		auth.User = "root"
	}
	instanceDir := strings.TrimSpace(instance.InstanceDir)
	if instanceDir == "" {
		instanceDir = fmt.Sprintf("/data/%d", instance.Port)
	}
	pathOrDefault := func(value, suffix string) string {
		if strings.TrimSpace(value) != "" {
			return value
		}
		return filepath.Join(instanceDir, suffix)
	}
	baseDir := strings.TrimSpace(instance.BaseDir)
	if baseDir == "" {
		baseDir = "/usr/local/mysql"
	}
	unit := strings.TrimSpace(instance.SystemdUnit)
	if unit == "" {
		unit = "mysqld"
	}
	spec := taskdomain.MySQLUninstallSpec{
		Port:            instance.Port,
		MySQLUser:       instance.MySQLUser,
		InstanceDir:     instanceDir,
		DataDir:         pathOrDefault(instance.DataDir, "data"),
		BinlogDir:       pathOrDefault(instance.BinlogDir, "binlog"),
		RedoDir:         pathOrDefault(instance.RedoDir, "redo"),
		UndoDir:         pathOrDefault(instance.UndoDir, "undo"),
		TmpDir:          pathOrDefault(instance.TmpDir, "tmp"),
		BaseDir:         baseDir,
		PackageName:     instance.PackageName,
		SystemdUnitName: unit,
		MyCnfPath:       pathOrDefault(instance.MyCnfPath, "my.cnf"),
		SocketPath:      instance.SocketPath,
		ExtraPaths: []string{
			"/etc/profile.d/mysql.sh",
			"/etc/security/limits.d/mysql.conf",
			"/etc/sysctl.d/99-gmha-mysql.conf",
		},
	}
	commands, err := agenthandler.BuildMySQLUninstallCommands(spec)
	if err != nil {
		return err
	}
	endpoint := machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}
	for _, step := range commands {
		if err := runner.Run(ctx, endpoint, auth, step.Command); err != nil {
			return fmt.Errorf("%s: %w", step.Title, err)
		}
	}
	return nil
}

func (s *MachineService) cleanupMySQLResiduesViaSSH(ctx context.Context, machine machinedomain.Machine) error {
	runner, ok := s.sshClient.(interface {
		Run(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) error
	})
	if !ok {
		return errors.New("SSH client does not support remote commands")
	}
	auth := machinedomain.SSHAuth{User: machine.SSHUser}
	if strings.TrimSpace(machine.CredentialID) != "" && s.credRepo != nil {
		credential, found, err := s.resolveCredential(ctx, machine.CredentialID)
		if err != nil {
			return err
		}
		if found {
			auth = machinedomain.SSHAuth{User: credential.SSHUser, Password: credential.SSHPassword, PrivateKey: credential.PrivateKey, Passphrase: credential.Passphrase}
		}
	}
	if strings.TrimSpace(auth.User) == "" {
		auth.User = "root"
	}
	return runner.Run(ctx, machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}, auth, mysqlResidueCleanupCommand())
}

func (s *MachineService) cleanupMySQLResidues(ctx context.Context, machine machinedomain.Machine, parentTaskID string) (string, string, error) {
	if s.canUseAgentTask(ctx, machine.ID) {
		detail, err := s.taskSvc.CreateExecTaskWithOptions(ctx, machine.IP, mysqlResidueCleanupCommand(), ExecTaskOptions{ParentTaskID: parentTaskID, Operation: "machine_delete_mysql_cleanup", DisplayName: "删除机器前清理 MySQL", StepName: "停止 MySQL 并清理 systemd、配置和数据路径"})
		if err == nil {
			finished, waitErr := s.taskSvc.WaitForTask(ctx, detail.Task.ID, 2*time.Minute)
			_ = s.taskSvc.RedactExecTaskCommand(context.Background(), detail.Task.ID)
			if waitErr == nil && finished.Task.Status == taskdomain.StatusSuccess {
				return "agent", detail.Task.ID, nil
			}
			if waitErr != nil {
				return "agent", detail.Task.ID, waitErr
			}
			return "agent", detail.Task.ID, fmt.Errorf("Agent cleanup task failed: %s", emptyTaskError(finished))
		}
	}
	if err := s.cleanupMySQLResiduesViaSSH(ctx, machine); err != nil {
		return "ssh", "", err
	}
	return "ssh", "", nil
}

// verifyMySQLRemovedViaSSH performs a fresh inspection after cleanup. Platform
// records must not be deleted while a service, process, configuration, binary,
// or data path is still present on the target machine.
func (s *MachineService) verifyMySQLRemovedViaSSH(ctx context.Context, machine machinedomain.Machine) ([]string, error) {
	runner, ok := s.sshClient.(interface {
		RunOutput(context.Context, machinedomain.Endpoint, machinedomain.SSHAuth, string) ([]byte, error)
	})
	if !ok {
		return nil, errors.New("SSH client does not support post-cleanup inspection")
	}
	auth, err := s.machineSSHAuth(ctx, machine)
	if err != nil {
		return nil, fmt.Errorf("load SSH credential: %w", err)
	}
	output, err := runner.RunOutput(ctx, machinedomain.Endpoint{IP: machine.IP, SSHPort: machine.SSHPort}, auth, mysqlResidueDetectionCommand())
	if err != nil {
		return nil, fmt.Errorf("inspect target after cleanup: %w", err)
	}
	residues := parseMySQLResidues(string(output))
	if strings.Contains(string(output), "MYSQL_PRESENT") || len(residues) > 0 {
		detail := strings.Join(residues, "、")
		if detail == "" {
			detail = "检测命令仍报告 MYSQL_PRESENT"
		}
		return residues, fmt.Errorf("目标机仍存在 MySQL 残留：%s", detail)
	}
	if !strings.Contains(string(output), "MYSQL_ABSENT") {
		return nil, errors.New("复检没有返回明确的 MYSQL_ABSENT 结果")
	}
	return nil, nil
}

func (s *MachineService) cleanupMachineLocalData(ctx context.Context, machineID string, deleteMachine bool) error {
	if s.agentSvc != nil {
		if s.agentSvc.heartbeat != nil {
			if err := s.agentSvc.heartbeat.RemoveMachine(ctx, machineID); err != nil {
				return err
			}
		}
		if s.agentSvc.repo != nil {
			if err := s.agentSvc.repo.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
	}
	if s.taskSvc != nil && s.taskSvc.mysqlInstance != nil {
		if cleaner, ok := s.taskSvc.mysqlInstance.(mysqlInstanceMachineCleaner); ok {
			if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
	}
	if cleaner, ok := s.staticRepo.(machineDataCleaner); ok {
		if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
			return err
		}
	}
	if s.recoveryRepo != nil {
		if err := s.recoveryRepo.DeleteByMachineID(ctx, machineID); err != nil {
			return err
		}
	}
	if deleteMachine {
		if cleaner, ok := s.infoRepo.(machineDataCleaner); ok {
			if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
		// Task history is an audit record. Removing a managed machine must not
		// erase its execution flow, logs, or timestamps from the task center.
		return s.machineRepo.Delete(ctx, machineID)
	}
	return nil
}

func appendCleanupError(current, next string) string {
	if strings.TrimSpace(current) == "" {
		return next
	}
	return current + "; " + next
}

// SaveMachineInfo 保存机器采集信息。
func (s *MachineService) SaveMachineInfo(ctx context.Context, item collectdomain.MachineInfo) error {
	if s.infoRepo == nil {
		return errors.New("machine info repository not configured")
	}
	return s.infoRepo.Save(ctx, item)
}

// GetMachineInfo 获取指定机器的采集信息（按 IP 或名称查找）。
func (s *MachineService) GetMachineInfo(ctx context.Context, machineSelector string) (collectdomain.MachineInfo, error) {
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if !ok {
		return collectdomain.MachineInfo{}, errors.New("machine not found")
	}
	if s.infoRepo == nil {
		return collectdomain.MachineInfo{}, errors.New("machine info repository not configured")
	}
	item, ok, err := s.infoRepo.Get(ctx, machine.ID)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if !ok {
		return collectdomain.MachineInfo{}, errors.New("machine info not found")
	}
	return item, nil
}

// RefreshMachineInfo 触发机器信息采集任务并等待完成后返回结果。
func (s *MachineService) RefreshMachineInfo(ctx context.Context, machineSelector string) (collectdomain.MachineInfo, error) {
	if s.taskSvc == nil {
		return collectdomain.MachineInfo{}, errors.New("task service not configured")
	}
	taskDetail, err := s.taskSvc.CreateCollectMachineInfoTask(ctx, machineSelector)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	finished, err := s.taskSvc.WaitForTask(ctx, taskDetail.Task.ID, 40*time.Second)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if finished.Task.Status != "success" {
		return collectdomain.MachineInfo{}, fmt.Errorf("collect task failed: %s", emptyTaskError(finished))
	}
	return s.GetMachineInfo(ctx, machineSelector)
}

// GetStaticInfo 获取指定机器的静态信息。
func (s *MachineService) GetStaticInfo(ctx context.Context, machineSelector string) (collectdomain.StaticInfo, error) {
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if !ok {
		return collectdomain.StaticInfo{}, errors.New("machine not found")
	}
	if s.staticRepo == nil {
		return collectdomain.StaticInfo{}, errors.New("static info repository not configured")
	}
	item, ok, err := s.staticRepo.Get(ctx, machine.ID)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if !ok {
		return collectdomain.StaticInfo{}, errors.New("static info not found")
	}
	// Older static snapshots do not contain architecture. Merge the machine
	// inventory snapshot so install compatibility can be checked before submit.
	if s.infoRepo != nil {
		if inventory, found, inventoryErr := s.infoRepo.Get(ctx, machine.ID); inventoryErr == nil && found {
			if item.Host.Arch == "" {
				item.Host.Arch = inventory.Arch
			}
			if item.Host.GlibcVersion == "" {
				item.Host.GlibcVersion = inventory.GlibcVersion
			}
		}
	}
	return item, nil
}

// RefreshStaticInfo 触发静态信息采集任务并等待完成后返回结果。
func (s *MachineService) RefreshStaticInfo(ctx context.Context, machineSelector string) (collectdomain.StaticInfo, error) {
	if s.taskSvc == nil {
		return collectdomain.StaticInfo{}, errors.New("task service not configured")
	}
	taskDetail, err := s.taskSvc.CreateCollectStaticInfoTask(ctx, taskusecase.CreateCollectStaticInfoRequest{Machine: machineSelector})
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	finished, err := s.taskSvc.WaitForTask(ctx, taskDetail.Task.ID, 60*time.Second)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if finished.Task.Status != "success" {
		return collectdomain.StaticInfo{}, fmt.Errorf("static collect task failed: %s", emptyTaskError(finished))
	}
	return s.GetStaticInfo(ctx, machineSelector)
}

// GetMachineDynamicMetrics 获取指定机器的主机动态指标。
func (s *MachineService) GetMachineDynamicMetrics(ctx context.Context, machineSelector string) (DynamicMetricsView, error) {
	return s.getDynamicMetrics(ctx, machineSelector, false)
}

// GetMySQLDynamicMetrics 获取指定机器的 MySQL 动态指标。
func (s *MachineService) GetMySQLDynamicMetrics(ctx context.Context, machineSelector string) (DynamicMetricsView, error) {
	return s.getDynamicMetrics(ctx, machineSelector, true)
}

func (s *MachineService) getDynamicMetrics(ctx context.Context, machineSelector string, mysqlOnly bool) (DynamicMetricsView, error) {
	if s.agentSvc == nil {
		return DynamicMetricsView{}, errors.New("agent service not configured")
	}
	machineTarget, mysqlPort := splitMachinePortSelector(machineSelector)
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if !ok && mysqlPort > 0 {
		machine, ok, err = s.resolveMachine(ctx, machineTarget)
	}
	if err != nil {
		return DynamicMetricsView{}, err
	}
	if !ok {
		return DynamicMetricsView{}, errors.New("machine not found")
	}
	agentView, ok, err := s.agentSvc.GetViewByIP(ctx, machine.IP)
	if err != nil {
		return DynamicMetricsView{}, err
	}
	if !ok {
		return DynamicMetricsView{}, errors.New("agent heartbeat not found")
	}
	metrics := make([]dynamicdomain.MetricResult, 0, len(agentView.Metrics))
	for _, item := range agentView.Metrics {
		isMySQL := isMySQLDynamicMetric(item)
		if mysqlOnly && mysqlPort > 0 && !metricMatchesMySQLPort(item, mysqlPort) {
			continue
		}
		if mysqlOnly == isMySQL {
			metrics = append(metrics, item)
		}
	}
	return DynamicMetricsView{
		MachineID:       machine.ID,
		MachineName:     machine.Name,
		MachineIP:       machine.IP,
		HeartbeatState:  agentView.HeartbeatState,
		LastHeartbeatAt: agentView.LastHeartbeatAt,
		Metrics:         metrics,
	}, nil
}

func metricMatchesMySQLPort(item dynamicdomain.MetricResult, port int) bool {
	if port <= 0 {
		return true
	}
	labelPort := strings.TrimSpace(item.Labels["mysql_port"])
	if labelPort == "" {
		return port == 3306
	}
	return labelPort == strconv.Itoa(port)
}

func isMySQLDynamicMetric(item dynamicdomain.MetricResult) bool {
	switch item.Labels["metric_scope"] {
	case "mysql_dynamic":
		return true
	case "machine_dynamic":
		return false
	}
	if strings.HasPrefix(item.Name, "mysql_") {
		return true
	}
	switch item.Category {
	case "mysql", "connection", "variables", "replication", "topology", "performance", "storage":
		return true
	default:
		return false
	}
}

func splitMachinePortSelector(selector string) (string, int) {
	selector = strings.TrimSpace(selector)
	host, portText, ok := strings.Cut(selector, ":")
	if !ok || strings.TrimSpace(host) == "" || strings.TrimSpace(portText) == "" {
		return selector, 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port <= 0 {
		return selector, 0
	}
	return strings.TrimSpace(host), port
}

func (s *MachineService) resolveMachine(ctx context.Context, selector string) (machinedomain.Machine, bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return machinedomain.Machine{}, false, errors.New("machine selector is required")
	}
	if item, ok, err := s.machineRepo.GetByIP(ctx, selector); err != nil {
		return machinedomain.Machine{}, false, err
	} else if ok {
		return item, true, nil
	}
	items, err := s.machineRepo.List(ctx)
	if err != nil {
		return machinedomain.Machine{}, false, err
	}
	for _, item := range items {
		if item.Name == selector {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}

func (s *MachineService) resolveCredential(ctx context.Context, selector string) (credentialdomain.SSHCredential, bool, error) {
	if s.credRepo == nil {
		return credentialdomain.SSHCredential{}, false, errors.New("ssh credential repository not configured")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return credentialdomain.SSHCredential{}, false, errors.New("ssh credential selector is required")
	}
	if item, ok, err := s.credRepo.GetByID(ctx, selector); err != nil {
		return credentialdomain.SSHCredential{}, false, err
	} else if ok {
		return item, true, nil
	}
	return s.credRepo.GetByName(ctx, selector)
}

func credentialView(item credentialdomain.SSHCredential) SSHCredentialView {
	return SSHCredentialView{
		ID:        item.ID,
		Name:      item.Name,
		SSHUser:   item.SSHUser,
		Type:      string(item.Type),
		CreatedAt: item.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		UpdatedAt: item.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
	}
}

func emptyTaskError(detail TaskDetail) string {
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if strings.TrimSpace(detail.Events[i].Content) != "" {
			return detail.Events[i].Content
		}
	}
	return "unknown error"
}
