package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
)

type architectureRunRepository interface {
	SaveArchitectureRun(context.Context, hadomain.ArchitectureRun) error
	GetArchitectureRun(context.Context, string, string) (hadomain.ArchitectureRun, bool, error)
}

type architectureRunRecoveryRepository interface {
	MarkInterruptedArchitectureRuns(context.Context) error
}

// StartArchitectureAdjustment 创建异步、严格串行的在线架构调整运行实例。
func (s *HAService) StartArchitectureAdjustment(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest) (hadomain.ArchitectureRun, error) {
	if s.tasks == nil {
		return hadomain.ArchitectureRun{}, errors.New("architecture executor is not configured")
	}
	runs, ok := s.repo.(architectureRunRepository)
	if !ok {
		return hadomain.ArchitectureRun{}, errors.New("architecture run repository is not configured")
	}
	if strings.TrimSpace(req.RootPassword) == "" {
		return hadomain.ArchitectureRun{}, errors.New("root_password is required for online architecture adjustment")
	}
	if strings.TrimSpace(req.ReplicationUser) == "" || req.ReplicationPassword == "" {
		return hadomain.ArchitectureRun{}, errors.New("replication credentials are required")
	}
	plan, err := s.PlanArchitectureAdjustment(ctx, clusterID, req)
	if err != nil {
		return hadomain.ArchitectureRun{}, err
	}
	if !plan.Executable {
		return hadomain.ArchitectureRun{}, fmt.Errorf("architecture plan is blocked: %s", strings.Join(plan.BlockingReasons, "; "))
	}
	now := time.Now().UTC()
	safeRequest := req
	safeRequest.RootPassword = ""
	safeRequest.ReplicationPassword = ""
	run := hadomain.ArchitectureRun{
		RunID: "arch-run-" + strings.TrimPrefix(newFailoverID(), "fo-"), ClusterID: clusterID,
		Status: hadomain.ArchitectureRunPending, Plan: plan, Request: safeRequest,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := runs.SaveArchitectureRun(ctx, run); err != nil {
		return hadomain.ArchitectureRun{}, err
	}
	if err := s.tasks.CreateArchitectureTrackingTask(ctx, run); err != nil {
		return hadomain.ArchitectureRun{}, fmt.Errorf("create architecture tracking task: %w", err)
	}
	go s.executeArchitectureAdjustment(context.Background(), runs, run, req)
	return run, nil
}

func (s *HAService) GetArchitectureRun(ctx context.Context, clusterID, runID string) (hadomain.ArchitectureRun, bool, error) {
	runs, ok := s.repo.(architectureRunRepository)
	if !ok {
		return hadomain.ArchitectureRun{}, false, errors.New("architecture run repository is not configured")
	}
	return runs.GetArchitectureRun(ctx, clusterID, runID)
}

func (s *HAService) ConfirmArchitectureForce(ctx context.Context, clusterID, runID string) (hadomain.ArchitectureRun, error) {
	runs, ok := s.repo.(architectureRunRepository)
	if !ok {
		return hadomain.ArchitectureRun{}, errors.New("architecture run repository is not configured")
	}
	run, found, err := runs.GetArchitectureRun(ctx, clusterID, runID)
	if err != nil {
		return hadomain.ArchitectureRun{}, err
	}
	if !found {
		return hadomain.ArchitectureRun{}, errors.New("architecture run not found")
	}
	if run.Status != hadomain.ArchitectureRunWaitingForce {
		return hadomain.ArchitectureRun{}, fmt.Errorf("run status %s does not accept force confirmation", run.Status)
	}
	run.ForceConfirmed = true
	run.UpdatedAt = time.Now().UTC()
	if err := runs.SaveArchitectureRun(ctx, run); err != nil {
		return hadomain.ArchitectureRun{}, err
	}
	return run, nil
}

func (s *HAService) executeArchitectureAdjustment(ctx context.Context, runs architectureRunRepository, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest) {
	const lockTTL = 5 * time.Minute
	if err := s.repo.AcquireFailoverLock(ctx, run.ClusterID, run.RunID, "gmha-architecture", lockTTL); err != nil {
		s.failArchitectureRun(ctx, runs, &run, "acquire_lock", err)
		return
	}
	defer func() { _ = s.repo.ReleaseFailoverLock(context.Background(), run.ClusterID, run.RunID) }()
	executionCtx, cancelExecution := context.WithCancel(ctx)
	defer cancelExecution()
	lockErrors := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if err := s.repo.RenewFailoverLock(context.Background(), run.ClusterID, run.RunID, lockTTL); err != nil {
					select {
					case lockErrors <- err:
					default:
					}
					cancelExecution()
					return
				}
			}
		}
	}()
	ctx = executionCtx
	run.Status = hadomain.ArchitectureRunRunning
	run.UpdatedAt = time.Now().UTC()
	_ = runs.SaveArchitectureRun(ctx, run)
	s.syncArchitectureTrackingTask(ctx, run)

	machines, err := s.architectureMachines(ctx, req)
	if err != nil {
		s.failArchitectureRun(ctx, runs, &run, "preflight", err)
		return
	}
	if err := s.addClusterMachines(ctx, run.ClusterID, machines); err != nil {
		s.failArchitectureRun(ctx, runs, &run, "preflight", err)
		return
	}
	if err := s.runArchitectureStep(ctx, runs, &run, "preflight", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return mysqlArchitectureCommand(req.RootPassword, node.Port, "SELECT @@hostname,@@port,@@server_id,@@read_only,@@super_read_only,@@global.gtid_mode; SELECT COUNT(*) FROM performance_schema.threads;")
		})
	}); err != nil {
		return
	}
	var elected hadomain.CandidateScore
	if err := s.runArchitectureStep(ctx, runs, &run, "elect_candidate", func() ([]string, error) {
		candidate, taskIDs, electionErr := s.electLiveArchitectureCandidate(ctx, run.ClusterID, req, machines)
		elected = candidate
		return taskIDs, electionErr
	}); err != nil {
		return
	}
	run.Plan.SelectedCandidate = elected
	run.UpdatedAt = time.Now().UTC()
	_ = runs.SaveArchitectureRun(ctx, run)
	if req.MoveVIP {
		if err := s.runArchitectureStep(ctx, runs, &run, "check_vip_conflict", func() ([]string, error) {
			return s.checkArchitectureVIPConflict(ctx, run, req, machines)
		}); err != nil {
			return
		}
	}

	if req.CurrentMasterMachineID != "" && req.CurrentMasterMachineID != run.Plan.SelectedCandidate.MachineID {
		oldNode, ok := architectureNode(req.Nodes, req.CurrentMasterMachineID)
		if !ok {
			s.failArchitectureRun(ctx, runs, &run, "freeze_old_master", errors.New("current master node is not in request"))
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "freeze_old_master", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], mysqlArchitectureCommand(req.RootPassword, oldNode.Port, "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT @@read_only,@@super_read_only;"))
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "kill_business_sessions", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], killBusinessSessionsCommand(req, oldNode.Port))
		}); err != nil {
			return
		}

		candidateNode, _ := architectureNode(req.Nodes, run.Plan.SelectedCandidate.MachineID)
		err = s.runArchitectureStep(ctx, runs, &run, "wait_replication_zero", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[candidateNode.MachineID], replicationCatchupCommand(req.RootPassword, candidateNode.Port))
		})
		if err != nil {
			run.Status = hadomain.ArchitectureRunWaitingForce
			run.CurrentStep = "force_gate"
			run.Error = "replication did not reach zero lag and complete relay/GTID replay within 60 seconds; explicit force confirmation is required"
			run.FinishedAt = nil
			run.UpdatedAt = time.Now().UTC()
			_ = runs.SaveArchitectureRun(ctx, run)
			s.syncArchitectureTrackingTask(ctx, run)
			if !waitArchitectureForce(ctx, runs, run.ClusterID, run.RunID) {
				select {
				case lockErr := <-lockErrors:
					s.failArchitectureRun(context.Background(), runs, &run, "renew_lock", lockErr)
				default:
				}
				return
			}
			run.ForceConfirmed = true
			run.Status = hadomain.ArchitectureRunRunning
			run.Error = ""
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "fence_old_master", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], mysqlArchitectureCommand(req.RootPassword, oldNode.Port, "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@read_only=1 AND @@super_read_only=1,'FENCED','NOT_FENCED');"))
		}); err != nil {
			return
		}
	}

	if err := s.runArchitectureStep(ctx, runs, &run, "promote_new_master", func() ([]string, error) {
		node, _ := architectureNode(req.Nodes, run.Plan.SelectedCandidate.MachineID)
		client := mysqlArchitectureClient(req.RootPassword, node.Port)
		command := client + " --execute='STOP REPLICA' >/dev/null 2>&1 || true; " + client + " --execute='RESET REPLICA ALL' >/dev/null 2>&1 || true; " + client + " --batch --raw --execute=" + shellQuote("SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; SELECT @@read_only,@@super_read_only;")
		return s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
	}); err != nil {
		return
	}

	repointErr := s.runArchitectureStep(ctx, runs, &run, "repoint_replicas", func() ([]string, error) {
		return s.configureArchitectureTopology(ctx, req, run.Plan.SelectedCandidate.MachineID, machines)
	})
	if repointErr != nil && !run.ForceConfirmed {
		return
	}
	verifyErr := repointErr
	if verifyErr == nil {
		verifyErr = s.runArchitectureStep(ctx, runs, &run, "verify_topology", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return verifyArchitectureNodeCommand(req, node, run.Plan.SelectedCandidate.MachineID)
			})
		})
	}
	if verifyErr != nil {
		if !run.ForceConfirmed {
			return
		}
		resumeArchitectureRun(ctx, runs, &run)
		if err := s.runArchitectureStep(ctx, runs, &run, "pt_repair_on_failure", func() ([]string, error) {
			return s.repairArchitectureReplication(ctx, req, run.Plan.SelectedCandidate.MachineID, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_topology", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return verifyArchitectureNodeCommand(req, node, run.Plan.SelectedCandidate.MachineID)
			})
		}); err != nil {
			return
		}
	}
	if req.MoveVIP {
		if err := s.runArchitectureStep(ctx, runs, &run, "move_vip", func() ([]string, error) {
			return s.moveArchitectureVIP(ctx, run, req, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_single_vip", func() ([]string, error) {
			return s.verifyArchitectureVIP(ctx, run, req, machines)
		}); err != nil {
			return
		}
	}

	select {
	case lockErr := <-lockErrors:
		s.failArchitectureRun(context.Background(), runs, &run, "renew_lock", lockErr)
		return
	default:
	}
	now := time.Now().UTC()
	run.Status, run.CurrentStep, run.Error = hadomain.ArchitectureRunSucceeded, "release_lock", ""
	run.UpdatedAt, run.FinishedAt = now, &now
	_ = runs.SaveArchitectureRun(ctx, run)
	s.syncArchitectureTrackingTask(ctx, run)
}

func (s *HAService) electLiveArchitectureCandidate(ctx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) (hadomain.CandidateScore, []string, error) {
	type liveCandidate struct {
		score        hadomain.CandidateScore
		transactions uint64
	}
	candidates := make([]liveCandidate, 0, len(req.Nodes))
	serverIDs := make(map[int]string)
	nodeGTIDSets := make(map[string]string, len(req.Nodes))
	var taskIDs []string
	for _, node := range req.Nodes {
		probeSQL := "SELECT CONCAT_WS('|',@@server_id,@@global.gtid_mode,@@global.gtid_executed,@@read_only,@@super_read_only);"
		id, output, err := s.runOneArchitectureProbe(ctx, machines[node.MachineID], mysqlArchitectureCommand(req.RootPassword, node.Port, probeSQL))
		if id != "" {
			taskIDs = append(taskIDs, id)
		}
		if err != nil {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("live election probe failed on %s: %w", node.MachineID, err)
		}
		fields := strings.Split(strings.TrimSpace(output), "|")
		if len(fields) < 5 {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("live election received malformed MySQL state from %s", node.MachineID)
		}
		serverID, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil || serverID <= 0 {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("live election received invalid server_id from %s", node.MachineID)
		}
		if other, duplicate := serverIDs[serverID]; duplicate {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("live election rejected duplicate server_id %d on %s and %s", serverID, other, node.MachineID)
		}
		serverIDs[serverID] = node.MachineID
		nodeGTIDSets[node.MachineID] = fields[2]
		if !strings.EqualFold(node.Role, "M") || node.DelaySeconds > 0 || (node.MachineID == req.CurrentMasterMachineID && req.PreferredNewMasterMachineID != req.CurrentMasterMachineID) {
			continue
		}
		if !strings.EqualFold(fields[1], "ON") {
			continue
		}
		priority := node.ElectionPriority
		if node.MachineID == req.PreferredNewMasterMachineID {
			priority += 1_000_000
		}
		score := hadomain.CandidateScore{ClusterID: clusterID, MachineID: node.MachineID, Hostname: machines[node.MachineID].Name, IP: machines[node.MachineID].IP, Port: node.Port, Eligible: true, GTIDMode: strings.EqualFold(fields[1], "ON"), ExecutedGTIDSet: fields[2], ElectionPriority: priority, HealthScore: 100, CanBindVIP: true}
		score.InstanceID = fmt.Sprintf("%s:%d", node.MachineID, node.Port)
		candidates = append(candidates, liveCandidate{score: score, transactions: gtidTransactionCount(fields[2])})
	}
	if len(candidates) == 0 {
		return hadomain.CandidateScore{}, taskIDs, errors.New("live election found no healthy target-master candidate")
	}
	// Reject divergent histories. Comparing only transaction counts can elect a
	// branch that is numerically newer but does not contain another candidate.
	supersetCandidates := candidates[:0]
	for _, candidate := range candidates {
		containsAll := true
		for _, other := range candidates {
			if !gtidSetSubset(other.score.ExecutedGTIDSet, candidate.score.ExecutedGTIDSet) {
				containsAll = false
				break
			}
		}
		if containsAll {
			supersetCandidates = append(supersetCandidates, candidate)
		}
	}
	if len(supersetCandidates) == 0 {
		return hadomain.CandidateScore{}, taskIDs, errors.New("live election rejected divergent GTID histories; no candidate contains all eligible transaction sets")
	}
	candidates = supersetCandidates
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].transactions != candidates[j].transactions {
			return candidates[i].transactions > candidates[j].transactions
		}
		return candidates[i].score.ElectionPriority > candidates[j].score.ElectionPriority
	})
	selected := candidates[0].score
	for _, node := range req.Nodes {
		if node.MachineID == req.CurrentMasterMachineID || node.MachineID == selected.MachineID || !strings.EqualFold(node.Role, "S") {
			continue
		}
		if !gtidSetSubset(nodeGTIDSets[node.MachineID], selected.ExecutedGTIDSet) {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("target replica %s has divergent GTID history; clone or rebuild it before assigning replication", node.MachineID)
		}
	}
	selected.DataFreshnessScore = 100
	selected.FinalScore = selected.HealthScore + selected.ElectionPriority
	return selected, taskIDs, nil
}

type gtidInterval struct{ start, end uint64 }

func parseGTIDSet(set string) map[string][]gtidInterval {
	result := make(map[string][]gtidInterval)
	for _, source := range strings.Split(set, ",") {
		parts := strings.Split(strings.TrimSpace(source), ":")
		if len(parts) < 2 {
			continue
		}
		uuid := strings.ToLower(parts[0])
		for _, raw := range parts[1:] {
			bounds := strings.SplitN(raw, "-", 2)
			start, err := strconv.ParseUint(bounds[0], 10, 64)
			if err != nil {
				continue
			}
			end := start
			if len(bounds) == 2 {
				end, err = strconv.ParseUint(bounds[1], 10, 64)
				if err != nil || end < start {
					continue
				}
			}
			result[uuid] = append(result[uuid], gtidInterval{start: start, end: end})
		}
	}
	for uuid, intervals := range result {
		sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
		merged := make([]gtidInterval, 0, len(intervals))
		for _, interval := range intervals {
			if len(merged) == 0 || interval.start > merged[len(merged)-1].end+1 {
				merged = append(merged, interval)
				continue
			}
			if interval.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = interval.end
			}
		}
		result[uuid] = merged
	}
	return result
}

func gtidSetSubset(subset, superset string) bool {
	if strings.TrimSpace(subset) == "" {
		return true
	}
	small, large := parseGTIDSet(subset), parseGTIDSet(superset)
	for uuid, intervals := range small {
		for _, interval := range intervals {
			contained := false
			for _, candidate := range large[uuid] {
				if candidate.start <= interval.start && candidate.end >= interval.end {
					contained = true
					break
				}
			}
			if !contained {
				return false
			}
		}
	}
	return true
}

func gtidTransactionCount(gtidSet string) uint64 {
	var total uint64
	for _, source := range strings.Split(gtidSet, ",") {
		parts := strings.Split(strings.TrimSpace(source), ":")
		for _, interval := range parts[1:] {
			bounds := strings.SplitN(interval, "-", 2)
			start, err := strconv.ParseUint(bounds[0], 10, 64)
			if err != nil {
				continue
			}
			end := start
			if len(bounds) == 2 {
				if parsed, parseErr := strconv.ParseUint(bounds[1], 10, 64); parseErr == nil {
					end = parsed
				}
			}
			if end >= start {
				total += end - start + 1
			}
		}
	}
	return total
}

func verifyArchitectureNodeCommand(req hadomain.ArchitectureAdjustmentRequest, node hadomain.ArchitectureNodeRequest, selectedPrimaryID string) string {
	isDualMaster := req.Architecture != hadomain.ArchitectureMasterSlave && strings.EqualFold(node.Role, "M")
	if node.MachineID == selectedPrimaryID && !isDualMaster {
		return mysqlArchitectureCommand(req.RootPassword, node.Port, "SELECT IF(@@read_only=0 AND @@super_read_only=0,'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
	}
	expectedReadOnly := "1"
	if isDualMaster {
		expectedReadOnly = "0"
	}
	if node.DelaySeconds > 0 && !isDualMaster {
		return delayedReplicationHealthCommand(req.RootPassword, node.Port, node.DelaySeconds) + " && " + mysqlArchitectureCommand(req.RootPassword, node.Port, "SELECT IF(@@read_only=1,'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
	}
	return replicationCatchupCommand(req.RootPassword, node.Port) + " && " + mysqlArchitectureCommand(req.RootPassword, node.Port, "SELECT IF(@@read_only="+expectedReadOnly+",'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
}

func resumeArchitectureRun(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun) {
	run.Status, run.Error, run.FinishedAt = hadomain.ArchitectureRunRunning, "", nil
	run.UpdatedAt = time.Now().UTC()
	_ = runs.SaveArchitectureRun(ctx, *run)
}

func (s *HAService) repairArchitectureReplication(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, primaryID string, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, primaryID)
	if !ok {
		return nil, errors.New("repair primary node not found")
	}
	var ids []string
	configured, err := s.configureArchitectureTopology(ctx, req, primaryID, machines)
	ids = append(ids, configured...)
	if err != nil {
		return ids, fmt.Errorf("replication reconfiguration failed before PT repair: %w", err)
	}
	// Install and version-check the Toolkit on every participating node. The
	// primary runs pt-table-checksum while replicas run restart/sync tools.
	for _, node := range req.Nodes {
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], installCompatiblePTCommand(req.RootPassword, node.Port))
		ids = append(ids, created...)
		if runErr != nil {
			return ids, runErr
		}
	}
	// First attempt to clear replication SQL errors. Data synchronization is
	// performed only after a checksum table has been generated on the primary.
	for _, node := range req.Nodes {
		if node.MachineID == primaryID && req.Architecture == hadomain.ArchitectureMasterSlave {
			continue
		}
		options := fmt.Sprintf("--host=127.0.0.1 --port=%d --user=root --password=%s", node.Port, shellQuote(req.RootPassword))
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], "pt-replica-restart "+options+" --max-sleep=5 --run-time=60s")
		ids = append(ids, created...)
		if runErr != nil {
			return ids, fmt.Errorf("PT replica restart failed on %s: %w", machines[node.MachineID].Name, runErr)
		}
	}
	checksumOptions := fmt.Sprintf("--host=127.0.0.1 --port=%d --user=root --password=%s", primary.Port, shellQuote(req.RootPassword))
	checksumCommand := "pt-table-checksum " + checksumOptions + " --replicate=percona.checksums --create-replicate-table --no-check-replication-filters"
	created, err := s.runOneArchitectureCommand(ctx, machines[primaryID], checksumCommand)
	ids = append(ids, created...)
	if err != nil {
		return ids, err
	}
	for _, node := range req.Nodes {
		if node.MachineID == primaryID && req.Architecture == hadomain.ArchitectureMasterSlave {
			continue
		}
		options := fmt.Sprintf("--host=127.0.0.1 --port=%d --user=root --password=%s", node.Port, shellQuote(req.RootPassword))
		syncCommand := "pt-table-sync " + options + " --execute --replicate=percona.checksums --sync-to-source --no-check-triggers"
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], syncCommand)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("PT checksum synchronization failed on %s: %w", machines[node.MachineID].Name, err)
		}
	}
	// Recompute checksums after synchronization; the final query below must see
	// no differing rows on any replica before the run can continue.
	created, err = s.runOneArchitectureCommand(ctx, machines[primaryID], checksumCommand)
	ids = append(ids, created...)
	if err != nil {
		return ids, err
	}
	for _, node := range req.Nodes {
		if node.MachineID == primaryID && req.Architecture == hadomain.ArchitectureMasterSlave {
			continue
		}
		check := mysqlArchitectureCommand(req.RootPassword, node.Port, "SELECT IF(COUNT(*)=0,'PT_SYNC_OK','PT_SYNC_DIFF') FROM percona.checksums WHERE master_crc<>this_crc OR master_cnt<>this_cnt;") + " | grep -Fxq PT_SYNC_OK"
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], check)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("PT checksum still reports differences on %s: %w", machines[node.MachineID].Name, err)
		}
	}
	return ids, nil
}

func installCompatiblePTCommand(rootPassword string, port int) string {
	client := mysqlArchitectureClient(rootPassword, port)
	return "mysql_version=$(" + client + " --batch --skip-column-names --execute='SELECT VERSION()' | cut -d- -f1); case \"$mysql_version\" in 8.0.*|8.4.*) min_pt=3.7.1 ;; *) echo \"unsupported MySQL version for automatic PT repair: $mysql_version\" >&2; exit 76 ;; esac; " +
		"if ! command -v pt-table-sync >/dev/null 2>&1; then if command -v apt-get >/dev/null 2>&1; then curl -fsSL https://repo.percona.com/apt/percona-release_latest.generic_all.deb -o /tmp/percona-release.deb && dpkg -i /tmp/percona-release.deb && percona-release enable tools release && DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y percona-toolkit; elif command -v dnf >/dev/null 2>&1; then dnf install -y https://repo.percona.com/yum/percona-release-latest.noarch.rpm && percona-release enable tools release && dnf install -y percona-toolkit; elif command -v yum >/dev/null 2>&1; then yum install -y https://repo.percona.com/yum/percona-release-latest.noarch.rpm && percona-release enable tools release && yum install -y percona-toolkit; else exit 77; fi; fi; " +
		"pt_version=$(pt-table-sync --version | awk '{print $NF}'); [ \"$(printf '%s\\n' \"$min_pt\" \"$pt_version\" | sort -V | head -n1)\" = \"$min_pt\" ] || { echo \"Percona Toolkit $pt_version is below required $min_pt\" >&2; exit 78; }; command -v pt-replica-restart >/dev/null; command -v pt-table-checksum >/dev/null"
}

func (s *HAService) checkArchitectureVIPConflict(ctx context.Context, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, vip := range configs {
		holders := make([]string, 0, 1)
		for _, machine := range sortedArchitectureMachines(machines) {
			command := "if ip -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq " + shellQuote(vip.VIPAddress) + "; then echo BOUND; else echo UNBOUND; fi"
			id, output, probeErr := s.runOneArchitectureProbe(ctx, machine, command)
			if id != "" {
				ids = append(ids, id)
			}
			if probeErr != nil {
				return ids, probeErr
			}
			if strings.TrimSpace(output) == "BOUND" {
				holders = append(holders, machine.ID)
			}
		}
		if len(holders) > 1 {
			return ids, fmt.Errorf("split-brain detected: VIP %s is present on %d nodes (%s)", vip.VIPAddress, len(holders), strings.Join(holders, ","))
		}
		if len(holders) == 1 && req.CurrentMasterMachineID != "" && holders[0] != req.CurrentMasterMachineID {
			return ids, fmt.Errorf("VIP %s is held by %s instead of declared current master %s", vip.VIPAddress, holders[0], req.CurrentMasterMachineID)
		}
	}
	return ids, nil
}

func (s *HAService) moveArchitectureVIP(ctx context.Context, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return nil, errors.New("no enabled VIP configuration found")
	}
	target := machines[run.Plan.SelectedCandidate.MachineID]
	var ids []string
	for _, vip := range configs {
		mode := vip.VIPRouteMode
		if mode == "" {
			mode = run.Plan.VIPRouteMode
		}
		switch mode {
		case hadomain.VipRouteModeL2ARP:
			for _, machine := range sortedArchitectureMachines(machines) {
				created, runErr := s.runOneArchitectureCommand(ctx, machine, l2VIPRemoveCommand(vip))
				ids = append(ids, created...)
				if runErr != nil {
					return ids, fmt.Errorf("cannot prove VIP %s absent on %s: %w", vip.VIPAddress, machine.Name, runErr)
				}
			}
			created, runErr := s.runOneArchitectureCommand(ctx, target, l2VIPBindCommand(vip))
			ids = append(ids, created...)
			if runErr != nil {
				return ids, runErr
			}
		case hadomain.VipRouteModeBGP:
			for _, machine := range sortedArchitectureMachines(machines) {
				created, runErr := s.runOneArchitectureCommand(ctx, machine, bgpVIPWithdrawCommand(vip))
				ids = append(ids, created...)
				if runErr != nil {
					return ids, fmt.Errorf("BGP withdrawal failed on %s: %w", machine.Name, runErr)
				}
			}
			created, runErr := s.runOneArchitectureCommand(ctx, target, bgpVIPAnnounceCommand(vip, target))
			ids = append(ids, created...)
			if runErr != nil {
				return ids, runErr
			}
		case hadomain.VipRouteModeKeepalived:
			created, runErr := s.moveKeepalivedVIP(ctx, vip, req, run.Plan.SelectedCandidate.MachineID, machines)
			ids = append(ids, created...)
			if runErr != nil {
				return ids, runErr
			}
		default:
			return ids, fmt.Errorf("unsupported VIP route mode %s", mode)
		}
		_ = s.repo.UpsertVIPBindingState(ctx, hadomain.VIPBindingState{ClusterID: run.ClusterID, VIPConfigID: vip.ID, VIPAddress: vip.VIPAddress, ExpectedHolderInstanceID: run.Plan.SelectedCandidate.InstanceID, ExpectedHolderMachineID: target.ID, CurrentHolderInstanceID: run.Plan.SelectedCandidate.InstanceID, CurrentHolderMachineID: target.ID, CurrentInterface: vip.DefaultInterface, VIPStatus: hadomain.VipStatusBound, DetectedHolders: target.ID, LastCheckResult: "single holder verified by ordered remove-before-bind"})
	}
	return ids, nil
}

func bgpVIPWithdrawCommand(vip hadomain.ClusterVIPConfig) string {
	prefix := vip.VIPAddress + "/32"
	return fmt.Sprintf("command -v vtysh >/dev/null 2>&1 || exit 73; vtysh -c 'configure terminal' -c %s -c 'address-family ipv4 unicast' -c %s -c 'end' -c 'write memory'; ip addr del %s dev lo 2>/dev/null || true; ! ip -o addr show | awk '{print $4}' | grep -Fxq %s", shellQuote(fmt.Sprintf("router bgp %d", vip.BGPLocalAS)), shellQuote("no network "+prefix), shellQuote(prefix), shellQuote(prefix))
}

func bgpVIPAnnounceCommand(vip hadomain.ClusterVIPConfig, target machinedomain.Machine) string {
	prefix := vip.VIPAddress + "/32"
	routerID := vip.BGPRouterID
	if routerID == "" {
		routerID = target.IP
	}
	commands := []string{"configure terminal", fmt.Sprintf("router bgp %d", vip.BGPLocalAS), "bgp router-id " + routerID, "neighbor " + vip.BGPPeerAddress + " remote-as " + fmt.Sprint(vip.BGPPeerAS), "address-family ipv4 unicast"}
	if vip.BGPCommunity != "" {
		commands = append(commands, "exit-address-family", "route-map GMHA-VIP permit 10", "set community "+vip.BGPCommunity, "router bgp "+fmt.Sprint(vip.BGPLocalAS), "address-family ipv4 unicast", "network "+prefix+" route-map GMHA-VIP")
	} else {
		commands = append(commands, "network "+prefix)
	}
	commands = append(commands, "end", "write memory")
	var vty strings.Builder
	vty.WriteString("vtysh")
	for _, command := range commands {
		vty.WriteString(" -c ")
		vty.WriteString(shellQuote(command))
	}
	return "command -v vtysh >/dev/null 2>&1 || exit 73; ip addr add " + shellQuote(prefix) + " dev lo; ip link set lo up; " + vty.String() + "; vtysh -c " + shellQuote("show bgp ipv4 unicast neighbors "+vip.BGPPeerAddress) + " | grep -Fq 'BGP state = Established'; vtysh -c " + shellQuote("show bgp ipv4 unicast neighbors "+vip.BGPPeerAddress+" advertised-routes") + " | grep -Fq " + shellQuote(prefix)
}

func (s *HAService) moveKeepalivedVIP(ctx context.Context, vip hadomain.ClusterVIPConfig, req hadomain.ArchitectureAdjustmentRequest, targetID string, machines map[string]machinedomain.Machine) ([]string, error) {
	masters := make([]hadomain.ArchitectureNodeRequest, 0, 2)
	for _, node := range req.Nodes {
		if strings.EqualFold(node.Role, "M") {
			masters = append(masters, node)
		}
	}
	if len(masters) != 2 {
		return nil, errors.New("Keepalived requires exactly two target master nodes")
	}
	var ids []string
	// Stop all possible holders and prove the address absent before installing a new VRRP state.
	for _, machine := range sortedArchitectureMachines(machines) {
		command := "systemctl stop keepalived 2>/dev/null || true; " + l2VIPRemoveCommand(vip)
		created, err := s.runOneArchitectureCommand(ctx, machine, command)
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	for _, node := range masters {
		peer := masters[0]
		if peer.MachineID == node.MachineID {
			peer = masters[1]
		}
		priority := 80
		if node.MachineID == targetID {
			priority = 150
		}
		command := keepalivedInstallCommand(vip, node, machines[node.MachineID], machines[peer.MachineID], req.RootPassword, priority)
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	// Start the promoted node first; a lower-priority peer therefore cannot acquire the VIP during rollout.
	created, err := s.runOneArchitectureCommand(ctx, machines[targetID], "systemctl enable keepalived; systemctl restart keepalived; sleep 3")
	ids = append(ids, created...)
	if err != nil {
		return ids, err
	}
	for _, node := range masters {
		if node.MachineID == targetID {
			continue
		}
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], "systemctl enable keepalived; systemctl restart keepalived; sleep 2")
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func keepalivedInstallCommand(vip hadomain.ClusterVIPConfig, node hadomain.ArchitectureNodeRequest, machine, peer machinedomain.Machine, rootPassword string, priority int) string {
	iface := strings.TrimSpace(vip.DefaultInterface)
	if iface == "" {
		iface = "eth0"
	}
	vrid := int(sha256.Sum256([]byte(vip.ClusterID + "|" + vip.VIPAddress))[0])%250 + 1
	auth := fmt.Sprintf("gm%06d", vrid)
	clientPassword := strings.ReplaceAll(strings.ReplaceAll(rootPassword, "\\", "\\\\"), "\"", "\\\"")
	client := "[client]\nuser=root\npassword=\"" + clientPassword + "\"\n"
	checkPath := fmt.Sprintf("/usr/local/libexec/gmha-keepalived-%d", node.Port)
	check := fmt.Sprintf("#!/bin/sh\nvalue=$(mysql --defaults-extra-file=/etc/gmha/keepalived-mysql.cnf --protocol=tcp --host=127.0.0.1 --port=%d --batch --skip-column-names --execute='SELECT @@read_only' 2>/dev/null) || exit 1\n[ \"$value\" = 0 ]\n", node.Port)
	config := fmt.Sprintf("global_defs {\n  enable_script_security\n  script_user root\n}\nvrrp_script chk_gmha_mysql {\n  script \"%s\"\n  interval 2\n  timeout 2\n  fall 2\n  rise 2\n}\nvrrp_instance GMHA_%d {\n  state BACKUP\n  interface %s\n  virtual_router_id %d\n  priority %d\n  advert_int 1\n  authentication { auth_type PASS auth_pass %s }\n  unicast_src_ip %s\n  unicast_peer { %s }\n  virtual_ipaddress { %s/%d dev %s }\n  track_script { chk_gmha_mysql }\n  garp_master_delay 1\n  garp_master_repeat 5\n}\n", checkPath, vrid, iface, vrid, priority, auth, machine.IP, peer.IP, vip.VIPAddress, vip.VIPPrefix, iface)
	write := func(path, content string, mode int) string {
		return fmt.Sprintf("printf %%s %s | base64 -d > %s; chmod %o %s", shellQuote(base64.StdEncoding.EncodeToString([]byte(content))), shellQuote(path), mode, shellQuote(path))
	}
	install := "command -v keepalived >/dev/null 2>&1 || { if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y keepalived; elif command -v dnf >/dev/null 2>&1; then dnf install -y keepalived; elif command -v yum >/dev/null 2>&1; then yum install -y keepalived; else exit 74; fi; }"
	return install + "; install -d -m 0700 /etc/gmha; install -d -m 0755 /usr/local/libexec; " + write("/etc/gmha/keepalived-mysql.cnf", client, 600) + "; " + write(checkPath, check, 700) + "; " + write("/etc/keepalived/keepalived.conf", config, 600) + "; keepalived -t -f /etc/keepalived/keepalived.conf"
}

func (s *HAService) verifyArchitectureVIP(ctx context.Context, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, vip := range configs {
		for _, machine := range sortedArchitectureMachines(machines) {
			expectBound := machine.ID == run.Plan.SelectedCandidate.MachineID
			check := "if ip -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq " + shellQuote(vip.VIPAddress) + "; then bound=1; else bound=0; fi; "
			if expectBound {
				check += "[ \"$bound\" = 1 ]"
			} else {
				check += "[ \"$bound\" = 0 ]"
			}
			created, runErr := s.runOneArchitectureCommand(ctx, machine, check)
			ids = append(ids, created...)
			if runErr != nil {
				return ids, fmt.Errorf("VIP %s single-holder verification failed on %s", vip.VIPAddress, machine.Name)
			}
		}
	}
	return ids, nil
}

func l2VIPInterface(vip hadomain.ClusterVIPConfig) string {
	if strings.TrimSpace(vip.DefaultInterface) != "" {
		return shellQuote(vip.DefaultInterface)
	}
	return "$(ip route get " + shellQuote(vip.VIPAddress) + " | awk '{for(i=1;i<=NF;i++) if($i==\"dev\"){print $(i+1); exit}}')"
}

func l2VIPRemoveCommand(vip hadomain.ClusterVIPConfig) string {
	iface := l2VIPInterface(vip)
	return fmt.Sprintf("iface=%s; [ -n \"$iface\" ] || exit 71; ip addr del %s/%d dev \"$iface\" 2>/dev/null || true; ! ip -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq %s", iface, shellQuote(vip.VIPAddress), vip.VIPPrefix, shellQuote(vip.VIPAddress))
}

func l2VIPBindCommand(vip hadomain.ClusterVIPConfig) string {
	iface := l2VIPInterface(vip)
	count := vip.ArpingCount
	if count <= 0 {
		count = 3
	}
	return fmt.Sprintf("iface=%s; [ -n \"$iface\" ] || exit 71; ip addr add %s/%d dev \"$iface\"; ip link set \"$iface\" up; command -v arping >/dev/null 2>&1 || exit 72; arping -U -c %d -I \"$iface\" %s; ip -o addr show dev \"$iface\" | awk '{print $4}' | cut -d/ -f1 | grep -Fxq %s", iface, shellQuote(vip.VIPAddress), vip.VIPPrefix, count, shellQuote(vip.VIPAddress), shellQuote(vip.VIPAddress))
}

func (s *HAService) architectureMachines(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest) (map[string]machinedomain.Machine, error) {
	out := make(map[string]machinedomain.Machine, len(req.Nodes))
	for _, node := range req.Nodes {
		machine, ok, err := s.machines.GetByID(ctx, node.MachineID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("machine %s not found", node.MachineID)
		}
		out[node.MachineID] = machine
	}
	return out, nil
}

func (s *HAService) addClusterMachines(ctx context.Context, clusterID string, target map[string]machinedomain.Machine) error {
	machines, err := s.machines.List(ctx)
	if err != nil {
		return err
	}
	for _, machine := range machines {
		if machine.Cluster == clusterID {
			target[machine.ID] = machine
		}
	}
	return nil
}

func sortedArchitectureMachines(machines map[string]machinedomain.Machine) []machinedomain.Machine {
	ids := make([]string, 0, len(machines))
	for id := range machines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]machinedomain.Machine, 0, len(ids))
	for _, id := range ids {
		result = append(result, machines[id])
	}
	return result
}

func (s *HAService) runArchitectureStep(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun, code string, execute func() ([]string, error)) error {
	step := hadomain.ArchitectureRunStepResult{Code: code, Name: architectureStepName(run.Plan.Steps, code), Status: "running", StartedAt: time.Now().UTC()}
	run.CurrentStep, run.UpdatedAt = code, time.Now().UTC()
	run.StepResults = append(run.StepResults, step)
	_ = runs.SaveArchitectureRun(ctx, *run)
	s.syncArchitectureTrackingTask(ctx, *run)
	taskIDs, err := execute()
	finished := time.Now().UTC()
	index := len(run.StepResults) - 1
	run.StepResults[index].TaskIDs = taskIDs
	run.StepResults[index].FinishedAt = &finished
	run.TaskIDs = append(run.TaskIDs, taskIDs...)
	if err != nil {
		run.StepResults[index].Status = "failed"
		run.StepResults[index].Message = err.Error()
		s.failArchitectureRun(ctx, runs, run, code, err)
		return err
	}
	run.StepResults[index].Status = "success"
	run.UpdatedAt = finished
	if err := runs.SaveArchitectureRun(ctx, *run); err != nil {
		return err
	}
	s.syncArchitectureTrackingTask(ctx, *run)
	return nil
}

func (s *HAService) runOneArchitectureCommand(ctx context.Context, machine machinedomain.Machine, command string) ([]string, error) {
	id, _, err := s.runOneArchitectureProbe(ctx, machine, command)
	if id == "" {
		return nil, err
	}
	return []string{id}, err
}

func (s *HAService) runOneArchitectureProbe(ctx context.Context, machine machinedomain.Machine, command string) (string, string, error) {
	detail, err := s.tasks.CreateExecTaskWithOptions(ctx, machine.IP, command, ExecTaskOptions{
		Operation: "mysql_architecture_step", DisplayName: "MySQL 架构调整子任务", StepName: "执行数据库架构调整命令",
	})
	if err != nil {
		return "", "", err
	}
	completed, err := s.tasks.WaitForTask(ctx, detail.Task.ID, 2*time.Minute)
	if err != nil {
		return detail.Task.ID, "", err
	}
	defer func() { _ = s.tasks.RedactExecTaskCommand(context.Background(), detail.Task.ID) }()
	output := ""
	if len(completed.Steps) > 0 {
		output = completed.Steps[len(completed.Steps)-1].Message
	}
	if completed.Task.Status != taskdomain.StatusSuccess {
		return detail.Task.ID, output, fmt.Errorf("agent task %s failed", detail.Task.ID)
	}
	return detail.Task.ID, output, nil
}

func (s *HAService) runOnArchitectureNodes(ctx context.Context, nodes []hadomain.ArchitectureNodeRequest, machines map[string]machinedomain.Machine, command func(hadomain.ArchitectureNodeRequest, machinedomain.Machine) string) ([]string, error) {
	var ids []string
	for _, node := range nodes {
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command(node, machines[node.MachineID]))
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func (s *HAService) configureArchitectureTopology(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, primaryID string, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, primaryID)
	if !ok {
		return nil, errors.New("selected primary node not found")
	}
	var ids []string
	masters := make([]hadomain.ArchitectureNodeRequest, 0, 2)
	for _, node := range req.Nodes {
		if strings.EqualFold(node.Role, "M") {
			masters = append(masters, node)
			accountSQL := fmt.Sprintf("CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED BY %s; ALTER USER %s@'%%' IDENTIFIED BY %s; GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO %s@'%%';", sqlIdentifier(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), sqlIdentifier(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), sqlIdentifier(req.ReplicationUser))
			created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], mysqlArchitectureCommand(req.RootPassword, node.Port, accountSQL))
			ids = append(ids, created...)
			if err != nil {
				return ids, err
			}
		}
	}
	for _, node := range req.Nodes {
		source := primary
		isMaster := strings.EqualFold(node.Role, "M")
		if len(masters) > 1 && isMaster {
			masterIndex := 0
			for index, master := range masters {
				if master.MachineID == node.MachineID {
					masterIndex = index
					break
				}
			}
			source = masters[(masterIndex-1+len(masters))%len(masters)]
			if node.SourceMachineID != "" {
				if requestedSource, found := architectureNode(req.Nodes, node.SourceMachineID); found && strings.EqualFold(requestedSource.Role, "M") && requestedSource.MachineID != node.MachineID {
					source = requestedSource
				}
			}
		} else if node.MachineID == primaryID {
			continue
		} else if node.SourceMachineID != "" {
			if item, found := architectureNode(req.Nodes, node.SourceMachineID); found {
				source = item
			}
		}
		sourceMachine := machines[source.MachineID]
		client := mysqlArchitectureClient(req.RootPassword, node.Port)
		reset := client + " --execute='STOP REPLICA' >/dev/null 2>&1 || true; " + client + " --execute='RESET REPLICA ALL' >/dev/null 2>&1 || true; "
		sql := fmt.Sprintf("CHANGE REPLICATION SOURCE TO SOURCE_HOST=%s,SOURCE_PORT=%d,SOURCE_USER=%s,SOURCE_PASSWORD=%s,SOURCE_AUTO_POSITION=1,SOURCE_DELAY=%d; START REPLICA;", sqlLiteral(sourceMachine.IP), source.Port, sqlLiteral(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), node.DelaySeconds)
		if isMaster {
			offset := 1
			for index, master := range masters {
				if master.MachineID == node.MachineID {
					offset = index + 1
					break
				}
			}
			sql += fmt.Sprintf(" SET PERSIST auto_increment_increment=%d; SET PERSIST auto_increment_offset=%d; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;", len(masters), offset)
		} else {
			sql += " SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;"
		}
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], reset+client+" --batch --raw --execute="+shellQuote(sql))
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func mysqlArchitectureCommand(password string, port int, sql string) string {
	return mysqlArchitectureClient(password, port) + " --batch --raw --execute=" + shellQuote(sql)
}

func mysqlArchitectureClient(password string, port int) string {
	if port <= 0 {
		port = 3306
	}
	return fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=root --connect-timeout=5", shellQuote(password), port)
}

func killBusinessSessionsCommand(req hadomain.ArchitectureAdjustmentRequest, port int) string {
	users := append([]string{"root", "mysql.sys", "mysql.session", "mysql.infoschema", req.ReplicationUser}, req.ManagementUsers...)
	seen, literals := map[string]bool{}, make([]string, 0, len(users))
	for _, user := range users {
		if user = strings.TrimSpace(user); user != "" && !seen[user] {
			seen[user] = true
			literals = append(literals, sqlLiteral(user))
		}
	}
	query := "SELECT CONCAT('KILL CONNECTION ',ID,';') FROM information_schema.PROCESSLIST WHERE ID<>CONNECTION_ID() AND USER NOT IN (" + strings.Join(literals, ",") + ")"
	client := fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=root --connect-timeout=5", shellQuote(req.RootPassword), port)
	return client + " --batch --skip-column-names --execute=" + shellQuote(query) + " | " + client
}

func replicationCatchupCommand(password string, port int) string {
	client := fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=root --connect-timeout=5 --batch --skip-column-names", shellQuote(password), port)
	gtidSQL := "SELECT IF(GTID_SUBSET(COALESCE((SELECT RECEIVED_TRANSACTION_SET FROM performance_schema.replication_connection_status LIMIT 1),''),@@GLOBAL.gtid_executed),'YES','NO')"
	return "i=0; while [ $i -lt 60 ]; do status=$(" + client + " -e 'SHOW REPLICA STATUS\\G' 2>/dev/null) || exit 70; lag=$(printf '%s\\n' \"$status\" | awk -F': ' '/Seconds_Behind_(Source|Master)/ {print $2; exit}'); io=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_IO_Running|Slave_IO_Running/ {print $2; exit}'); sql=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_SQL_Running|Slave_SQL_Running/ {print $2; exit}'); gtid=$(" + client + " -e " + shellQuote(gtidSQL) + " 2>/dev/null) || exit 70; if [ \"$lag\" = 0 ] && [ \"$io\" = Yes ] && [ \"$sql\" = Yes ] && [ \"$gtid\" = YES ]; then echo GMHA_REPLICATION_CAUGHT_UP; exit 0; fi; i=$((i+1)); sleep 1; done; echo GMHA_REPLICATION_TIMEOUT >&2; exit 75"
}

func delayedReplicationHealthCommand(password string, port, expectedDelay int) string {
	client := fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=root --connect-timeout=5 --batch --skip-column-names", shellQuote(password), port)
	return "status=$(" + client + " -e 'SHOW REPLICA STATUS\\G' 2>/dev/null) || exit 70; configured=$(printf '%s\\n' \"$status\" | awk -F': ' '/SQL_Delay/ {print $2; exit}'); io=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_IO_Running|Slave_IO_Running/ {print $2; exit}'); sql=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_SQL_Running|Slave_SQL_Running/ {print $2; exit}'); [ \"$configured\" = " + fmt.Sprint(expectedDelay) + " ] && [ \"$io\" = Yes ] && [ \"$sql\" = Yes ]"
}

func sqlLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func sqlIdentifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }

func architectureNode(nodes []hadomain.ArchitectureNodeRequest, machineID string) (hadomain.ArchitectureNodeRequest, bool) {
	for _, node := range nodes {
		if node.MachineID == machineID {
			return node, true
		}
	}
	return hadomain.ArchitectureNodeRequest{}, false
}

func architectureStepName(steps []hadomain.ArchitecturePlanStep, code string) string {
	for _, step := range steps {
		if step.Code == code {
			return step.Name
		}
	}
	return code
}

func waitArchitectureForce(ctx context.Context, runs architectureRunRepository, clusterID, runID string) bool {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			run, ok, err := runs.GetArchitectureRun(ctx, clusterID, runID)
			if err != nil || !ok {
				return false
			}
			if run.ForceConfirmed {
				return true
			}
			if run.Status != hadomain.ArchitectureRunWaitingForce {
				return false
			}
		}
	}
}

func (s *HAService) failArchitectureRun(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun, step string, err error) {
	now := time.Now().UTC()
	run.Status, run.CurrentStep, run.Error = hadomain.ArchitectureRunFailed, step, err.Error()
	run.UpdatedAt, run.FinishedAt = now, &now
	if ctx.Err() != nil {
		ctx = context.Background()
	}
	_ = runs.SaveArchitectureRun(ctx, *run)
	s.syncArchitectureTrackingTask(ctx, *run)
}

func (s *HAService) syncArchitectureTrackingTask(ctx context.Context, run hadomain.ArchitectureRun) {
	if s.tasks == nil {
		return
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	_ = s.tasks.SyncArchitectureTrackingTask(ctx, run)
}
