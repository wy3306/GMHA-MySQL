package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

const (
	mgrProbeMarker = "__GMHA_MGR_MEMBER__"
	mgrJSONMarker  = "__GMHA_MGR_JSON__"
)

type MGRManagementMember struct {
	MachineID        string `json:"machine_id"`
	MachineName      string `json:"machine_name"`
	IP               string `json:"ip"`
	Port             int    `json:"port"`
	ServerUUID       string `json:"server_uuid,omitempty"`
	ServerID         int    `json:"server_id,omitempty"`
	Version          string `json:"version,omitempty"`
	GroupName        string `json:"group_name,omitempty"`
	State            string `json:"state"`
	Role             string `json:"role"`
	ReadOnly         bool   `json:"read_only"`
	SuperReadOnly    bool   `json:"super_read_only"`
	ApplyQueue       int64  `json:"apply_queue"`
	RemoteApplyQueue int64  `json:"remote_apply_queue"`
	Conflicts        int64  `json:"conflicts"`
	Reachable        bool   `json:"reachable"`
	Error            string `json:"error,omitempty"`
}

type MGRManagementStatus struct {
	Cluster           string                `json:"cluster"`
	Architecture      string                `json:"architecture"`
	Available         bool                  `json:"available"`
	GroupName         string                `json:"group_name,omitempty"`
	Healthy           bool                  `json:"healthy"`
	Quorum            bool                  `json:"quorum"`
	MemberCount       int                   `json:"member_count"`
	OnlineMemberCount int                   `json:"online_member_count"`
	PrimaryMachineID  string                `json:"primary_machine_id,omitempty"`
	Members           []MGRManagementMember `json:"members"`
	AdminAPIStatus    any                   `json:"admin_api_status,omitempty"`
	Routers           any                   `json:"routers,omitempty"`
	RouterOptions     any                   `json:"router_options,omitempty"`
	AdminAPIError     string                `json:"admin_api_error,omitempty"`
	TaskIDs           []string              `json:"task_ids,omitempty"`
	CollectedAt       time.Time             `json:"collected_at"`
}

type MGRManagementActionRequest struct {
	Action       string `json:"action"`
	MachineID    string `json:"machine_id,omitempty"`
	Confirmation string `json:"confirmation"`
}

type MGRManagementActionResult struct {
	Action  string              `json:"action"`
	Success bool                `json:"success"`
	Message string              `json:"message"`
	TaskIDs []string            `json:"task_ids,omitempty"`
	Status  MGRManagementStatus `json:"status"`
}

type mgrClusterTarget struct {
	machine  machinedomain.Machine
	instance mysqlapp.Instance
	member   MGRManagementMember
}

func (s *HAService) MGRManagementStatus(ctx context.Context, clusterID string) (MGRManagementStatus, error) {
	status, targets, err := s.collectMGRManagementStatus(ctx, strings.TrimSpace(clusterID))
	if err != nil {
		return status, err
	}
	if !status.Available {
		return status, nil
	}
	seed, ok := mgrOnlineSeed(targets)
	if !ok {
		status.AdminAPIError = "当前没有 ONLINE 成员，AdminAPI 与 Router 元数据暂不可读"
		return status, nil
	}
	username, password := s.architectureManagementAccount(ctx)
	taskID, output, shellErr := s.runMGRTask(ctx, seed.machine, "mgr_status", "读取 MGR 与 Router 状态",
		mysqlShellMGRCommand(username, password, seed.instance.Port, mgrStatusScript()), 2*time.Minute)
	if taskID != "" {
		status.TaskIDs = append(status.TaskIDs, taskID)
	}
	if shellErr != nil {
		status.AdminAPIError = shellErr.Error()
		return status, nil
	}
	var payload struct {
		Status        any `json:"status"`
		Routers       any `json:"routers"`
		RouterOptions any `json:"router_options"`
	}
	if err := parseMGRJSONMarker(output, &payload); err != nil {
		status.AdminAPIError = err.Error()
		return status, nil
	}
	status.AdminAPIStatus = payload.Status
	status.Routers = payload.Routers
	status.RouterOptions = payload.RouterOptions
	return status, nil
}

func (s *HAService) RunMGRManagementAction(ctx context.Context, clusterID string, req MGRManagementActionRequest) (MGRManagementActionResult, error) {
	clusterID = strings.TrimSpace(clusterID)
	req.Action = strings.TrimSpace(req.Action)
	req.MachineID = strings.TrimSpace(req.MachineID)
	if err := validateMGRActionConfirmation(clusterID, req); err != nil {
		return MGRManagementActionResult{}, err
	}

	var result MGRManagementActionResult
	err := s.withMGRManagementLock(ctx, clusterID, req.Action, func(operationCtx context.Context) error {
		status, targets, err := s.collectMGRManagementStatus(operationCtx, clusterID)
		if err != nil {
			return err
		}
		if !status.Available {
			return errors.New("当前集群不是可识别的 MGR 架构")
		}
		username, password := s.architectureManagementAccount(operationCtx)
		seed, script, err := prepareMGRAction(clusterID, req, status, targets, username)
		if err != nil {
			return err
		}
		taskID, _, err := s.runMGRTask(operationCtx, seed.machine, "mgr_"+req.Action, "执行 MGR 专属管理操作",
			mysqlShellMGRCommand(username, password, seed.instance.Port, script), 10*time.Minute)
		if taskID != "" {
			result.TaskIDs = append(result.TaskIDs, taskID)
		}
		if err != nil {
			return err
		}
		refreshed, refreshedTargets, err := s.collectMGRManagementStatus(operationCtx, clusterID)
		if err != nil {
			return err
		}
		if req.Action == "reboot_complete_outage" {
			seed, ok := mgrOnlineSeed(refreshedTargets)
			if !ok {
				return errors.New("MGR complete-outage recovery returned without an ONLINE member")
			}
			verifyID, _, verifyErr := s.runMGRTask(operationCtx, seed.machine, "mgr_verify_outage_recovery", "验证 MGR 全组恢复与数据一致性",
				mgrRecoveredGroupVerifyCommand(username, password, seed.instance.Port, refreshed.GroupName, refreshed.MemberCount), 4*time.Minute)
			if verifyID != "" {
				result.TaskIDs = append(result.TaskIDs, verifyID)
			}
			if verifyErr != nil {
				return verifyErr
			}
			refreshed, _, err = s.collectMGRManagementStatus(operationCtx, clusterID)
			if err != nil {
				return err
			}
		}
		refreshed.TaskIDs = append(refreshed.TaskIDs, result.TaskIDs...)
		result = MGRManagementActionResult{
			Action: req.Action, Success: true, Message: mgrActionSuccessMessage(req.Action),
			TaskIDs: append([]string(nil), result.TaskIDs...), Status: refreshed,
		}
		return nil
	})
	return result, err
}

func (s *HAService) collectMGRManagementStatus(ctx context.Context, clusterID string) (MGRManagementStatus, []mgrClusterTarget, error) {
	status := MGRManagementStatus{
		Cluster: clusterID, Architecture: "standalone", Members: []MGRManagementMember{},
		CollectedAt: time.Now().UTC(),
	}
	if clusterID == "" {
		return status, nil, errors.New("cluster is required")
	}
	if s.tasks == nil || s.machines == nil || s.instances == nil {
		return status, nil, errors.New("MGR 管理需要实例仓库和在线 Agent 任务通道")
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return status, nil, err
	}
	machineByID := make(map[string]machinedomain.Machine)
	for _, machine := range machines {
		if machine.Cluster == clusterID {
			machineByID[machine.ID] = machine
		}
	}
	instances, err := s.instances.List(ctx)
	if err != nil {
		return status, nil, err
	}
	username, password := s.architectureManagementAccount(ctx)
	var targets []mgrClusterTarget
	for _, instance := range instances {
		machine, ok := machineByID[instance.MachineID]
		if !ok {
			continue
		}
		member := MGRManagementMember{
			MachineID: machine.ID, MachineName: machine.Name, IP: machine.IP, Port: instance.Port,
			ServerID: instance.ServerID, State: "UNKNOWN", Role: "UNKNOWN",
		}
		taskID, output, probeErr := s.runMGRTask(ctx, machine, "mgr_member_status", "读取 MGR 成员状态",
			mgrMemberProbeCommand(username, password, instance.Port), 45*time.Second)
		if taskID != "" {
			status.TaskIDs = append(status.TaskIDs, taskID)
		}
		if probeErr != nil {
			member.Error = probeErr.Error()
		} else if parsed, found := parseMGRMemberMarker(output); found {
			parsed.MachineID, parsed.MachineName, parsed.IP, parsed.Port = machine.ID, machine.Name, machine.IP, instance.Port
			member = parsed
		} else {
			member.Reachable = true
		}
		status.Members = append(status.Members, member)
		targets = append(targets, mgrClusterTarget{machine: machine, instance: instance, member: member})
	}
	if len(status.Members) == 0 {
		return status, targets, nil
	}
	consistentGroup, queuesEmpty := true, true
	for index := range targets {
		member := targets[index].member
		if member.GroupName != "" {
			status.Available = true
			status.Architecture = "mgr_router"
			if status.GroupName == "" {
				status.GroupName = member.GroupName
			} else if status.GroupName != member.GroupName {
				consistentGroup = false
			}
		}
		if member.ApplyQueue != 0 || member.RemoteApplyQueue != 0 {
			queuesEmpty = false
		}
		if strings.EqualFold(member.State, "ONLINE") {
			status.OnlineMemberCount++
		}
		if strings.EqualFold(member.Role, "PRIMARY") && strings.EqualFold(member.State, "ONLINE") {
			status.PrimaryMachineID = member.MachineID
		}
	}
	status.MemberCount = len(status.Members)
	status.Quorum = status.Available && status.OnlineMemberCount >= status.MemberCount/2+1
	status.Healthy = status.Quorum && status.OnlineMemberCount == status.MemberCount && status.PrimaryMachineID != "" && consistentGroup && queuesEmpty
	return status, targets, nil
}

func mgrMemberProbeCommand(username, password string, port int) string {
	sql := "SELECT CONCAT(" + sqlLiteral(mgrProbeMarker) + ",CONCAT_WS(CHAR(9)," +
		"@@server_uuid,@@server_id,@@version,COALESCE(@@group_replication_group_name,'')," +
		"COALESCE((SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid LIMIT 1),'OFFLINE')," +
		"COALESCE((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid LIMIT 1),'UNKNOWN')," +
		"@@read_only,@@super_read_only," +
		"COALESCE((SELECT COUNT_TRANSACTIONS_IN_QUEUE FROM performance_schema.replication_group_member_stats WHERE MEMBER_ID=@@server_uuid LIMIT 1),0)," +
		"COALESCE((SELECT COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE FROM performance_schema.replication_group_member_stats WHERE MEMBER_ID=@@server_uuid LIMIT 1),0)," +
		"COALESCE((SELECT COUNT_CONFLICTS_DETECTED FROM performance_schema.replication_group_member_stats WHERE MEMBER_ID=@@server_uuid LIMIT 1),0)))"
	return mysqlMGRClient(username, password, port) + " --batch --raw --skip-column-names --execute=" + shellQuote(sql)
}

func mysqlMGRClient(username, password string, port int) string {
	if port <= 0 {
		port = 3306
	}
	return fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=%s --connect-timeout=5",
		shellQuote(password), port, shellQuote(username))
}

func parseMGRMemberMarker(output string) (MGRManagementMember, bool) {
	index := strings.LastIndex(output, mgrProbeMarker)
	if index < 0 {
		return MGRManagementMember{}, false
	}
	line := strings.SplitN(output[index+len(mgrProbeMarker):], "\n", 2)[0]
	fields := strings.Split(strings.TrimSpace(line), "\t")
	if len(fields) < 11 {
		return MGRManagementMember{}, false
	}
	serverID, _ := strconv.Atoi(fields[1])
	applyQueue, _ := strconv.ParseInt(fields[8], 10, 64)
	remoteQueue, _ := strconv.ParseInt(fields[9], 10, 64)
	conflicts, _ := strconv.ParseInt(fields[10], 10, 64)
	return MGRManagementMember{
		ServerUUID: fields[0], ServerID: serverID, Version: fields[2], GroupName: fields[3],
		State: strings.ToUpper(fields[4]), Role: strings.ToUpper(fields[5]),
		ReadOnly: fields[6] == "1", SuperReadOnly: fields[7] == "1",
		ApplyQueue: applyQueue, RemoteApplyQueue: remoteQueue, Conflicts: conflicts, Reachable: true,
	}, true
}

func mgrStatusScript() string {
	return `var c=dba.getCluster(); var o=null; try { o=c.routerOptions({extended:1}); } catch(e) { o={unavailable:String(e.message||e)}; } print("` +
		mgrJSONMarker + `"+JSON.stringify({status:c.status({extended:2}),routers:c.listRouters(),router_options:o}));`
}

func mysqlShellMGRCommand(username, password string, port int, script string) string {
	findShell := `if [ -x /opt/gmha/mysql-shell/current/bin/mysqlsh ]; then mysqlsh_bin=/opt/gmha/mysql-shell/current/bin/mysqlsh; else mysqlsh_bin=$(command -v mysqlsh || find /opt /usr/local -type f -path '*/bin/mysqlsh' -perm -111 -print -quit 2>/dev/null); fi; [ -n "$mysqlsh_bin" ]`
	uri := fmt.Sprintf("%s@127.0.0.1:%d", url.User(strings.TrimSpace(username)).String(), port)
	return "set -eu; " + findShell +
		`; mysqlsh_home=$(mktemp -d /tmp/gmha-mysqlsh.XXXXXX); chmod 700 "$mysqlsh_home"; trap 'rm -rf "$mysqlsh_home"' EXIT; ` +
		"HOME=\"$mysqlsh_home\" MYSQLSH_USER_CONFIG_HOME=\"$mysqlsh_home\" MYSQLSH_TERM_COLOR_MODE=nocolor \"$mysqlsh_bin\" --js --uri " +
		shellQuote(uri) + " --password=" + shellQuote(password) + " --execute " + shellQuote(script)
}

func parseMGRJSONMarker(output string, target any) error {
	index := strings.LastIndex(output, mgrJSONMarker)
	if index < 0 {
		return errors.New("MySQL Shell 未返回可解析的 MGR 状态")
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(output[index+len(mgrJSONMarker):])))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("解析 MySQL Shell MGR 状态: %w", err)
	}
	return nil
}

func validateMGRActionConfirmation(clusterID string, req MGRManagementActionRequest) error {
	expected := ""
	switch req.Action {
	case "set_primary":
		expected = "SET PRIMARY " + req.MachineID
	case "rejoin_member":
		expected = "REJOIN " + req.MachineID
	case "rescan_metadata":
		expected = "RESCAN " + clusterID
	case "rotate_recovery_passwords":
		expected = "ROTATE RECOVERY " + clusterID
	case "reboot_complete_outage":
		expected = "REBOOT MGR " + clusterID
	default:
		return fmt.Errorf("不支持的 MGR 管理操作 %q", req.Action)
	}
	if req.Confirmation != expected {
		return fmt.Errorf("确认文本不匹配，请输入 %q", expected)
	}
	return nil
}

func prepareMGRAction(clusterID string, req MGRManagementActionRequest, status MGRManagementStatus, targets []mgrClusterTarget, username string) (mgrClusterTarget, string, error) {
	targetByMachine := make(map[string]mgrClusterTarget, len(targets))
	for _, target := range targets {
		targetByMachine[target.machine.ID] = target
	}
	target := targetByMachine[req.MachineID]
	seed, hasSeed := mgrOnlineSeed(targets)
	switch req.Action {
	case "set_primary":
		if !status.Quorum || !hasSeed {
			return mgrClusterTarget{}, "", errors.New("MGR 当前没有法定人数，不能切换 PRIMARY")
		}
		if target.machine.ID == "" || !strings.EqualFold(target.member.State, "ONLINE") {
			return mgrClusterTarget{}, "", errors.New("目标必须是当前集群中的 ONLINE MGR 成员")
		}
		if strings.EqualFold(target.member.Role, "PRIMARY") {
			return mgrClusterTarget{}, "", errors.New("目标已经是 PRIMARY")
		}
		instanceJSON, _ := json.Marshal(fmt.Sprintf("%s:%d", target.machine.IP, target.instance.Port))
		return seed, `var c=dba.getCluster(); c.setPrimaryInstance(` + string(instanceJSON) + `); print("` + mgrJSONMarker + `"+JSON.stringify({ok:true}));`, nil
	case "rejoin_member":
		if !status.Quorum || !hasSeed {
			return mgrClusterTarget{}, "", errors.New("MGR 当前没有法定人数，不能重新加入成员")
		}
		if target.machine.ID == "" || !target.member.Reachable {
			return mgrClusterTarget{}, "", errors.New("目标 MySQL 实例不可达，无法重新加入")
		}
		if strings.EqualFold(target.member.State, "ONLINE") {
			return mgrClusterTarget{}, "", errors.New("目标成员已经 ONLINE")
		}
		instanceJSON, _ := json.Marshal(fmt.Sprintf("%s@%s:%d", url.User(username).String(), target.machine.IP, target.instance.Port))
		return seed, `var c=dba.getCluster(); c.rejoinInstance(` + string(instanceJSON) + `); print("` + mgrJSONMarker + `"+JSON.stringify({ok:true}));`, nil
	case "rescan_metadata":
		if !status.Quorum || !hasSeed {
			return mgrClusterTarget{}, "", errors.New("MGR 当前没有法定人数，不能重扫元数据")
		}
		return seed, `var c=dba.getCluster(); c.rescan({addUnmanaged:true,removeObsolete:true}); print("` + mgrJSONMarker + `"+JSON.stringify({ok:true}));`, nil
	case "rotate_recovery_passwords":
		if !status.Healthy || !hasSeed {
			return mgrClusterTarget{}, "", errors.New("恢复账户轮换要求全部成员 ONLINE 且存在唯一 PRIMARY")
		}
		return seed, `var c=dba.getCluster(); c.resetRecoveryAccountsPassword(); print("` + mgrJSONMarker + `"+JSON.stringify({ok:true}));`, nil
	case "reboot_complete_outage":
		if status.OnlineMemberCount != 0 {
			return mgrClusterTarget{}, "", errors.New("只有全部 MGR 成员均非 ONLINE 时才能执行全组停机恢复")
		}
		if target.machine.ID == "" || !target.member.Reachable {
			return mgrClusterTarget{}, "", errors.New("请选择一个 MySQL 可达的恢复目标成员")
		}
		for _, member := range status.Members {
			if !member.Reachable {
				return mgrClusterTarget{}, "", fmt.Errorf("成员 %s 不可达；全组恢复要求所有成员均可达", member.MachineName)
			}
		}
		clusterJSON, _ := json.Marshal(safeMGRClusterName(clusterID))
		// Deliberately do not pass the AdminAPI "primary" override. MySQL Shell
		// compares every member's GTID history and selects the most up-to-date
		// member; forcing the UI-selected connection seed could discard writes.
		return target, `var c=dba.rebootClusterFromCompleteOutage(` + string(clusterJSON) + `); print("` + mgrJSONMarker + `"+JSON.stringify({status:c.status({extended:1})}));`, nil
	default:
		return mgrClusterTarget{}, "", fmt.Errorf("不支持的 MGR 管理操作 %q", req.Action)
	}
}

func mgrRecoveredGroupVerifyCommand(username, password string, port int, groupName string, expectedMembers int) string {
	if expectedMembers < 1 {
		expectedMembers = 1
	}
	sql := fmt.Sprintf(`SELECT IF(
		@@group_replication_group_name=%s AND COUNT(*)=%d AND
		SUM(m.MEMBER_STATE='ONLINE')=%d AND SUM(m.MEMBER_ROLE='PRIMARY')=1 AND
		COALESCE(SUM(s.COUNT_TRANSACTIONS_IN_QUEUE),0)=0 AND
		COALESCE(SUM(s.COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE),0)=0,
		'MGR_RECOVERY_CONSISTENT','MGR_RECOVERY_INCOMPLETE')
		FROM performance_schema.replication_group_members m
		LEFT JOIN performance_schema.replication_group_member_stats s ON s.MEMBER_ID=m.MEMBER_ID`, sqlLiteral(groupName), expectedMembers, expectedMembers)
	client := mysqlMGRClient(username, password, port) + " --batch --raw --skip-column-names"
	return "for i in $(seq 1 120); do " + client + " --execute=" + shellQuote(sql) +
		" 2>/dev/null | grep -Fxq MGR_RECOVERY_CONSISTENT && exit 0; sleep 1; done; echo 'MGR recovery consistency verification timed out' >&2; exit 1"
}

func mgrOnlineSeed(targets []mgrClusterTarget) (mgrClusterTarget, bool) {
	for _, target := range targets {
		if strings.EqualFold(target.member.State, "ONLINE") && strings.EqualFold(target.member.Role, "PRIMARY") {
			return target, true
		}
	}
	for _, target := range targets {
		if strings.EqualFold(target.member.State, "ONLINE") {
			return target, true
		}
	}
	return mgrClusterTarget{}, false
}

func mgrActionSuccessMessage(action string) string {
	return map[string]string{
		"set_primary":               "MGR PRIMARY 已安全切换",
		"rejoin_member":             "MGR 成员已重新加入",
		"rescan_metadata":           "InnoDB Cluster 元数据已重扫",
		"rotate_recovery_passwords": "MGR 内部恢复账户密码已轮换",
		"reboot_complete_outage":    "MGR 已从全组停机状态恢复",
	}[action]
}

func (s *HAService) runMGRTask(ctx context.Context, machine machinedomain.Machine, operation, display, command string, timeout time.Duration) (string, string, error) {
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		Internal: true, Operation: operation, DisplayName: display, StepName: display,
	})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = s.tasks.RedactExecTaskCommand(context.Background(), detail.Task.ID) }()
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, timeout)
	if err != nil {
		return detail.Task.ID, "", err
	}
	output := architectureProbeOutput(completed)
	if completed.Task.Status != taskdomain.StatusSuccess {
		if strings.TrimSpace(output) == "" {
			return detail.Task.ID, output, fmt.Errorf("%s 在 %s 执行失败", display, machine.Name)
		}
		return detail.Task.ID, output, fmt.Errorf("%s 在 %s 执行失败: %s", display, machine.Name, strings.TrimSpace(output))
	}
	return detail.Task.ID, output, nil
}

func (s *HAService) withMGRManagementLock(ctx context.Context, clusterID, operation string, execute func(context.Context) error) error {
	const lockTTL = 10 * time.Minute
	lockID := "mgr-" + operation + "-" + strings.TrimPrefix(newFailoverID(), "fo-")
	if err := s.repo.AcquireFailoverLock(ctx, clusterID, lockID, "gmha-mgr-management", lockTTL); err != nil {
		return fmt.Errorf("集群正在执行其他高可用操作，MGR 操作已阻止: %w", err)
	}
	executionCtx, cancel := context.WithCancel(ctx)
	lockErrors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if err := s.repo.RenewFailoverLock(context.Background(), clusterID, lockID, lockTTL); err != nil {
					lockErrors <- err
					cancel()
					return
				}
			}
		}
	}()
	operationErr := execute(executionCtx)
	cancel()
	<-done
	_ = s.repo.ReleaseFailoverLock(context.Background(), clusterID, lockID)
	select {
	case lockErr := <-lockErrors:
		return fmt.Errorf("MGR 操作失去集群互斥锁，已终止: %w", lockErr)
	default:
		return operationErr
	}
}
