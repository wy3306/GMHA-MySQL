package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
)

const topologyRecoveryInterval = 30 * time.Second

// StartTopologyRecovery starts the credential-free desired-state reconciler.
// It never rebuilds replication metadata or forces a primary. Async channels
// are merely restarted after their configured source is verified; MGR complete
// outage recovery is delegated to AdminAPI's GTID-aware election.
func (s *HAService) StartTopologyRecovery() {
	s.topologyRecoveryMu.Lock()
	defer s.topologyRecoveryMu.Unlock()
	if s.topologyRecoveryStop != nil || s.tasks == nil {
		return
	}
	s.topologyRecoveryStop = make(chan struct{})
	s.topologyRecoveryDone = make(chan struct{})
	s.topologyRecoveryLast = make(map[string]time.Time)
	baseCtx, cancel := context.WithCancel(context.Background())
	s.topologyRecoveryCancel = cancel
	go func(stop <-chan struct{}, done chan<- struct{}) {
		defer close(done)
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-baseCtx.Done():
				return
			case <-timer.C:
				runCtx, runCancel := context.WithTimeout(baseCtx, 20*time.Minute)
				s.reconcileTopologyRecovery(runCtx)
				runCancel()
				timer.Reset(topologyRecoveryInterval)
			}
		}
	}(s.topologyRecoveryStop, s.topologyRecoveryDone)
}

func (s *HAService) StopTopologyRecovery() {
	s.topologyRecoveryMu.Lock()
	stop, done, cancel := s.topologyRecoveryStop, s.topologyRecoveryDone, s.topologyRecoveryCancel
	s.topologyRecoveryStop, s.topologyRecoveryDone = nil, nil
	s.topologyRecoveryCancel = nil
	s.topologyRecoveryMu.Unlock()
	if stop != nil {
		cancel()
		close(stop)
		<-done
	}
}

func (s *HAService) reconcileTopologyRecovery(ctx context.Context) {
	repo, ok := s.repo.(topologyIntentRepository)
	if !ok {
		return
	}
	intents, err := repo.ListTopologyIntents(ctx)
	if err != nil {
		return
	}
	for _, intent := range intents {
		if !s.topologyRecoveryDue(intent.ClusterID) {
			continue
		}
		var recovered bool
		switch intent.Architecture {
		case hadomain.ArchitectureMGRRouter:
			recovered = s.reconcileMGRIntent(ctx, intent) == nil
		case hadomain.ArchitectureMasterSlave, hadomain.ArchitectureDualMaster, hadomain.ArchitectureMultiMaster:
			recovered = s.reconcileAsyncIntent(ctx, intent) == nil
		default:
			continue
		}
		if recovered {
			s.markTopologyRecoveryAttempt(intent.ClusterID, 2*time.Minute)
		} else {
			s.markTopologyRecoveryAttempt(intent.ClusterID, time.Minute)
		}
	}
}

func (s *HAService) topologyRecoveryDue(clusterID string) bool {
	s.topologyRecoveryMu.Lock()
	defer s.topologyRecoveryMu.Unlock()
	return time.Now().After(s.topologyRecoveryLast[clusterID])
}

func (s *HAService) markTopologyRecoveryAttempt(clusterID string, delay time.Duration) {
	s.topologyRecoveryMu.Lock()
	s.topologyRecoveryLast[clusterID] = time.Now().Add(delay)
	s.topologyRecoveryMu.Unlock()
}

func (s *HAService) reconcileMGRIntent(ctx context.Context, intent hadomain.TopologyIntent) error {
	status, targets, err := s.collectMGRManagementStatus(ctx, intent.ClusterID)
	if err != nil {
		return err
	}
	if !status.Available {
		return errors.New("desired MGR members are not yet discoverable")
	}
	if status.Healthy {
		return nil
	}
	if status.OnlineMemberCount != 0 || len(targets) != len(intent.Nodes) {
		return errors.New("MGR is not a complete outage or not every desired member is registered")
	}
	for _, target := range targets {
		if !target.member.Reachable {
			return fmt.Errorf("MGR member %s is not reachable", target.machine.ID)
		}
		if intent.MGRGroupName != "" && target.member.GroupName != intent.MGRGroupName {
			return fmt.Errorf("MGR group mismatch on %s", target.machine.ID)
		}
	}
	return s.withMGRManagementLock(ctx, intent.ClusterID, "automatic_complete_outage_recovery", func(operationCtx context.Context) error {
		status, targets, err = s.collectMGRManagementStatus(operationCtx, intent.ClusterID)
		if err != nil || status.OnlineMemberCount != 0 {
			return err
		}
		seed := targets[0]
		for _, target := range targets {
			if target.machine.ID == intent.PrimaryMachineID {
				seed = target
				break
			}
		}
		username, password := s.architectureManagementAccount(operationCtx)
		clusterJSON := strconv.Quote(safeMGRClusterName(intent.ClusterID))
		script := `var c=dba.rebootClusterFromCompleteOutage(` + clusterJSON + `); print("` + mgrJSONMarker + `"+JSON.stringify({status:c.status({extended:1})}));`
		if _, _, err := s.runMGRTask(operationCtx, seed.machine, "mgr_automatic_complete_outage_recovery", "自动恢复 MGR 全组停机", mysqlShellMGRCommand(username, password, seed.instance.Port, script), 10*time.Minute); err != nil {
			return err
		}
		refreshed, refreshedTargets, err := s.collectMGRManagementStatus(operationCtx, intent.ClusterID)
		if err != nil {
			return err
		}
		online, ok := mgrOnlineSeed(refreshedTargets)
		if !ok {
			return errors.New("MGR recovery did not produce an ONLINE member")
		}
		_, _, err = s.runMGRTask(operationCtx, online.machine, "mgr_verify_automatic_recovery", "验证自动恢复后的 MGR 数据一致性",
			mgrRecoveredGroupVerifyCommand(username, password, online.instance.Port, refreshed.GroupName, len(intent.Nodes)), 4*time.Minute)
		return err
	})
}

func (s *HAService) reconcileAsyncIntent(ctx context.Context, intent hadomain.TopologyIntent) error {
	machines, err := s.machines.List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]machinedomain.Machine, len(machines))
	for _, machine := range machines {
		if machine.Cluster == intent.ClusterID {
			byID[machine.ID] = machine
		}
	}
	username, password := s.architectureManagementAccount(ctx)
	nodeByID := make(map[string]hadomain.ArchitectureNodeRequest, len(intent.Nodes))
	for _, node := range intent.Nodes {
		nodeByID[node.MachineID] = node
	}
	for _, node := range intent.Nodes {
		if strings.TrimSpace(node.SourceMachineID) == "" {
			continue
		}
		target, source := byID[node.MachineID], byID[node.SourceMachineID]
		if target.ID == "" || source.ID == "" {
			return errors.New("desired async topology references a missing machine")
		}
		port := node.Port
		if port <= 0 {
			port = 3306
		}
		sourcePort := nodeByID[node.SourceMachineID].Port
		if sourcePort <= 0 {
			sourcePort = 3306
		}
		command := asyncReplicationRecoveryCommand(username, password, port, source.IP, sourcePort, node.DelaySeconds)
		if _, _, err := s.runMGRTask(ctx, target, "async_replication_restart_recovery", "恢复并校验 MySQL 复制链路", command, 4*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

func asyncReplicationRecoveryCommand(username, password string, port int, sourceIP string, sourcePort int, expectedDelay int) string {
	client := mysqlMGRClient(username, password, port)
	status := client + " --execute='SHOW REPLICA STATUS\\G' 2>/dev/null || " + client + " --execute='SHOW SLAVE STATUS\\G' 2>/dev/null"
	gtidSQL := "SELECT IF(GTID_SUBSET(COALESCE((SELECT RECEIVED_TRANSACTION_SET FROM performance_schema.replication_connection_status LIMIT 1),''),@@GLOBAL.gtid_executed),'YES','NO')"
	successCheck := "[ \"$lag\" = 0 ] && [ \"$applied\" = YES ]"
	successMarker := "ASYNC_REPLICATION_CONSISTENT"
	if expectedDelay > 0 {
		successCheck = "[ \"$configured_delay\" = " + strconv.Itoa(expectedDelay) + " ]"
		successMarker = "ASYNC_REPLICATION_DELAYED_HEALTHY"
	}
	return "set -eu; out=$(" + status + "); [ -n \"$out\" ] || { echo 'replication metadata is missing; refusing to rebuild without credentials' >&2; exit 78; }; " +
		"actual_host=$(printf '%s\\n' \"$out\" | awk -F': ' '/(Source_Host|Master_Host):/ {print $2; exit}'); " +
		"actual_port=$(printf '%s\\n' \"$out\" | awk -F': ' '/(Source_Port|Master_Port):/ {print $2; exit}'); " +
		"[ \"$actual_host\" = " + shellQuote(sourceIP) + " ] && [ \"$actual_port\" = " + strconv.Itoa(sourcePort) + " ] || { echo 'replication source differs from durable topology; refusing automatic start' >&2; exit 79; }; " +
		"(" + client + " --execute='START REPLICA' >/dev/null 2>&1 || " + client + " --execute='START SLAVE' >/dev/null 2>&1 || true); " +
		"for i in $(seq 1 120); do out=$(" + status + "); io=$(printf '%s\\n' \"$out\" | awk -F': ' '/(Replica_IO_Running|Slave_IO_Running):/ {print $2; exit}'); sql=$(printf '%s\\n' \"$out\" | awk -F': ' '/(Replica_SQL_Running|Slave_SQL_Running):/ {print $2; exit}'); lag=$(printf '%s\\n' \"$out\" | awk -F': ' '/Seconds_Behind_(Source|Master):/ {print $2; exit}'); configured_delay=$(printf '%s\\n' \"$out\" | awk -F': ' '/SQL_Delay:/ {print $2; exit}'); last_error=$(printf '%s\\n' \"$out\" | awk -F': ' '/Last_(IO|SQL)_Error:/ && length($2)>0 {print $2; exit}'); applied=$(" + client + " --batch --raw --skip-column-names --execute=" + shellQuote(gtidSQL) + " 2>/dev/null || true); [ \"$io\" = Yes ] && [ \"$sql\" = Yes ] && [ -z \"$last_error\" ] && " + successCheck + " && { echo " + successMarker + "; exit 0; }; sleep 1; done; echo 'replication consistency verification timed out' >&2; exit 1"
}
