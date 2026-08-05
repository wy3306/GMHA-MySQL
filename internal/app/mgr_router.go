package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
)

const (
	defaultMGRPort    = 33061
	defaultRouterPort = 6446
)

func mgrVersionNumber(raw string) int {
	parts := strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool { return r < '0' || r > '9' })
	if len(parts) < 2 {
		return 0
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch := 0
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}
	if majorErr != nil || minorErr != nil {
		return 0
	}
	return major*1_000_000 + minor*1_000 + patch
}

func mgrVersionSupported(raw string) bool {
	return mgrVersionNumber(raw) >= 8_000_017
}

func normalizeMGRRouterRequest(clusterID string, req hadomain.ArchitectureAdjustmentRequest) hadomain.ArchitectureAdjustmentRequest {
	if req.Architecture != hadomain.ArchitectureMGRRouter {
		return req
	}
	if req.MGRPort <= 0 {
		req.MGRPort = defaultMGRPort
	}
	if req.RouterPort <= 0 {
		req.RouterPort = defaultRouterPort
	}
	if strings.TrimSpace(req.MGRGroupName) == "" {
		req.MGRGroupName = deterministicMGRUUID(clusterID)
	}
	return req
}

func deterministicMGRUUID(clusterID string) string {
	sum := sha256.Sum256([]byte("gmha:mgr:" + strings.TrimSpace(clusterID)))
	raw := append([]byte(nil), sum[:16]...)
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	value := hex.EncodeToString(raw)
	return value[0:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:32]
}

func plannedMGRServerIDs(req hadomain.ArchitectureAdjustmentRequest, instances map[string]mysqlapp.Instance) map[string]int {
	result := make(map[string]int, len(req.Nodes))
	used := make(map[int]bool, len(req.Nodes))
	counts := make(map[int]int, len(req.Nodes))
	for _, node := range req.Nodes {
		if instance, exists := instances[node.MachineID]; exists && instance.ServerID > 0 {
			counts[instance.ServerID]++
		}
	}
	for _, node := range req.Nodes {
		instance, exists := instances[node.MachineID]
		if !exists {
			continue
		}
		current := instance.ServerID
		if current > 0 && counts[current] == 1 && !used[current] {
			result[node.MachineID] = current
			used[current] = true
			continue
		}
		for salt := 0; ; salt++ {
			sum := sha256.Sum256([]byte(fmt.Sprintf("gmha:mgr-server-id:%s:%s:%d", req.MGRGroupName, node.MachineID, salt)))
			candidate := int(binary.BigEndian.Uint32(sum[:4]))
			if candidate == 0 || used[candidate] {
				continue
			}
			result[node.MachineID] = candidate
			used[candidate] = true
			break
		}
	}
	return result
}

func validMGRUUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func (s *HAService) executeMGRRouterArchitecture(
	ctx context.Context,
	runs architectureRunRepository,
	run *hadomain.ArchitectureRun,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
) error {
	instances, err := s.mgrInstances(ctx, req)
	if err != nil {
		s.failArchitectureRun(ctx, runs, run, "preflight", err)
		return err
	}
	existingGroup := req.CurrentArchitecture == hadomain.ArchitectureMGRRouter
	if !existingGroup {
		for machineID, serverID := range plannedMGRServerIDs(req, instances) {
			instance := instances[machineID]
			instance.ServerID = serverID
			instances[machineID] = instance
		}
	}
	if !existingGroup {
		if err := s.runArchitectureStep(ctx, runs, run, "cleanup_stale_router", func() ([]string, error) {
			return s.cleanupMGRRouters(ctx, run.ClusterID, req, machines)
		}); err != nil {
			return err
		}
	}
	if err := s.runArchitectureStep(ctx, runs, run, "preflight", func() ([]string, error) {
		return s.preflightMGRRouter(ctx, req, machines, instances)
	}); err != nil {
		return err
	}

	if !existingGroup {
		if err := s.runArchitectureStep(ctx, runs, run, "freeze_business_access", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port,
					"SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@offline_mode=1,'MGR_FROZEN','MGR_OPEN');") + " | grep -Fxq MGR_FROZEN"
			})
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "drain_business_sessions", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return killBusinessSessionsCommand(req, node.MachineID, node.Port)
			})
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "stop_stale_group", func() ([]string, error) {
			return s.stopStaleMGRGroup(ctx, req, machines)
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "align_group_data", func() ([]string, error) {
			return s.alignMGRMemberData(ctx, req, machines)
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "configure_group_replication", func() ([]string, error) {
			return s.configureMGRMembers(ctx, req, machines, instances)
		}); err != nil {
			return err
		}
	}
	if err := s.runArchitectureStep(ctx, runs, run, "bootstrap_group", func() ([]string, error) {
		if existingGroup {
			return s.switchMGRPrimary(ctx, req, machines)
		}
		return s.bootstrapMGRGroup(ctx, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "verify_group", func() ([]string, error) {
		return s.verifyMGRGroup(ctx, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "deploy_mysql_shell", func() ([]string, error) {
		return s.deployMySQLShell(ctx, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "adopt_innodb_cluster", func() ([]string, error) {
		return s.adoptMGRMetadata(ctx, run.ClusterID, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "deploy_mysql_router", func() ([]string, error) {
		return s.deployMGRRouters(ctx, run.ClusterID, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "verify_router", func() ([]string, error) {
		return s.verifyMGRRouters(ctx, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "resume_business_connections", func() ([]string, error) {
		return s.resumeArchitectureBusinessConnections(ctx, req, machines)
	}); err != nil {
		return err
	}
	return nil
}

func (s *HAService) executeMGRToAsyncArchitecture(
	ctx context.Context,
	runs architectureRunRepository,
	run *hadomain.ArchitectureRun,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
) error {
	businessFrozen := false
	defer func() {
		if businessFrozen {
			_, _ = s.resumeArchitectureBusinessConnections(context.Background(), req, machines)
		}
	}()
	if err := s.runArchitectureStep(ctx, runs, run, "freeze_business_access", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port,
				"SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@offline_mode=1 AND @@read_only=1 AND @@super_read_only=1,'MGR_FROZEN','MGR_OPEN');") + " | grep -Fxq MGR_FROZEN"
		})
	}); err != nil {
		return err
	}
	businessFrozen = true
	if err := s.runArchitectureStep(ctx, runs, run, "drain_business_sessions", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return killBusinessSessionsCommand(req, node.MachineID, node.Port)
		})
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "verify_group", func() ([]string, error) {
		ids, err := s.switchMGRPrimary(ctx, req, machines)
		if err != nil {
			return ids, err
		}
		verified, err := s.verifyMGRGroup(ctx, req, machines)
		return append(ids, verified...), err
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "teardown_mgr", func() ([]string, error) {
		return s.teardownMGRForAsync(ctx, run.ClusterID, req, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "promote_new_master", func() ([]string, error) {
		primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
		if !ok {
			return nil, errors.New("target asynchronous primary not found")
		}
		client := mysqlArchitectureClient(architectureRootPassword(req, primary.MachineID), primary.Port)
		command := replicationStopResetShell(client) + mysqlRolePersistenceCommand(client, false)
		return s.runOneArchitectureCommand(ctx, machines[primary.MachineID], command)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "reconfigure_topology", func() ([]string, error) {
		return s.configureArchitectureTopology(ctx, req, req.PreferredNewMasterMachineID, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "verify_topology", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return verifyArchitectureNodeCommand(req, node, req.PreferredNewMasterMachineID)
		})
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "pt_verify_replication", func() ([]string, error) {
		return s.verifyArchitectureDataWithPT(ctx, req, req.PreferredNewMasterMachineID, machines)
	}); err != nil {
		return err
	}
	if err := s.runArchitectureStep(ctx, runs, run, "resume_business_connections", func() ([]string, error) {
		return s.resumeArchitectureBusinessConnections(ctx, req, machines)
	}); err != nil {
		return err
	}
	businessFrozen = false
	return nil
}

func (s *HAService) cleanupMGRRouters(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	var ids []string
	for _, node := range req.Nodes {
		created, err := s.CleanupMGRRouterNode(ctx, clusterID, machines[node.MachineID])
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("cleanup managed MySQL Router on %s: %w", node.MachineID, err)
		}
	}
	return ids, nil
}

func (s *HAService) stopStaleMGRGroup(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		return "(" + client + " --execute='STOP GROUP_REPLICATION' >/dev/null 2>&1 || true); " +
			client + " --batch --skip-column-names --execute=\"SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid\" | grep -Fxq 0"
	})
}

func (s *HAService) teardownMGRForAsync(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	ids, err := s.cleanupMGRRouters(ctx, clusterID, req, machines)
	if err != nil {
		return ids, err
	}
	instances, err := s.mgrInstances(ctx, req)
	if err != nil {
		return ids, err
	}
	for _, node := range req.Nodes {
		command := mgrAsyncTeardownCommand(req, node, instances[node.MachineID])
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if runErr != nil {
			return ids, fmt.Errorf("stop Group Replication on %s: %w", node.MachineID, runErr)
		}
	}
	return ids, nil
}

func mgrAsyncTeardownCommand(req hadomain.ArchitectureAdjustmentRequest, node hadomain.ArchitectureNodeRequest, instance mysqlapp.Instance) string {
	client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
	return fmt.Sprintf(`set -eu
cnf=%s
[ -n "$cnf" ] && [ -f "$cnf" ] || { echo "registered MySQL config is missing: $cnf" >&2; exit 72; }
backup="${cnf}.gmha-mgr-exit.$(date +%%Y%%m%%d%%H%%M%%S).bak"
cp -a "$cnf" "$backup"
awk '
  /^# gmha mgr managed$/ {managed=1}
  managed && /^group_replication_start_on_boot=/ {$0="group_replication_start_on_boot=OFF"}
  managed && /^[[:space:]]*$/ {managed=0}
  {print}
' "$cnf" > "${cnf}.tmp"
mv "${cnf}.tmp" "$cnf"
if ! grep -Fxq 'group_replication_start_on_boot=OFF' "$cnf"; then
  printf '\n# gmha mgr managed\n[mysqld]\ngroup_replication_start_on_boot=OFF\n\n' >> "$cnf"
fi
(%s --execute='STOP GROUP_REPLICATION' >/dev/null 2>&1 || true)
(%s --execute="RESET REPLICA ALL FOR CHANNEL 'group_replication_recovery'" >/dev/null 2>&1 || %s --execute="RESET SLAVE ALL FOR CHANNEL 'group_replication_recovery'" >/dev/null 2>&1 || true)

grep -Fxq 'group_replication_start_on_boot=OFF' "$cnf"
%s --batch --skip-column-names --execute="SELECT COUNT(*) FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid" | grep -Fxq 0`,
		shellQuote(instance.MyCnfPath), client, client, client, client)
}

func (s *HAService) mgrInstances(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest) (map[string]mysqlapp.Instance, error) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return nil, err
	}
	byMachine := make(map[string][]mysqlapp.Instance)
	for _, item := range items {
		byMachine[item.MachineID] = append(byMachine[item.MachineID], item)
	}
	result := make(map[string]mysqlapp.Instance, len(req.Nodes))
	for _, node := range req.Nodes {
		instance, ok := architectureInstanceForNode(node, byMachine[node.MachineID])
		if !ok {
			return nil, fmt.Errorf("registered MySQL instance %s:%d not found", node.MachineID, node.Port)
		}
		result[node.MachineID] = instance
	}
	return result, nil
}

func (s *HAService) preflightMGRRouter(
	ctx context.Context,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
	instances map[string]mysqlapp.Instance,
) ([]string, error) {
	var ids []string
	serverIDs := make(map[string]string, len(req.Nodes))
	serverUUIDs := make(map[string]string, len(req.Nodes))
	groupName, groupView, currentPrimaries := "", "", 0
	for _, node := range req.Nodes {
		instance := instances[node.MachineID]
		version := instance.Version
		if strings.TrimSpace(version) == "" {
			version, _ = mysqlapp.PackageVersion(instance.PackageName)
		}
		capabilities, err := mysqlapp.CapabilitiesForVersion(version)
		if err != nil || capabilities.Legacy57 || !mgrVersionSupported(version) {
			return ids, fmt.Errorf("MGR member %s must run MySQL 8.0.17 or newer, got %q", node.MachineID, version)
		}
		password := architectureRootPassword(req, node.MachineID)
		client := mysqlArchitectureClient(password, node.Port)
		sql := mgrPreflightSQL(req.CurrentArchitecture == hadomain.ArchitectureMGRRouter)
		identityCondition := `$1!="" && $3=="ON" && $4==0 && $5==0 && $6=="YES"`
		if req.CurrentArchitecture == hadomain.ArchitectureMGRRouter {
			identityCondition = `$1!="" && $2+0>0 && $3=="ON" && $4==0 && $5==0 && $6=="YES"`
		}
		command := "command -v systemctl >/dev/null && command -v mysql >/dev/null && command -v tar >/dev/null && command -v xz >/dev/null && command -v find >/dev/null && command -v sha256sum >/dev/null && command -v useradd >/dev/null && (command -v curl >/dev/null || command -v wget >/dev/null) && " +
			"probe=$(" + mysqlArchitectureCommand(password, node.Port, sql) + ") && " +
			"printf '%s\\n' \"$probe\" | awk -F'|' '" + identityCondition + " {ok=1} END {exit ok?0:1}' && " +
			"grants=$(" + client + " --batch --raw --skip-column-names --execute='SHOW GRANTS FOR CURRENT_USER') && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'GROUP_REPLICATION_ADMIN' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'PERSIST_RO_VARIABLES_ADMIN' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'ALL PRIVILEGES|CREATE USER' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'WITH GRANT OPTION' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'SYSTEM_VARIABLES_ADMIN|SUPER' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'CONNECTION_ADMIN|SUPER' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'BACKUP_ADMIN' && " +
			"printf '%s\\n' \"$grants\" | grep -Eqi 'CLONE_ADMIN'"
		if mysqlapp.SupportsDynamicPrivilegeForVersion(version, "REPLICATION_APPLIER") {
			command += " && printf '%s\\n' \"$grants\" | grep -Eqi 'REPLICATION_APPLIER'"
		}
		if req.CurrentArchitecture != hadomain.ArchitectureMGRRouter {
			command += fmt.Sprintf(
				" && command -v ss >/dev/null && { for port in %d %d %d %d; do ! ss -ltnH | awk '{print $4}' | grep -Eq \"(^|:)${port}$\" || { echo \"required Router port ${port} is already listening\" >&2; exit 78; }; done; }",
				req.RouterPort, req.RouterPort+1, req.RouterPort+2, req.RouterPort+3,
			)
			command += fmt.Sprintf(
				" && { if ss -ltnH | awk '{print $4}' | grep -Eq '(^|:)%d$'; then mgr_local=$(%s --batch --skip-column-names --execute='SELECT @@global.group_replication_local_address' 2>/dev/null || true); printf '%%s\\n' \"$mgr_local\" | grep -Eq '(^|:)%d$' || { echo 'required MGR port %d is occupied by an unmanaged process' >&2; exit 78; }; fi; }",
				req.MGRPort, client, req.MGRPort, req.MGRPort,
			)
		}
		command += " && printf 'MGR_PREFLIGHT|%s\\n' \"$probe\""
		taskID, output, runErr := s.runOneArchitectureProbe(ctx, machines[node.MachineID], command)
		if taskID != "" {
			ids = append(ids, taskID)
		}
		if runErr != nil {
			return ids, fmt.Errorf("MGR prerequisite check failed on %s: MySQL 8.0.17+, GTID, TLS, InnoDB tables with keys, MGR administrative privileges, systemd, MySQL client, archive/hash tools, and a download tool are required: %w", node.MachineID, runErr)
		}
		line := strings.TrimSpace(output)
		line = strings.TrimPrefix(line, "MGR_PREFLIGHT|")
		fields := strings.Split(line, "|")
		if len(fields) != 10 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
			return ids, fmt.Errorf("MGR prerequisite check returned malformed server identity on %s", node.MachineID)
		}
		serverUUID, serverID := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if other, exists := serverUUIDs[serverUUID]; exists {
			return ids, fmt.Errorf("MGR requires unique server_uuid; %s and %s both report %s", other, node.MachineID, serverUUID)
		}
		if other, exists := serverIDs[serverID]; exists && req.CurrentArchitecture == hadomain.ArchitectureMGRRouter {
			return ids, fmt.Errorf("MGR requires unique server_id; %s and %s both report %s", other, node.MachineID, serverID)
		}
		serverUUIDs[serverUUID], serverIDs[serverID] = node.MachineID, node.MachineID
		if req.CurrentArchitecture == hadomain.ArchitectureMGRRouter {
			nodeGroup, nodeState, nodeRole, nodeView := strings.TrimSpace(fields[6]), strings.TrimSpace(fields[7]), strings.TrimSpace(fields[8]), strings.TrimSpace(fields[9])
			if !strings.EqualFold(nodeState, "ONLINE") {
				return ids, fmt.Errorf("existing MGR member %s is not ONLINE (state=%s)", node.MachineID, nodeState)
			}
			if strings.EqualFold(nodeRole, "PRIMARY") {
				currentPrimaries++
			}
			if groupName == "" {
				groupName, groupView = nodeGroup, nodeView
			} else if nodeGroup != groupName || nodeView != groupView {
				return ids, fmt.Errorf("existing MGR member %s observes a different group or membership view", node.MachineID)
			}
		}
	}
	if req.CurrentArchitecture == hadomain.ArchitectureMGRRouter {
		if groupName == "" || currentPrimaries != 1 {
			return ids, fmt.Errorf("existing MGR must have one group UUID and exactly one PRIMARY, got group=%q primaries=%d", groupName, currentPrimaries)
		}
		viewUUIDs := make(map[string]bool)
		for _, value := range strings.Split(groupView, ",") {
			if value = strings.TrimSpace(value); value != "" {
				viewUUIDs[value] = true
			}
		}
		if len(viewUUIDs) != len(serverUUIDs) {
			return ids, fmt.Errorf("existing MGR view has %d members but %d managed nodes were requested", len(viewUUIDs), len(serverUUIDs))
		}
		for uuid := range serverUUIDs {
			if !viewUUIDs[uuid] {
				return ids, fmt.Errorf("existing MGR view does not contain managed server_uuid %s", uuid)
			}
		}
	}
	return ids, nil
}

func mgrPreflightSQL(existingGroup bool) string {
	groupFields := "'','','',''"
	if existingGroup {
		groupFields = "@@group_replication_group_name," +
			"COALESCE((SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid),'')," +
			"COALESCE((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid),'')," +
			"COALESCE((SELECT GROUP_CONCAT(MEMBER_ID ORDER BY MEMBER_ID SEPARATOR ',') FROM performance_schema.replication_group_members),'')"
	}
	return "SELECT CONCAT_WS('|',@@server_uuid,@@server_id,@@global.gtid_mode," +
		"(SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema') AND engine<>'InnoDB')," +
		"(SELECT COUNT(*) FROM information_schema.tables t WHERE t.table_schema NOT IN ('mysql','sys','performance_schema','information_schema') AND t.table_type='BASE TABLE' AND NOT EXISTS (" +
		"SELECT 1 FROM information_schema.statistics s JOIN information_schema.columns c ON c.table_schema=s.table_schema AND c.table_name=s.table_name AND c.column_name=s.column_name " +
		"WHERE s.table_schema=t.table_schema AND s.table_name=t.table_name AND s.non_unique=0 GROUP BY s.index_name HAVING SUM(c.is_nullable='YES')=0))," +
		"@@have_ssl," + groupFields + ");"
}

func (s *HAService) alignMGRMemberData(
	ctx context.Context,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	var ids []string
	businessTables := make(map[string]int, len(req.Nodes))
	countSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema','gmha','percona') AND table_type='BASE TABLE'"
	for _, node := range req.Nodes {
		taskID, output, err := s.runOneArchitectureProbe(
			ctx,
			machines[node.MachineID],
			mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, countSQL),
		)
		if taskID != "" {
			ids = append(ids, taskID)
		}
		if err != nil {
			return ids, fmt.Errorf("count business tables on %s: %w", node.MachineID, err)
		}
		count, err := strconv.Atoi(strings.TrimSpace(output))
		if err != nil || count < 0 {
			return ids, fmt.Errorf("invalid business table count from %s: %q", node.MachineID, output)
		}
		businessTables[node.MachineID] = count
	}

	allEmpty := true
	for _, count := range businessTables {
		if count > 0 {
			allEmpty = false
			break
		}
	}
	if req.FreshInstall && !allEmpty {
		return ids, errors.New("fresh MGR bootstrap found business tables; refusing to discard any secondary GTID history")
	}
	if allEmpty {
		for _, node := range req.Nodes {
			if node.MachineID == primary.MachineID {
				continue
			}
			client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
			command := mgrResetEmptyGTIDCommand(client, architectureRootPassword(req, node.MachineID), node.Port)
			created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
			ids = append(ids, created...)
			if err != nil {
				return ids, fmt.Errorf("reset empty MGR secondary GTID history on %s: %w", node.MachineID, err)
			}
		}
		return ids, nil
	}

	switch req.CurrentArchitecture {
	case hadomain.ArchitectureMasterSlave, hadomain.ArchitectureDualMaster, hadomain.ArchitectureMultiMaster:
	default:
		return ids, errors.New("MGR conversion with existing business data requires a healthy managed replication topology; seed empty members or first convert them to a verified master/replica topology")
	}
	primaryClient := mysqlArchitectureClient(architectureRootPassword(req, primary.MachineID), primary.Port)
	primaryTaskID, baseline, err := s.runOneArchitectureProbe(
		ctx,
		machines[primary.MachineID],
		primaryClient+" --batch --raw --skip-column-names --execute="+shellQuote("SELECT REPLACE(@@global.gtid_executed,'\n','')"),
	)
	if primaryTaskID != "" {
		ids = append(ids, primaryTaskID)
	}
	if err != nil {
		return ids, fmt.Errorf("read preferred MGR primary GTID baseline: %w", err)
	}
	baseline = strings.TrimSpace(baseline)
	if baseline == "" {
		return ids, errors.New("preferred MGR primary has business data but an empty GTID baseline")
	}
	for _, node := range req.Nodes {
		if node.MachineID == primary.MachineID {
			continue
		}
		sql := fmt.Sprintf(
			"SELECT IF(WAIT_FOR_EXECUTED_GTID_SET(%s,60)=0 AND GTID_SUBSET(@@global.gtid_executed,%s)=1 AND GTID_SUBSET(%s,@@global.gtid_executed)=1,'MGR_GTID_ALIGNED','MGR_GTID_DIVERGED')",
			sqlLiteral(baseline), sqlLiteral(baseline), sqlLiteral(baseline),
		)
		command := mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, sql) + " | grep -Fxq MGR_GTID_ALIGNED"
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if runErr != nil {
			return ids, fmt.Errorf("MGR member %s did not converge to the preferred primary GTID set; divergent transactions must be reconciled before conversion: %w", node.MachineID, runErr)
		}
	}
	created, err := s.runOneArchitectureCommand(
		ctx,
		machines[primary.MachineID],
		mysqlArchitectureCommand(
			architectureRootPassword(req, primary.MachineID),
			primary.Port,
			"SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;",
		),
	)
	ids = append(ids, created...)
	if err != nil {
		return ids, fmt.Errorf("open fenced preferred primary for PT checksum: %w", err)
	}
	verified, err := s.verifyArchitectureDataWithPT(ctx, req, primary.MachineID, machines)
	ids = append(ids, verified...)
	fenceTasks, fenceErr := s.runOneArchitectureCommand(
		context.WithoutCancel(ctx),
		machines[primary.MachineID],
		mysqlArchitectureCommand(
			architectureRootPassword(req, primary.MachineID),
			primary.Port,
			"SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;",
		),
	)
	ids = append(ids, fenceTasks...)
	if fenceErr != nil {
		return ids, fmt.Errorf("restore preferred primary write fence after PT checksum: %w", fenceErr)
	}
	if err != nil {
		return ids, fmt.Errorf("MGR conversion data consistency gate failed: %w", err)
	}
	return ids, nil
}

func mgrResetEmptyGTIDCommand(client, password string, port int) string {
	resetGTID := "(" + client + " --execute='RESET BINARY LOGS AND GTIDS' >/dev/null 2>&1 || " +
		client + " --execute='RESET MASTER')"
	return "(" + client + " --execute='STOP GROUP_REPLICATION' >/dev/null 2>&1 || true); " +
		replicationStopResetShell(client) +
		client + " --execute='SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=ON'; " +
		"if ! " + resetGTID + "; then " + client + " --execute='SET GLOBAL super_read_only=ON' || true; exit 1; fi; " +
		client + " --execute='SET GLOBAL super_read_only=ON'; " +
		mysqlArchitectureCommand(
			password,
			port,
			"SELECT IF(@@global.gtid_executed='','MGR_GTID_EMPTY','MGR_GTID_NOT_EMPTY')",
		) + " | grep -Fxq MGR_GTID_EMPTY"
}

func (s *HAService) configureMGRMembers(
	ctx context.Context,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
	instances map[string]mysqlapp.Instance,
) ([]string, error) {
	seeds := make([]string, 0, len(req.Nodes))
	allowlist := make([]string, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		seeds = append(seeds, net.JoinHostPort(machines[node.MachineID].IP, strconv.Itoa(req.MGRPort)))
		allowlist = append(allowlist, machines[node.MachineID].IP)
	}
	sort.Strings(seeds)
	sort.Strings(allowlist)
	var ids []string
	for _, node := range req.Nodes {
		instance := instances[node.MachineID]
		command := mgrConfigCommand(req, node, machines[node.MachineID], instance, strings.Join(seeds, ","), strings.Join(allowlist, ","))
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("configure Group Replication on %s: %w", node.MachineID, err)
		}
		if saver, ok := s.instances.(interface {
			Save(context.Context, mysqlapp.Instance) error
		}); ok {
			instance.UpdatedAt = time.Now().UTC()
			if err := saver.Save(ctx, instance); err != nil {
				return ids, fmt.Errorf("persist repaired server_id for %s: %w", node.MachineID, err)
			}
		}
	}
	return ids, nil
}

func mgrConfigCommand(
	req hadomain.ArchitectureAdjustmentRequest,
	node hadomain.ArchitectureNodeRequest,
	machine machinedomain.Machine,
	instance mysqlapp.Instance,
	seeds string,
	allowlist string,
) string {
	unit := strings.TrimSpace(instance.SystemdUnit)
	if unit == "" {
		unit = fmt.Sprintf("mysqld-%d", node.Port)
	}
	settings := []string{
		"# gmha mgr managed",
		"[mysqld]",
		"plugin_load_add=group_replication.so",
		"plugin_load_add=mysql_clone.so",
		"report_host=" + machine.IP,
		"disabled_storage_engines=MyISAM,BLACKHOLE,FEDERATED,ARCHIVE,MEMORY",
		"gtid_mode=ON",
		"enforce_gtid_consistency=ON",
		"binlog_format=ROW",
	}
	if instance.ServerID > 0 {
		settings = append(settings, fmt.Sprintf("server_id=%d", instance.ServerID))
	}
	version := instance.Version
	if strings.TrimSpace(version) == "" {
		version, _ = mysqlapp.PackageVersion(instance.PackageName)
	}
	capabilities, _ := mysqlapp.CapabilitiesForVersion(version)
	if capabilities.LegacyReplicationNames {
		settings = append(settings, "log_slave_updates=ON")
	} else {
		settings = append(settings, "log_replica_updates=ON")
	}
	if number := mgrVersionNumber(version); number >= 8_000_017 && number < 8_000_021 {
		settings = append(settings, "transaction_write_set_extraction=XXHASH64")
	}
	if mgrVersionNumber(version) < 8_000_022 {
		settings = append(settings, "group_replication_ip_whitelist="+allowlist)
	} else {
		settings = append(settings, "group_replication_ip_allowlist="+allowlist)
	}
	settings = append(settings,
		"group_replication_group_name="+req.MGRGroupName,
		"group_replication_start_on_boot=ON",
		"group_replication_local_address="+net.JoinHostPort(machine.IP, strconv.Itoa(req.MGRPort)),
		"group_replication_group_seeds="+seeds,
		"group_replication_bootstrap_group=OFF",
		"group_replication_single_primary_mode=ON",
		"group_replication_enforce_update_everywhere_checks=OFF",
		"group_replication_consistency=BEFORE_ON_PRIMARY_FAILOVER",
		"group_replication_ssl_mode=REQUIRED",
		"group_replication_recovery_use_ssl=ON",
		"group_replication_exit_state_action=READ_ONLY",
		"group_replication_autorejoin_tries=3",
		"group_replication_member_expel_timeout=5",
		"",
	)
	block := strings.Join(settings, "\n")
	mysqld := strings.TrimRight(instance.BaseDir, "/") + "/bin/mysqld"
	pluginDirCommand := mysqlArchitectureCommand(
		architectureRootPassword(req, node.MachineID),
		node.Port,
		"SELECT @@global.plugin_dir",
	)
	pluginStatusCommand := mysqlArchitectureCommand(
		architectureRootPassword(req, node.MachineID),
		node.Port,
		"SELECT IF(COUNT(*)=2,'MGR_PLUGINS_OK','MGR_PLUGINS_MISSING') FROM information_schema.plugins WHERE plugin_name IN ('group_replication','clone') AND plugin_status='ACTIVE'",
	)
	return fmt.Sprintf(`set -eu
cnf=%s
backup="${cnf}.gmha-mgr.$(date +%%Y%%m%%d%%H%%M%%S).bak"
plugin_dir=$(%s)
for plugin_file in group_replication.so mysql_clone.so; do
  if [ ! -r "${plugin_dir%%/}/$plugin_file" ]; then
    echo "MGR prerequisite missing: ${plugin_dir%%/}/$plugin_file; reinstall the MySQL Server package matching this instance version and architecture" >&2
    exit 1
  fi
done
cp -a "$cnf" "$backup"
awk '
  /^# gmha mgr managed$/ {managed=1; next}
  managed && /^[[:space:]]*$/ {managed=0; next}
  managed {next}
  {print}
' "$cnf" > "${cnf}.tmp"
mv "${cnf}.tmp" "$cnf"
printf '\n%%s\n' %s >> "$cnf"
if ! %s --defaults-file="$cnf" --validate-config; then cp -a "$backup" "$cnf"; exit 1; fi
if ! systemctl restart %s; then cp -a "$backup" "$cnf"; systemctl restart %s || true; exit 1; fi
for i in $(seq 1 90); do
  if %s >/dev/null 2>&1 && %s | grep -Fxq MGR_PLUGINS_OK; then exit 0; fi
  sleep 1
done
cp -a "$backup" "$cnf"
systemctl restart %s || true
exit 1`,
		shellQuote(instance.MyCnfPath),
		pluginDirCommand,
		shellQuote(block),
		shellQuote(mysqld),
		shellQuote(unit),
		shellQuote(unit),
		mysqlArchitectureCommand(
			architectureRootPassword(req, node.MachineID),
			node.Port,
			fmt.Sprintf("SELECT IF(@@server_id=%d,'MGR_SERVER_ID_OK','MGR_SERVER_ID_MISMATCH')", instance.ServerID),
		)+" | grep -Fxq MGR_SERVER_ID_OK",
		pluginStatusCommand,
		shellQuote(unit),
	)
}

func (s *HAService) bootstrapMGRGroup(
	ctx context.Context,
	req hadomain.ArchitectureAdjustmentRequest,
	machines map[string]machinedomain.Machine,
) ([]string, error) {
	var ids []string
	user := strings.TrimSpace(req.ReplicationUser)
	if user == "" || req.ReplicationPassword == "" {
		return nil, errors.New("MGR recovery and Router bootstrap credentials are required")
	}
	managementUser, _ := s.architectureManagementAccount(ctx)
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return ids, errors.New("preferred MGR primary not found")
	}
	primaryClient := mysqlArchitectureClient(architectureRootPassword(req, primary.MachineID), primary.Port)
	instances, err := s.mgrInstances(ctx, req)
	if err != nil {
		return ids, fmt.Errorf("load MGR member versions for account grants: %w", err)
	}
	primaryVersion := instances[primary.MachineID].Version
	if strings.TrimSpace(primaryVersion) == "" {
		primaryVersion, _ = mysqlapp.PackageVersion(instances[primary.MachineID].PackageName)
	}
	dynamicGrant := strings.Join(mgrDynamicAdminPrivileges(primaryVersion), ",")
	primaryAccountCommand := mgrBootstrapAccountCommand(primaryClient, user, req.ReplicationPassword, managementUser, dynamicGrant, primary.Port)
	created, err := s.runOneArchitectureCommand(ctx, machines[primary.MachineID], primaryAccountCommand)
	ids = append(ids, created...)
	if err != nil {
		return ids, fmt.Errorf("prepare MGR recovery and Router account on preferred primary: %w", err)
	}
	for _, node := range req.Nodes {
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		recoverySQL := fmt.Sprintf(
			"CHANGE REPLICATION SOURCE TO SOURCE_USER=%s,SOURCE_PASSWORD=%s FOR CHANNEL 'group_replication_recovery';",
			sqlLiteral(user), sqlLiteral(req.ReplicationPassword),
		)
		legacyRecoverySQL := fmt.Sprintf(
			"CHANGE MASTER TO MASTER_USER=%s,MASTER_PASSWORD=%s FOR CHANNEL 'group_replication_recovery';",
			sqlLiteral(user), sqlLiteral(req.ReplicationPassword),
		)
		command := "(" + client + " --execute='STOP GROUP_REPLICATION' >/dev/null 2>&1 || true); " +
			replicationStopResetShell(client) +
			"(" + client + " --execute=" + shellQuote(recoverySQL) + " || " + client + " --execute=" + shellQuote(legacyRecoverySQL) + ")"
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("prepare MGR member %s: %w", node.MachineID, err)
		}
	}
	bootstrap := mgrBootstrapPrimaryCommand(primaryClient)
	created, err = s.runOneArchitectureCommand(ctx, machines[primary.MachineID], bootstrap)
	ids = append(ids, created...)
	if err != nil {
		return ids, fmt.Errorf("bootstrap preferred MGR primary: %w", err)
	}
	for _, node := range req.Nodes {
		if node.MachineID == primary.MachineID {
			continue
		}
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		join := client + " --execute='START GROUP_REPLICATION'; " +
			"for i in $(seq 1 180); do " + client + " --batch --skip-column-names --execute=" +
			shellQuote("SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid") +
			" | grep -Fxq ONLINE && exit 0; sleep 1; done; exit 1"
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], join)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("join MGR member %s: %w", node.MachineID, err)
		}
	}
	return ids, nil
}

func mgrBootstrapAccountCommand(primaryClient, user, password, managementUser, dynamicGrant string, port int) string {
	if strings.EqualFold(strings.TrimSpace(user), strings.TrimSpace(managementUser)) {
		return "MYSQL_PWD=" + shellQuote(password) + " mysql --protocol=tcp --host=127.0.0.1 --port=" +
			strconv.Itoa(port) + " --user=" + shellQuote(user) +
			" --connect-timeout=5 --batch --raw --execute='SELECT 1' >/dev/null"
	}
	account := sqlIdentifier(user) + "@'%'"
	accountSQL := fmt.Sprintf(
		"SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s; ALTER USER %s IDENTIFIED BY %s; GRANT ALL PRIVILEGES ON *.* TO %s WITH GRANT OPTION; GRANT %s ON *.* TO %s WITH GRANT OPTION; FLUSH PRIVILEGES;",
		account, sqlLiteral(password), account, sqlLiteral(password), account, dynamicGrant, account,
	)
	managementGrantQuery := "SELECT CONCAT('GRANT " + dynamicGrant + " ON *.* TO ',QUOTE(User),'@',QUOTE(Host),' WITH GRANT OPTION;') FROM mysql.user WHERE User=" + sqlLiteral(managementUser)
	return primaryClient + " --batch --raw --execute=" + shellQuote(accountSQL) + "; " +
		"management_grants=$(" + primaryClient + " --batch --raw --skip-column-names --execute=" + shellQuote(managementGrantQuery) + "); " +
		"[ -z \"$management_grants\" ] || printf '%s\\n' \"$management_grants\" | " + primaryClient
}

func mgrDynamicAdminPrivileges(version string) []string {
	privileges := []string{
		"CONNECTION_ADMIN", "SYSTEM_VARIABLES_ADMIN", "REPLICATION_SLAVE_ADMIN",
		"BACKUP_ADMIN", "CLONE_ADMIN", "GROUP_REPLICATION_ADMIN", "PERSIST_RO_VARIABLES_ADMIN",
	}
	if mysqlapp.SupportsDynamicPrivilegeForVersion(version, "REPLICATION_APPLIER") {
		privileges = append(privileges, "REPLICATION_APPLIER")
	}
	return privileges
}

func mgrBootstrapPrimaryCommand(primaryClient string) string {
	return primaryClient + " --execute='SET GLOBAL group_replication_bootstrap_group=ON'; " +
		"if ! " + primaryClient + " --execute='START GROUP_REPLICATION'; then " +
		primaryClient + " --execute='SET GLOBAL group_replication_bootstrap_group=OFF' || true; exit 1; fi; " +
		primaryClient + " --execute='SET GLOBAL group_replication_bootstrap_group=OFF'; " +
		"for i in $(seq 1 90); do " + primaryClient + " --batch --skip-column-names --execute=" +
		shellQuote("SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid") +
		" | grep -Fxq ONLINE && exit 0; sleep 1; done; exit 1"
}

func (s *HAService) switchMGRPrimary(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	node, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
	sql := "SELECT IF((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid)='PRIMARY','ALREADY_PRIMARY',group_replication_set_as_primary(@@server_uuid));"
	return s.runOneArchitectureCommand(ctx, machines[node.MachineID], client+" --batch --raw --execute="+shellQuote(sql))
}

func (s *HAService) verifyMGRGroup(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	client := mysqlArchitectureClient(architectureRootPassword(req, primary.MachineID), primary.Port)
	expected := len(req.Nodes)
	sql := fmt.Sprintf(
		"SELECT IF(COUNT(*)=%d AND SUM(MEMBER_STATE='ONLINE')=%d AND SUM(MEMBER_ROLE='PRIMARY')=1 AND COALESCE((SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid),'')='PRIMARY','MGR_OK','MGR_BAD') FROM performance_schema.replication_group_members;",
		expected, expected,
	)
	command := "for i in $(seq 1 120); do " + mysqlArchitectureCommand(architectureRootPassword(req, primary.MachineID), primary.Port, sql) +
		" | grep -Fxq MGR_OK && queues=$(" + client + " --batch --skip-column-names --execute=" +
		shellQuote("SELECT COALESCE(SUM(COUNT_TRANSACTIONS_IN_QUEUE+COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE),0) FROM performance_schema.replication_group_member_stats") +
		") && [ \"$queues\" = 0 ] && exit 0; sleep 1; done; exit 1"
	ids, err := s.runOneArchitectureCommand(ctx, machines[primary.MachineID], command)
	if err != nil {
		return ids, fmt.Errorf("MGR membership or apply-queue verification failed: %w", err)
	}
	return ids, nil
}

func (s *HAService) adoptMGRMetadata(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	clusterName := safeMGRClusterName(clusterID)
	clusterJSON, _ := json.Marshal(clusterName)
	script := "var c; try { c=dba.getCluster(); } catch(e) { c=dba.createCluster(" + string(clusterJSON) + ",{adoptFromGR:true}); } print(JSON.stringify(c.status({extended:1})));"
	findShell := `if [ -x /opt/gmha/mysql-shell/current/bin/mysqlsh ]; then mysqlsh_bin=/opt/gmha/mysql-shell/current/bin/mysqlsh; else mysqlsh_bin=$(command -v mysqlsh || find /opt /usr/local -type f -path '*/bin/mysqlsh' -perm -111 -print -quit 2>/dev/null); fi; [ -n "$mysqlsh_bin" ]`
	command := "set -eu; " + findShell +
		`; mysqlsh_home=$(mktemp -d /tmp/gmha-mysqlsh.XXXXXX); chmod 700 "$mysqlsh_home"; trap 'rm -rf "$mysqlsh_home"' EXIT; ` +
		"HOME=\"$mysqlsh_home\" MYSQLSH_USER_CONFIG_HOME=\"$mysqlsh_home\" MYSQLSH_TERM_COLOR_MODE=nocolor \"$mysqlsh_bin\" --js --uri " +
		shellQuote(fmt.Sprintf("%s@127.0.0.1:%d", url.User(req.ReplicationUser).String(), primary.Port)) +
		" --password=" + shellQuote(req.ReplicationPassword) + " --execute " + shellQuote(script)
	ids, err := s.runOneArchitectureCommand(ctx, machines[primary.MachineID], command)
	if err != nil {
		return ids, fmt.Errorf("adopt MGR as InnoDB Cluster metadata: %w", err)
	}
	return ids, nil
}

func safeMGRClusterName(clusterID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(clusterID)))
	return "gmha_" + hex.EncodeToString(sum[:8])
}

// CleanupMGRRouterNode removes only the per-cluster Router service and
// configuration. The shared binaries stay in place because another managed
// cluster or an administrator may still be using them.
func (s *HAService) CleanupMGRRouterNode(ctx context.Context, clusterID string, machine machinedomain.Machine) ([]string, error) {
	return s.runOneArchitectureCommand(ctx, machine, mysqlRouterCleanupCommand(clusterID))
}

func mysqlRouterCleanupCommand(clusterID string) string {
	safeName := safeMGRClusterName(clusterID)
	unit := "mysqlrouter-" + safeName
	configDir := "/etc/mysqlrouter/" + safeName
	return fmt.Sprintf(`set -eu
unit=%s
config_dir=%s
if command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now "$unit" 2>/dev/null || true
fi
rm -f -- "/etc/systemd/system/${unit}.service"
if [ -d "$config_dir" ]; then
  rm -rf -- "$config_dir"
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
fi`, shellQuote(unit), shellQuote(configDir))
}

type routerArtifacts struct {
	X8664   PackageItem
	AArch64 PackageItem
}

func (s *HAService) resolveMGRToolArtifacts(category string, serverVersions ...string) (routerArtifacts, error) {
	if s.packages == nil {
		return routerArtifacts{}, fmt.Errorf("%s package repository is not configured", category)
	}
	items, err := s.packages.List(category, "")
	if err != nil {
		return routerArtifacts{}, err
	}
	serverVersion := ""
	if len(serverVersions) > 0 {
		serverVersion = strings.TrimSpace(serverVersions[0])
	}
	var result routerArtifacts
	for _, item := range items {
		if !validSHA256Hex(item.SHA256) {
			continue
		}
		if category == "mysql-shell" && serverVersion != "" && !sameMajorMinorRelease(item.Version, serverVersion) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Arch)) {
		case "x86_64", "amd64":
			if result.X8664.Name == "" || comparePackageRelease(item.Version, result.X8664.Version) > 0 {
				result.X8664 = item
			}
		case "aarch64", "arm64":
			if result.AArch64.Name == "" || comparePackageRelease(item.Version, result.AArch64.Version) > 0 {
				result.AArch64 = item
			}
		}
	}
	if result.X8664.Name == "" && result.AArch64.Name == "" {
		if category == "mysql-shell" && serverVersion != "" {
			return result, fmt.Errorf("no SHA256-verified MySQL Shell artifact matching MySQL %s is available", majorMinorRelease(serverVersion))
		}
		return result, fmt.Errorf("no SHA256-verified x86_64 or aarch64 %s artifact is available", category)
	}
	return result, nil
}

func majorMinorRelease(version string) string {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 {
		return strings.TrimSpace(version)
	}
	return parts[0] + "." + parts[1]
}

func sameMajorMinorRelease(left, right string) bool {
	leftSeries, rightSeries := majorMinorRelease(left), majorMinorRelease(right)
	return leftSeries != "" && leftSeries == rightSeries
}

func validSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (s *HAService) resolveRouterArtifacts() (routerArtifacts, error) {
	return s.resolveMGRToolArtifacts("mysql-router")
}

func (s *HAService) deployMySQLShell(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	instances, err := s.mgrInstances(ctx, req)
	if err != nil {
		return nil, err
	}
	primaryVersion := instances[primary.MachineID].Version
	if strings.TrimSpace(primaryVersion) == "" {
		primaryVersion, _ = mysqlapp.PackageVersion(instances[primary.MachineID].PackageName)
	}
	artifacts, err := s.resolveMGRToolArtifacts("mysql-shell", primaryVersion)
	if err != nil {
		return nil, err
	}
	machine := machines[primary.MachineID]
	base := ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machine.IP)
	x86URL, armURL, x86SHA, armSHA := "", "", "", ""
	if artifacts.X8664.Name != "" {
		x86URL = strings.TrimRight(base, "/") + "/api/v1/software/packages/mysql-shell/" + url.PathEscape(artifacts.X8664.Name)
		x86SHA = artifacts.X8664.SHA256
	}
	if artifacts.AArch64.Name != "" {
		armURL = strings.TrimRight(base, "/") + "/api/v1/software/packages/mysql-shell/" + url.PathEscape(artifacts.AArch64.Name)
		armSHA = artifacts.AArch64.SHA256
	}
	command := mysqlShellDeployCommand(x86URL, armURL, x86SHA, armSHA)
	ids, err := s.runOneArchitectureCommand(ctx, machine, command)
	if err != nil {
		return ids, fmt.Errorf("deploy MySQL Shell on %s: %w", primary.MachineID, err)
	}
	return ids, nil
}

func mysqlShellDeployCommand(x86URL, armURL, x86SHA, armSHA string) string {
	return fmt.Sprintf(`set -eu
case "$(uname -m)" in
  x86_64|amd64) package_url=%s; package_sha=%s ;;
  aarch64|arm64) package_url=%s; package_sha=%s ;;
  *) echo "unsupported MySQL Shell architecture: $(uname -m)" >&2; exit 74 ;;
esac
[ -n "$package_url" ] || { echo "no MySQL Shell package for $(uname -m)" >&2; exit 75; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
archive="$work/mysql-shell.pkg"
if command -v curl >/dev/null 2>&1; then curl -fsSL "$package_url" -o "$archive"; else wget -qO "$archive" "$package_url"; fi
if [ -n "$package_sha" ]; then printf '%%s  %%s\n' "$package_sha" "$archive" | sha256sum -c -; fi
mkdir -p "$work/extract" /opt/gmha/mysql-shell
tar -xf "$archive" -C "$work/extract"
mysqlsh_bin=$(find "$work/extract" -type f -path '*/bin/mysqlsh' -perm -111 -print -quit)
[ -n "$mysqlsh_bin" ]
root_dir=${mysqlsh_bin%%/bin/mysqlsh}
target="/opt/gmha/mysql-shell/$(basename "$root_dir")"
rm -rf "$target"
mv "$root_dir" "$target"
ln -sfn "$target" /opt/gmha/mysql-shell/current
/opt/gmha/mysql-shell/current/bin/mysqlsh --version`,
		shellQuote(x86URL), shellQuote(x86SHA),
		shellQuote(armURL), shellQuote(armSHA),
	)
}

func (s *HAService) deployMGRRouters(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	artifacts, err := s.resolveRouterArtifacts()
	if err != nil {
		return nil, err
	}
	primary, ok := architectureNode(req.Nodes, req.PreferredNewMasterMachineID)
	if !ok {
		return nil, errors.New("preferred MGR primary not found")
	}
	var ids []string
	for _, node := range req.Nodes {
		base := ResolveManagerHTTPAddrForTarget(s.managerHTTPAddr, machines[node.MachineID].IP)
		x86URL, armURL, x86SHA, armSHA := "", "", "", ""
		if artifacts.X8664.Name != "" {
			x86URL = strings.TrimRight(base, "/") + "/api/v1/software/packages/mysql-router/" + url.PathEscape(artifacts.X8664.Name)
			x86SHA = artifacts.X8664.SHA256
		}
		if artifacts.AArch64.Name != "" {
			armURL = strings.TrimRight(base, "/") + "/api/v1/software/packages/mysql-router/" + url.PathEscape(artifacts.AArch64.Name)
			armSHA = artifacts.AArch64.SHA256
		}
		command := mysqlRouterDeployCommand(clusterID, req, node, machines[primary.MachineID], primary.Port, x86URL, armURL, x86SHA, armSHA)
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if runErr != nil {
			return ids, fmt.Errorf("deploy MySQL Router on %s: %w", node.MachineID, runErr)
		}
	}
	return ids, nil
}

func mysqlRouterDeployCommand(
	clusterID string,
	req hadomain.ArchitectureAdjustmentRequest,
	node hadomain.ArchitectureNodeRequest,
	primary machinedomain.Machine,
	primaryPort int,
	x86URL, armURL, x86SHA, armSHA string,
) string {
	routerName := safeMGRClusterName(clusterID) + "_" + shortMachineID(node.MachineID)
	configDir := "/etc/mysqlrouter/" + safeMGRClusterName(clusterID)
	unit := "mysqlrouter-" + safeMGRClusterName(clusterID)
	service := fmt.Sprintf(`[Unit]
Description=MySQL Router for %s
After=network-online.target

[Service]
Type=simple
User=mysqlrouter
Group=mysqlrouter
ExecStart=/opt/gmha/mysql-router/current/bin/mysqlrouter -c %s/mysqlrouter.conf
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, safeMGRClusterName(clusterID), configDir)
	return fmt.Sprintf(`set -eu
case "$(uname -m)" in
  x86_64|amd64) package_url=%s; package_sha=%s ;;
  aarch64|arm64) package_url=%s; package_sha=%s ;;
  *) echo "unsupported Router architecture: $(uname -m)" >&2; exit 74 ;;
esac
[ -n "$package_url" ] || { echo "no Router package for $(uname -m)" >&2; exit 75; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
archive="$work/router.pkg"
if command -v curl >/dev/null 2>&1; then curl -fsSL "$package_url" -o "$archive"; else wget -qO "$archive" "$package_url"; fi
if [ -n "$package_sha" ]; then printf '%%s  %%s\n' "$package_sha" "$archive" | sha256sum -c -; fi
mkdir -p "$work/extract" /opt/gmha/mysql-router
case "$package_url" in *.zip) command -v unzip >/dev/null && unzip -q "$archive" -d "$work/extract" ;; *) tar -xf "$archive" -C "$work/extract" ;; esac
router_bin=$(find "$work/extract" -type f -path '*/bin/mysqlrouter' -perm -111 -print -quit)
[ -n "$router_bin" ] || { echo "mysqlrouter binary not found in package" >&2; exit 76; }
root_dir=$(dirname "$(dirname "$router_bin")")
[ -d "$root_dir" ] || { echo "invalid Router package root" >&2; exit 77; }
release_name=$(basename "$root_dir")
[ -n "$release_name" ] && [ "$release_name" != "." ] && [ "$release_name" != "/" ] || { echo "invalid Router release directory" >&2; exit 78; }
target="/opt/gmha/mysql-router/$release_name"
install_tmp="${target}.new.$$"
rm -rf -- "$install_tmp"
cp -a -- "$root_dir" "$install_tmp"
rm -rf -- "$target"
mv -- "$install_tmp" "$target"
ln -sfn "$target" /opt/gmha/mysql-router/current
id mysqlrouter >/dev/null 2>&1 || useradd --system --home-dir /var/lib/mysqlrouter --shell /sbin/nologin mysqlrouter
systemctl stop %s 2>/dev/null || true
config_dir=%s
rm -rf -- "$config_dir"
install -d -o mysqlrouter -g mysqlrouter "$config_dir"
printf '%%s\n' %s | /opt/gmha/mysql-router/current/bin/mysqlrouter --bootstrap %s --password-retries=1 --directory %s --name %s --user=mysqlrouter --conf-base-port=%d --force
printf '%%s' %s > /etc/systemd/system/%s.service
chown -R mysqlrouter:mysqlrouter %s
systemctl daemon-reload
systemctl enable --now %s
systemctl is-active --quiet %s`,
		shellQuote(x86URL),
		shellQuote(x86SHA),
		shellQuote(armURL),
		shellQuote(armSHA),
		shellQuote(unit),
		shellQuote(configDir),
		shellQuote(req.ReplicationPassword),
		shellQuote(url.User(req.ReplicationUser).String()+"@"+net.JoinHostPort(primary.IP, strconv.Itoa(primaryPort))),
		shellQuote(configDir),
		shellQuote(routerName),
		req.RouterPort,
		shellQuote(service),
		shellQuote(unit),
		shellQuote(configDir),
		shellQuote(unit),
		shellQuote(unit),
	)
}

func shortMachineID(machineID string) string {
	sum := sha256.Sum256([]byte(machineID))
	return hex.EncodeToString(sum[:4])
}

func (s *HAService) verifyMGRRouters(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	var ids []string
	for _, node := range req.Nodes {
		mysql := "MYSQL_PWD=" + shellQuote(req.ReplicationPassword) + " mysql --protocol=tcp --host=127.0.0.1 --user=" +
			shellQuote(req.ReplicationUser) + " --connect-timeout=5"
		rw := mysql + " --port=" + strconv.Itoa(req.RouterPort) + " --batch --skip-column-names --execute=" +
			shellQuote("SELECT MEMBER_ROLE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid")
		ro := mysql + " --port=" + strconv.Itoa(req.RouterPort+1) + " --batch --skip-column-names --execute=" +
			shellQuote("SELECT IF(@@read_only=1,'RO_OK','RO_BAD')")
		command := "for i in $(seq 1 60); do " + rw + " 2>/dev/null | grep -Fxq PRIMARY && " + ro +
			" 2>/dev/null | grep -Fxq RO_OK && exit 0; sleep 1; done; exit 1"
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("Router verification failed on %s ports %d/%d: %w", node.MachineID, req.RouterPort, req.RouterPort+1, err)
		}
	}
	return ids, nil
}
