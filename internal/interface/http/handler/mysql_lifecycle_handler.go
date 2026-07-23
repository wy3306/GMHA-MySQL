package handler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

type mysqlLifecycleRequest struct {
	Machine             string `json:"machine"`
	Port                int    `json:"port"`
	Action              string `json:"action"`
	Confirmation        string `json:"confirmation"`
	RiskAcknowledged    bool   `json:"risk_acknowledged"`
	PrimaryAcknowledged bool   `json:"primary_acknowledged"`
	DeepDataCheck       *bool  `json:"deep_data_check,omitempty"`
}

// HandleMySQLLifecycle creates a guarded restart or service-shutdown task.
// The task captures the cluster topology immediately before the action. A
// restart is successful only when topology, replication and data checks pass
// again after mysqld is available.
func (h *TaskHandler) HandleMySQLLifecycle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req mysqlLifecycleRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Machine = strings.TrimSpace(req.Machine)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	if err := validateMySQLLifecycleRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	machine, instance, err := h.service.ResolveMySQLInstance(r.Context(), req.Machine, req.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	endpoint := machine.IP + ":" + strconv.Itoa(instance.Port)
	expectedConfirmation := strings.ToUpper(req.Action) + " " + endpoint
	if req.Confirmation != expectedConfirmation {
		writeError(w, http.StatusConflict, fmt.Errorf("confirmation must exactly match %q", expectedConfirmation))
		return
	}
	if compatible, reason := h.service.MachineCapability(machine.ID, taskdomain.CapabilityMySQLDefaultsFile); !compatible {
		writeError(w, http.StatusConflict, fmt.Errorf("Agent does not support secure MySQL credential injection: %s", reason))
		return
	}
	targets, err := h.service.ListClusterMySQLTargets(r.Context(), machine.Cluster)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(targets) == 0 {
		targets = []app.MySQLInstanceTarget{{Machine: machine, Instance: instance}}
	}
	targets = uniqueLifecycleTargets(targets)
	deepCheck := true
	if req.DeepDataCheck != nil {
		deepCheck = *req.DeepDataCheck
	}
	stateDir := filepath.Join("/tmp", fmt.Sprintf("gmha-mysql-lifecycle-%d-%d", instance.Port, time.Now().UTC().UnixNano()))
	lockDir := mysqlLifecycleLockDir(machine.Cluster, endpoint)
	commands, err := mysqlLifecycleCommands(instance, machine.IP, targets, req.Action, stateDir, lockDir, deepCheck, req.PrimaryAcknowledged)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	actionName := map[string]string{"restart": "安全重启", "shutdown": "安全关闭"}[req.Action]
	detail, err := h.service.CreateExecTaskWithOptions(r.Context(), machine.IP, "", app.ExecTaskOptions{
		Operation:   "mysql_" + req.Action,
		DisplayName: fmt.Sprintf("MySQL %s %s", actionName, endpoint),
		Port:        instance.Port,
		Commands:    commands,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	go h.trackMySQLLifecycleTask(detail.Task.ID, machine.ID, instance.Port, req.Action)
	writeJSON(w, http.StatusOK, detail)
}

func validateMySQLLifecycleRequest(req mysqlLifecycleRequest) error {
	if req.Machine == "" || req.Port <= 0 || req.Port > 65535 {
		return errors.New("machine and valid port are required")
	}
	if req.Action != "restart" && req.Action != "shutdown" {
		return errors.New("action must be restart or shutdown")
	}
	if !req.RiskAcknowledged {
		return errors.New("risk acknowledgement is required")
	}
	if req.Confirmation == "" {
		return errors.New("typed confirmation is required")
	}
	return nil
}

func (h *TaskHandler) trackMySQLLifecycleTask(taskID, machineID string, port int, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	finished, err := h.service.WaitForTask(ctx, taskID, 2*time.Hour)
	if err != nil || finished.Task.Status != taskdomain.StatusSuccess {
		return
	}
	status := mysqlapp.StatusRunning
	if action == "shutdown" {
		status = mysqlapp.StatusStopped
	}
	_ = h.service.UpdateMySQLInstanceStatus(context.Background(), machineID, port, status)
}

func uniqueLifecycleTargets(items []app.MySQLInstanceTarget) []app.MySQLInstanceTarget {
	seen := make(map[string]bool, len(items))
	result := make([]app.MySQLInstanceTarget, 0, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Machine.IP) + ":" + strconv.Itoa(item.Instance.Port)
		if item.Machine.IP == "" || item.Instance.Port <= 0 || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Machine.IP + ":" + strconv.Itoa(result[i].Instance.Port)
		right := result[j].Machine.IP + ":" + strconv.Itoa(result[j].Instance.Port)
		return left < right
	})
	return result
}

func mysqlLifecycleCommands(target mysqlapp.Instance, targetIP string, members []app.MySQLInstanceTarget, action, stateDir, lockDir string, deepCheck, primaryAcknowledged bool) ([]taskdomain.ExecCommandStep, error) {
	unit := strings.TrimSuffix(strings.TrimSpace(target.SystemdUnit), ".service")
	if unit == "" {
		unit = fmt.Sprintf("mysqld-%d", target.Port)
	}
	mysqlBinary := "mysql"
	if strings.TrimSpace(target.BaseDir) != "" {
		mysqlBinary = filepath.Join(target.BaseDir, "bin", "mysql")
	}
	common := mysqlLifecycleShellPrelude(mysqlBinary)
	baseline := common + "\n" + strings.Join([]string{
		"set -eu",
		"state_dir=" + shellQuote(stateDir),
		"lock_dir=" + shellQuote(lockDir),
		"if ! mkdir -m 700 \"$lock_dir\" 2>/dev/null; then if find \"$lock_dir\" -maxdepth 0 -mmin +120 -print -quit 2>/dev/null | grep -q .; then rm -rf -- \"$lock_dir\" && mkdir -m 700 \"$lock_dir\"; else echo 'Another MySQL lifecycle operation is active for this cluster' >&2; exit 70; fi; fi",
		"printf '%s\\n' " + shellQuote(stateDir) + " > \"$lock_dir/owner\"",
		"baseline_ok=0; cleanup_baseline() { if [ \"$baseline_ok\" != 1 ]; then rm -rf -- \"$state_dir\" \"$lock_dir\"; fi; }; trap cleanup_baseline EXIT",
		"install -d -m 700 \"$state_dir\"",
		"systemctl is-active --quiet " + shellQuote(unit),
		mysqlLifecycleWritableGate(targetIP, target.Port, len(members), primaryAcknowledged),
		mysqlLifecycleTransactionGate(targetIP, target.Port),
		mysqlLifecycleSnapshotScript(members, "$state_dir/topology.before"),
		mysqlLifecycleReplicationCheckScript(members, deepCheck, 60),
		"baseline_ok=1",
		"echo 'GMHA_LIFECYCLE_BASELINE_OK topology_members=" + strconv.Itoa(len(members)) + "'",
	}, "\n")
	commands := []taskdomain.ExecCommandStep{{Name: "执行前风险检查与拓扑留档", Command: baseline}}
	if deepCheck && len(members) > 1 {
		commands = append(commands, taskdomain.ExecCommandStep{
			Name:    "重启前主从数据一致性校验",
			Command: common + "\nset -eu\n" + mysqlLifecyclePTCheckScript(members, "before"),
		})
	}
	switch action {
	case "shutdown":
		commands = append(commands, taskdomain.ExecCommandStep{
			Name: "关闭 MySQL 实例并确认进程退出",
			Command: strings.Join([]string{
				"set -eu",
				"systemctl stop " + shellQuote(unit),
				"if systemctl is-active --quiet " + shellQuote(unit) + "; then echo 'MySQL service is still active after shutdown' >&2; exit 73; fi",
				"echo 'GMHA_MYSQL_SHUTDOWN_OK unit=" + unit + "'",
				"rm -rf -- " + shellQuote(stateDir),
				"rm -rf -- " + shellQuote(lockDir),
			}, "\n"),
		})
	case "restart":
		commands = append(commands,
			taskdomain.ExecCommandStep{
				Name: "重启 MySQL 并等待服务恢复",
				Command: common + "\n" + strings.Join([]string{
					"set -eu",
					"systemctl restart " + shellQuote(unit),
					"ready=0",
					"for attempt in $(seq 1 120); do if systemctl is-active --quiet " + shellQuote(unit) + " && mysql_exec " + shellQuote(targetIP) + " " + strconv.Itoa(target.Port) + " --execute='SELECT 1' >/dev/null 2>&1; then ready=1; break; fi; sleep 1; done",
					"[ \"$ready\" = 1 ] || { echo 'MySQL did not become ready within 120 seconds' >&2; exit 74; }",
					"echo 'GMHA_MYSQL_RESTART_READY unit=" + unit + "'",
				}, "\n"),
			},
			taskdomain.ExecCommandStep{
				Name: "比对原主从架构并等待复制追平",
				Command: common + "\nset -eu\n" + strings.Join([]string{
					"state_dir=" + shellQuote(stateDir),
					mysqlLifecycleSnapshotScript(members, "$state_dir/topology.after"),
					"diff -u \"$state_dir/topology.before\" \"$state_dir/topology.after\" || { echo 'Replication topology changed after restart' >&2; exit 75; }",
					mysqlLifecycleReplicationCheckScript(members, deepCheck, 120),
					"echo 'GMHA_LIFECYCLE_TOPOLOGY_OK'",
				}, "\n"),
			},
		)
		if deepCheck && len(members) > 1 {
			commands = append(commands, taskdomain.ExecCommandStep{
				Name:    "重启后主从数据一致性复核",
				Command: common + "\nset -eu\n" + mysqlLifecyclePTCheckScript(members, "after"),
			})
		}
		commands = append(commands, taskdomain.ExecCommandStep{
			Name:    "完成安全重启",
			Command: "set -eu\nrm -rf -- " + shellQuote(stateDir) + "\nrm -rf -- " + shellQuote(lockDir) + "\necho 'GMHA_MYSQL_SAFE_RESTART_OK'",
		})
	default:
		return nil, errors.New("unsupported lifecycle action")
	}
	return commands, nil
}

func mysqlLifecycleLockDir(cluster, endpoint string) string {
	key := strings.TrimSpace(cluster)
	if key == "" {
		key = endpoint
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join("/var/lock", fmt.Sprintf("gmha-mysql-lifecycle-%x", sum[:6]))
}

func mysqlLifecycleShellPrelude(mysqlBinary string) string {
	return strings.Join([]string{
		"mysql_bin=" + shellQuote(mysqlBinary),
		"mysql_defaults=" + mysqlDefaultsFilePlaceholder,
		"mysql_exec() { mysql_host=\"$1\"; mysql_port=\"$2\"; shift 2; \"$mysql_bin\" --defaults-extra-file=\"$mysql_defaults\" --protocol=tcp --connect-timeout=5 --host=\"$mysql_host\" --port=\"$mysql_port\" --batch --raw --skip-column-names \"$@\"; }",
		"replica_status() { result=$(mysql_exec \"$1\" \"$2\" --vertical --execute='SHOW REPLICA STATUS' 2>/dev/null || true); if [ -z \"$result\" ]; then result=$(mysql_exec \"$1\" \"$2\" --vertical --execute='SHOW SLAVE STATUS' 2>/dev/null || true); fi; printf '%s\\n' \"$result\"; }",
		"status_field() { printf '%s\\n' \"$1\" | awk -F': ' -v wanted=\"$2\" '{ key=$1; gsub(/^[[:space:]]+|[[:space:]]+$/, \"\", key); if (key==wanted) { print substr($0,index($0,\": \")+2); exit } }'; }",
		"status_field_either() { value=$(status_field \"$1\" \"$2\"); if [ -z \"$value\" ]; then value=$(status_field \"$1\" \"$3\"); fi; printf '%s' \"$value\"; }",
		"snapshot_node() { host=\"$1\"; port=\"$2\"; core=$(mysql_exec \"$host\" \"$port\" --execute=\"SELECT CONCAT_WS('|',@@server_uuid,@@server_id,@@read_only,@@super_read_only)\"); repl=$(replica_status \"$host\" \"$port\"); source=$(status_field_either \"$repl\" Source_Host Master_Host); source_port=$(status_field_either \"$repl\" Source_Port Master_Port); [ -n \"$source\" ] || source='-'; [ -n \"$source_port\" ] || source_port='-'; printf '%s:%s|%s|%s|%s\\n' \"$host\" \"$port\" \"$core\" \"$source\" \"$source_port\"; }",
		"check_replication() { host=\"$1\"; port=\"$2\"; attempts=\"$3\"; allow_no_gtid=\"$4\"; n=1; while [ \"$n\" -le \"$attempts\" ]; do repl=$(replica_status \"$host\" \"$port\"); [ -n \"$repl\" ] || return 0; io=$(status_field_either \"$repl\" Replica_IO_Running Slave_IO_Running); sql=$(status_field_either \"$repl\" Replica_SQL_Running Slave_SQL_Running); lag=$(status_field_either \"$repl\" Seconds_Behind_Source Seconds_Behind_Master); source=$(status_field_either \"$repl\" Source_Host Master_Host); source_port=$(status_field_either \"$repl\" Source_Port Master_Port); [ -n \"$source_port\" ] || source_port=\"$port\"; if [ \"$io\" = Yes ] && [ \"$sql\" = Yes ] && [ \"${lag:-null}\" = 0 ]; then source_gtid=$(mysql_exec \"$source\" \"$source_port\" --execute='SELECT @@GLOBAL.gtid_executed' 2>/dev/null || true); replica_gtid=$(mysql_exec \"$host\" \"$port\" --execute='SELECT @@GLOBAL.gtid_executed' 2>/dev/null || true); if [ -z \"$source_gtid\" ] || [ -z \"$replica_gtid\" ]; then [ \"$allow_no_gtid\" = 1 ] && return 0; else verdict=$(mysql_exec \"$host\" \"$port\" --execute=\"SELECT IF(GTID_SUBSET('$source_gtid',@@GLOBAL.gtid_executed) AND GTID_SUBSET(@@GLOBAL.gtid_executed,'$source_gtid'),'OK','DIFF')\"); [ \"$verdict\" = OK ] && return 0; fi; fi; sleep 1; n=$((n+1)); done; echo \"replication/GTID check failed for $host:$port (io=$io sql=$sql lag=${lag:-NULL})\" >&2; return 1; }",
	}, "\n")
}

func mysqlLifecycleWritableGate(host string, port, memberCount int, acknowledged bool) string {
	ack := "0"
	if acknowledged {
		ack = "1"
	}
	return "target_read_only=$(mysql_exec " + shellQuote(host) + " " + strconv.Itoa(port) + " --execute='SELECT @@read_only'); " +
		"if [ \"$target_read_only\" = 0 ] && [ " + strconv.Itoa(memberCount) + " -gt 1 ] && [ " + ack + " != 1 ]; then " +
		"echo 'Target is currently writable/primary; confirm traffic failover before retrying' >&2; exit 71; fi"
}

func mysqlLifecycleTransactionGate(host string, port int) string {
	query := "SELECT COUNT(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id<>CONNECTION_ID()"
	return "trx_clear=0; for attempt in $(seq 1 60); do active_trx=$(mysql_exec " + shellQuote(host) + " " + strconv.Itoa(port) +
		" --execute=" + shellQuote(query) + "); if [ \"$active_trx\" = 0 ]; then trx_clear=1; break; fi; sleep 1; done; " +
		"[ \"$trx_clear\" = 1 ] || { echo \"active transactions did not drain within 60 seconds: $active_trx\" >&2; exit 72; }"
}

func mysqlLifecycleSnapshotScript(members []app.MySQLInstanceTarget, output string) string {
	lines := []string{": > " + output + ".tmp"}
	for _, item := range members {
		lines = append(lines, "snapshot_node "+shellQuote(item.Machine.IP)+" "+strconv.Itoa(item.Instance.Port)+" >> "+output+".tmp")
	}
	lines = append(lines, "sort "+output+".tmp > "+output, "rm -f "+output+".tmp", "cat "+output)
	return strings.Join(lines, "\n")
}

func mysqlLifecycleReplicationCheckScript(members []app.MySQLInstanceTarget, allowNoGTID bool, attempts int) string {
	allow := "0"
	if allowNoGTID {
		allow = "1"
	}
	lines := make([]string, 0, len(members)+1)
	for _, item := range members {
		lines = append(lines, "check_replication "+shellQuote(item.Machine.IP)+" "+strconv.Itoa(item.Instance.Port)+" "+strconv.Itoa(attempts)+" "+allow)
	}
	lines = append(lines, "echo 'GMHA_REPLICATION_HEALTH_OK'")
	return strings.Join(lines, "\n")
}

func mysqlLifecyclePTCheckScript(members []app.MySQLInstanceTarget, phase string) string {
	lines := []string{
		"command -v pt-table-checksum >/dev/null 2>&1 || { echo 'pt-table-checksum is required for deep data verification' >&2; exit 76; }",
		"primary_host=''; primary_port=''",
	}
	for _, item := range members {
		host, port := shellQuote(item.Machine.IP), strconv.Itoa(item.Instance.Port)
		lines = append(lines, "if [ -z \"$primary_host\" ] && [ \"$(mysql_exec "+host+" "+port+" --execute='SELECT @@read_only')\" = 0 ]; then primary_host="+host+"; primary_port="+port+"; fi")
	}
	lines = append(lines,
		"[ -n \"$primary_host\" ] || { echo 'no writable checksum source found in cluster' >&2; exit 77; }",
		"pt-table-checksum --defaults-file=\"$mysql_defaults\" --host=\"$primary_host\" --port=\"$primary_port\" --replicate=percona.checksums --create-replicate-table --no-check-replication-filters --no-check-binlog-format --max-load=Threads_running=25 --critical-load=Threads_running=50 --max-lag=10",
	)
	for _, item := range members {
		host, port := shellQuote(item.Machine.IP), strconv.Itoa(item.Instance.Port)
		checkSQL := "SELECT IF(COALESCE(SUM(master_crc<>this_crc OR master_cnt<>this_cnt),0)=0,'OK','DIFF') FROM percona.checksums"
		lines = append(lines,
			"checksum_table=$(mysql_exec "+host+" "+port+" --execute=\"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='percona' AND table_name='checksums'\"); [ \"$checksum_table\" = 1 ] || { echo 'checksum result did not reach "+item.Machine.IP+":"+port+"' >&2; exit 78; }",
			"checksum_result=$(mysql_exec "+host+" "+port+" --execute="+shellQuote(checkSQL)+"); [ \"$checksum_result\" = OK ] || { echo 'data checksum difference detected on "+item.Machine.IP+":"+port+"' >&2; exit 79; }",
		)
	}
	lines = append(lines, "echo 'GMHA_PT_DATA_CONSISTENCY_OK phase="+phase+"'")
	return strings.Join(lines, "\n")
}
