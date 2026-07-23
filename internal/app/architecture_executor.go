package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
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
	return s.startArchitectureAdjustment(ctx, context.Background(), clusterID, req, "")
}

// startArchitectureAdjustmentWithLock lets a higher-level maintenance
// workflow retain the cluster lock across multiple architecture adjustments.
// The caller owns renewal and release of the supplied lock.
func (s *HAService) startArchitectureAdjustmentWithLock(ctx, executionCtx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, lockID string) (hadomain.ArchitectureRun, error) {
	if strings.TrimSpace(lockID) == "" {
		return hadomain.ArchitectureRun{}, errors.New("existing maintenance lock id is required")
	}
	return s.startArchitectureAdjustment(ctx, executionCtx, clusterID, req, strings.TrimSpace(lockID))
}

func (s *HAService) startArchitectureAdjustment(ctx, executionCtx context.Context, clusterID string, req hadomain.ArchitectureAdjustmentRequest, existingLockID string) (hadomain.ArchitectureRun, error) {
	if s.tasks == nil {
		return hadomain.ArchitectureRun{}, errors.New("architecture executor is not configured")
	}
	runs, ok := s.repo.(architectureRunRepository)
	if !ok {
		return hadomain.ArchitectureRun{}, errors.New("architecture run repository is not configured")
	}
	hasReplicationUser := strings.TrimSpace(req.ReplicationUser) != ""
	hasReplicationPassword := req.ReplicationPassword != ""
	if hasReplicationUser != hasReplicationPassword {
		return hadomain.ArchitectureRun{}, errors.New("replication_user and replication_password must be provided together")
	}
	if !hasReplicationUser {
		req.ReplicationUser, req.ReplicationPassword = s.architectureManagementAccount(ctx)
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
	safeRequest.RootPasswords = nil
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
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	go s.executeArchitectureAdjustment(executionCtx, runs, run, req, existingLockID)
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

func (s *HAService) executeArchitectureAdjustment(ctx context.Context, runs architectureRunRepository, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, existingLockID string) {
	const lockTTL = 5 * time.Minute
	ownsLock := strings.TrimSpace(existingLockID) == ""
	if ownsLock {
		if err := s.repo.AcquireFailoverLock(ctx, run.ClusterID, run.RunID, "gmha-architecture", lockTTL); err != nil {
			s.failArchitectureRun(ctx, runs, &run, "acquire_lock", err)
			return
		}
	}
	lockReleased := !ownsLock
	releaseLock := func() error {
		if lockReleased {
			return nil
		}
		if err := s.repo.ReleaseFailoverLock(context.Background(), run.ClusterID, run.RunID); err != nil {
			return err
		}
		lockReleased = true
		return nil
	}
	if ownsLock {
		defer func() { _ = releaseLock() }()
	}
	executionCtx, cancelExecution := context.WithCancel(ctx)
	defer cancelExecution()
	lockErrors := make(chan error, 1)
	if ownsLock {
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
	}
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
	if req.VIPOnly {
		businessFrozen := false
		defer func() {
			if businessFrozen {
				_, _ = s.resumeArchitectureBusinessConnections(context.Background(), req, machines)
			}
		}()
		if err := s.runArchitectureStep(ctx, runs, &run, "preflight", func() ([]string, error) {
			if strings.TrimSpace(req.PreferredNewMasterMachineID) == "" {
				return nil, errors.New("VIP target master is required")
			}
			if _, ok := machines[req.PreferredNewMasterMachineID]; !ok {
				return nil, errors.New("VIP target master is not an available cluster machine")
			}
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return architecturePreflightCommand(architectureRootPassword(req, node.MachineID), node.Port)
			})
		}); err != nil {
			return
		}
		if !req.InitializeVIP {
			if err := s.runArchitectureStep(ctx, runs, &run, "freeze_business_access", func() ([]string, error) {
				return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
					return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SET GLOBAL offline_mode=ON; SELECT IF(@@offline_mode=1,'BUSINESS_LOCKED','BUSINESS_OPEN');") + " | grep -Fxq BUSINESS_LOCKED"
				})
			}); err != nil {
				return
			}
			businessFrozen = true
			if err := s.runArchitectureStep(ctx, runs, &run, "drain_business_sessions", func() ([]string, error) {
				return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
					return killBusinessSessionsCommand(req, node.MachineID, node.Port)
				})
			}); err != nil {
				return
			}
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "check_vip_conflict", func() ([]string, error) {
			return s.checkArchitectureVIPConflict(ctx, run, req, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "withdraw_vip", func() ([]string, error) {
			return s.withdrawArchitectureVIP(ctx, run, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_zero_vip", func() ([]string, error) {
			return s.verifyArchitectureVIPZero(ctx, run, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "bind_vip", func() ([]string, error) {
			return s.bindArchitectureVIP(ctx, run, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_single_vip", func() ([]string, error) {
			return s.verifyArchitectureVIP(ctx, run, req, machines)
		}); err != nil {
			_, _ = s.withdrawArchitectureVIP(context.Background(), run, machines)
			return
		}
		if !req.InitializeVIP {
			if err := s.runArchitectureStep(ctx, runs, &run, "resume_business_connections", func() ([]string, error) {
				return s.resumeArchitectureBusinessConnections(ctx, req, machines)
			}); err != nil {
				return
			}
			businessFrozen = false
		}
		select {
		case lockErr := <-lockErrors:
			s.failArchitectureRun(context.Background(), runs, &run, "renew_lock", lockErr)
			return
		default:
		}
		if err := releaseLock(); err != nil {
			s.failArchitectureRun(ctx, runs, &run, "release_lock", err)
			return
		}
		s.succeedArchitectureRun(ctx, runs, &run)
		return
	}
	if strings.TrimSpace(req.RootPassword) != "" && len(req.RootPasswords) == 0 {
		managementUser, managementPassword := s.architectureManagementAccount(ctx)
		if err := s.runArchitectureStep(ctx, runs, &run, "repair_management_privileges", func() ([]string, error) {
			return s.repairArchitectureManagementPrivileges(ctx, req, machines, managementUser, managementPassword)
		}); err != nil {
			return
		}
		// Root is a one-time bootstrap credential. Every architecture operation
		// after this point must prove that the Agent-managed MHA account works.
		req.RootPassword = ""
	}
	if err := s.runArchitectureStep(ctx, runs, &run, "preflight", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return architecturePreflightCommand(architectureRootPassword(req, node.MachineID), node.Port)
		})
	}); err != nil {
		return
	}
	if transition := architectureTransitionKind(req); transition != "" {
		freezeNodes := make([]hadomain.ArchitectureNodeRequest, 0, len(req.Nodes))
		for _, node := range req.Nodes {
			if (transition == "dual_to_master_slave" && strings.EqualFold(node.Role, "S")) || (transition == "master_slave_to_dual" && node.MachineID == req.CurrentMasterMachineID) {
				freezeNodes = append(freezeNodes, node)
			}
		}
		if len(freezeNodes) == 0 {
			s.failArchitectureRun(ctx, runs, &run, "freeze_business_access", errors.New("cannot identify the nodes affected by topology conversion"))
			return
		}
		businessFrozen := false
		defer func() {
			if businessFrozen {
				_, _ = s.resumeArchitectureBusinessConnections(context.Background(), req, machines)
			}
		}()
		if err := s.runArchitectureStep(ctx, runs, &run, "freeze_business_access", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, freezeNodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SET GLOBAL offline_mode=ON; SELECT IF(@@offline_mode=1,'BUSINESS_LOCKED','BUSINESS_OPEN');") + " | grep -Fxq BUSINESS_LOCKED"
			})
		}); err != nil {
			return
		}
		businessFrozen = true
		if err := s.runArchitectureStep(ctx, runs, &run, "drain_business_sessions", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, freezeNodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return killBusinessSessionsCommand(req, node.MachineID, node.Port)
			})
		}); err != nil {
			return
		}
		catchupNodes := make([]hadomain.ArchitectureNodeRequest, 0, len(req.Nodes))
		for _, node := range req.Nodes {
			if (transition == "dual_to_master_slave" && strings.EqualFold(node.Role, "S")) || (transition == "master_slave_to_dual" && strings.EqualFold(node.Role, "M") && node.MachineID != req.CurrentMasterMachineID) {
				catchupNodes = append(catchupNodes, node)
			}
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "wait_replication_zero", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, catchupNodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return replicationCatchupCommand(architectureRootPassword(req, node.MachineID), node.Port)
			})
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "reconfigure_topology", func() ([]string, error) {
			return s.configureArchitectureTopology(ctx, req, req.PreferredNewMasterMachineID, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_topology", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return verifyArchitectureNodeCommand(req, node, req.PreferredNewMasterMachineID)
			})
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "pt_verify_replication", func() ([]string, error) {
			return s.verifyArchitectureDataWithPT(ctx, req, req.PreferredNewMasterMachineID, machines)
		}); err != nil {
			return
		}
		if req.MoveVIP {
			if err := s.runArchitectureStep(ctx, runs, &run, "check_vip_conflict", func() ([]string, error) { return s.checkArchitectureVIPConflict(ctx, run, req, machines) }); err != nil {
				return
			}
			if err := s.runArchitectureStep(ctx, runs, &run, "move_vip", func() ([]string, error) { return s.moveArchitectureVIP(ctx, run, req, machines) }); err != nil {
				return
			}
			if err := s.runArchitectureStep(ctx, runs, &run, "verify_single_vip", func() ([]string, error) { return s.verifyArchitectureVIP(ctx, run, req, machines) }); err != nil {
				return
			}
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "resume_business_connections", func() ([]string, error) {
			return s.resumeArchitectureBusinessConnections(ctx, req, machines)
		}); err != nil {
			return
		}
		businessFrozen = false
		if err := releaseLock(); err != nil {
			s.failArchitectureRun(ctx, runs, &run, "release_lock", err)
			return
		}
		s.succeedArchitectureRun(ctx, runs, &run)
		return
	}
	if req.Architecture == hadomain.ArchitectureStandalone {
		if err := s.executeStandaloneArchitecture(ctx, runs, &run, req, machines); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "resume_business_connections", func() ([]string, error) {
			return s.resumeArchitectureBusinessConnections(ctx, req, machines)
		}); err != nil {
			return
		}
		select {
		case lockErr := <-lockErrors:
			s.failArchitectureRun(context.Background(), runs, &run, "renew_lock", lockErr)
			return
		default:
		}
		if err := releaseLock(); err != nil {
			s.failArchitectureRun(ctx, runs, &run, "release_lock", err)
			return
		}
		s.succeedArchitectureRun(ctx, runs, &run)
		return
	}
	startingFromIndependent := req.CurrentMasterMachineID == "" && !req.InitializeVIP
	if startingFromIndependent {
		if err := s.runArchitectureStep(ctx, runs, &run, "freeze_old_master", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@offline_mode=1 AND @@read_only=1 AND @@super_read_only=1,'FROZEN','NOT_FROZEN');") + " | grep -Fxq FROZEN"
			})
		}); err != nil {
			s.restoreIndependentWriters(context.Background(), req, machines)
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "kill_business_sessions", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return killBusinessSessionsCommand(req, node.MachineID, node.Port)
			})
		}); err != nil {
			s.restoreIndependentWriters(context.Background(), req, machines)
			return
		}
	}
	var elected hadomain.CandidateScore
	if err := s.runArchitectureStep(ctx, runs, &run, "elect_candidate", func() ([]string, error) {
		candidate, taskIDs, electionErr := s.electLiveArchitectureCandidate(ctx, run.ClusterID, req, machines)
		elected = candidate
		return taskIDs, electionErr
	}); err != nil {
		if startingFromIndependent {
			s.restoreIndependentWriters(context.Background(), req, machines)
		}
		return
	}
	run.Plan.SelectedCandidate = elected
	run.UpdatedAt = time.Now().UTC()
	_ = runs.SaveArchitectureRun(ctx, run)
	if startingFromIndependent {
		if err := s.runArchitectureStep(ctx, runs, &run, "align_replica_gtid", func() ([]string, error) {
			return s.alignIndependentReplicaGTID(ctx, req, elected, machines)
		}); err != nil {
			s.restoreIndependentWriters(context.Background(), req, machines)
			return
		}
	}
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
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, oldNode.MachineID), oldNode.Port, "SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT @@offline_mode,@@read_only,@@super_read_only;"))
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "kill_business_sessions", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], killBusinessSessionsCommand(req, oldNode.MachineID, oldNode.Port))
		}); err != nil {
			return
		}

		candidateNode, _ := architectureNode(req.Nodes, run.Plan.SelectedCandidate.MachineID)
		err = s.runArchitectureStep(ctx, runs, &run, "wait_replication_zero", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[candidateNode.MachineID], replicationCatchupCommand(architectureRootPassword(req, candidateNode.MachineID), candidateNode.Port))
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
			return s.runOneArchitectureCommand(ctx, machines[oldNode.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, oldNode.MachineID), oldNode.Port, "SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@offline_mode=1 AND @@read_only=1 AND @@super_read_only=1,'FENCED','NOT_FENCED');"))
		}); err != nil {
			return
		}
	}

	if err := s.runArchitectureStep(ctx, runs, &run, "promote_new_master", func() ([]string, error) {
		node, _ := architectureNode(req.Nodes, run.Plan.SelectedCandidate.MachineID)
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		command := replicationStopResetShell(client) + client + " --batch --raw --execute=" + shellQuote("SET GLOBAL offline_mode=ON; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; SELECT @@offline_mode,@@read_only,@@super_read_only;")
		return s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
	}); err != nil {
		return
	}

	topologyReq := architectureParticipationRequest(req)
	repointErr := s.runArchitectureStep(ctx, runs, &run, "repoint_replicas", func() ([]string, error) {
		return s.configureArchitectureTopology(ctx, topologyReq, run.Plan.SelectedCandidate.MachineID, machines)
	})
	if repointErr != nil && !run.ForceConfirmed {
		return
	}
	verifyErr := repointErr
	if verifyErr == nil {
		verifyErr = s.runArchitectureStep(ctx, runs, &run, "verify_topology", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, topologyReq.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return verifyArchitectureNodeCommand(topologyReq, node, run.Plan.SelectedCandidate.MachineID)
			})
		})
	}
	if verifyErr != nil {
		if !run.ForceConfirmed {
			return
		}
		resumeArchitectureRun(ctx, runs, &run)
		if err := s.runArchitectureStep(ctx, runs, &run, "pt_repair_on_failure", func() ([]string, error) {
			return s.repairArchitectureReplication(ctx, topologyReq, run.Plan.SelectedCandidate.MachineID, machines)
		}); err != nil {
			return
		}
		if err := s.runArchitectureStep(ctx, runs, &run, "verify_topology", func() ([]string, error) {
			return s.runOnArchitectureNodes(ctx, topologyReq.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
				return verifyArchitectureNodeCommand(topologyReq, node, run.Plan.SelectedCandidate.MachineID)
			})
		}); err != nil {
			return
		}
	}
	if err := s.runArchitectureStep(ctx, runs, &run, "pt_verify_replication", func() ([]string, error) {
		return s.verifyArchitectureDataWithPT(ctx, topologyReq, run.Plan.SelectedCandidate.MachineID, machines)
	}); err != nil {
		return
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
	if err := s.runArchitectureStep(ctx, runs, &run, "resume_business_connections", func() ([]string, error) {
		return s.resumeArchitectureBusinessConnections(ctx, topologyReq, machines)
	}); err != nil {
		return
	}

	select {
	case lockErr := <-lockErrors:
		s.failArchitectureRun(context.Background(), runs, &run, "renew_lock", lockErr)
		return
	default:
	}
	if err := releaseLock(); err != nil {
		s.failArchitectureRun(ctx, runs, &run, "release_lock", err)
		return
	}
	s.succeedArchitectureRun(ctx, runs, &run)
}

func architectureParticipationRequest(req hadomain.ArchitectureAdjustmentRequest) hadomain.ArchitectureAdjustmentRequest {
	if len(req.MaintenanceDetachedMachineIDs) == 0 {
		return req
	}
	detached := make(map[string]struct{}, len(req.MaintenanceDetachedMachineIDs))
	for _, machineID := range req.MaintenanceDetachedMachineIDs {
		if machineID = strings.TrimSpace(machineID); machineID != "" {
			detached[machineID] = struct{}{}
		}
	}
	filtered := req
	filtered.Nodes = make([]hadomain.ArchitectureNodeRequest, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		if _, skip := detached[node.MachineID]; !skip {
			filtered.Nodes = append(filtered.Nodes, node)
		}
	}
	return filtered
}

func (s *HAService) executeStandaloneArchitecture(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) error {
	if err := s.runArchitectureStep(ctx, runs, run, "validate_independent_targets", func() ([]string, error) {
		return s.validateLiveIndependentTargets(ctx, req, machines)
	}); err != nil {
		return err
	}
	if req.CurrentMasterMachineID != "" {
		current, ok := architectureNode(req.Nodes, req.CurrentMasterMachineID)
		if !ok {
			err := errors.New("current master node is not in standalone target")
			s.failArchitectureRun(ctx, runs, run, "freeze_old_master", err)
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "freeze_old_master", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[current.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, current.MachineID), current.Port, "SET GLOBAL offline_mode=ON; SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON; SELECT IF(@@offline_mode=1 AND @@read_only=1 AND @@super_read_only=1,'FROZEN','NOT_FROZEN');")+" | grep -Fxq FROZEN")
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "kill_business_sessions", func() ([]string, error) {
			return s.runOneArchitectureCommand(ctx, machines[current.MachineID], killBusinessSessionsCommand(req, current.MachineID, current.Port))
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "wait_replication_zero", func() ([]string, error) {
			var ids []string
			for _, node := range req.Nodes {
				if node.MachineID == current.MachineID {
					continue
				}
				created, waitErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], replicationCatchupCommand(architectureRootPassword(req, node.MachineID), node.Port))
				ids = append(ids, created...)
				if waitErr != nil {
					return ids, fmt.Errorf("replica %s did not reach the split point: %w", node.MachineID, waitErr)
				}
			}
			return ids, nil
		}); err != nil {
			return err
		}
		if err := s.runArchitectureStep(ctx, runs, run, "pt_verify_before_split", func() ([]string, error) {
			return s.verifyArchitectureDataWithPT(ctx, req, current.MachineID, machines)
		}); err != nil {
			return err
		}
	}
	if err := s.runArchitectureStep(ctx, runs, run, "detach_replication", func() ([]string, error) {
		return s.configureStandaloneArchitecture(ctx, req, machines)
	}); err != nil {
		return err
	}
	return s.runArchitectureStep(ctx, runs, run, "verify_topology", func() ([]string, error) {
		return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
			return verifyIndependentNodeCommand(architectureRootPassword(req, node.MachineID), node.Port)
		})
	})
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
		id, output, err := s.runOneArchitectureProbe(ctx, machines[node.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, probeSQL))
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
		startingFromIndependent := req.CurrentMasterMachineID == "" && !req.InitializeVIP
		if !startingFromIndependent && !gtidSetSubset(nodeGTIDSets[node.MachineID], selected.ExecutedGTIDSet) {
			return hadomain.CandidateScore{}, taskIDs, fmt.Errorf("target replica %s has divergent GTID history; clone or rebuild it before assigning replication", node.MachineID)
		}
	}
	selected.DataFreshnessScore = 100
	selected.FinalScore = selected.HealthScore + selected.ElectionPriority
	return selected, taskIDs, nil
}

func (s *HAService) alignIndependentReplicaGTID(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, selected hadomain.CandidateScore, machines map[string]machinedomain.Machine) ([]string, error) {
	var taskIDs []string
	for _, node := range req.Nodes {
		if node.MachineID == selected.MachineID || !strings.EqualFold(node.Role, "S") {
			continue
		}
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		gtidProbe := mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SELECT @@GLOBAL.gtid_executed;")
		id, output, err := s.runOneArchitectureProbe(ctx, machines[node.MachineID], gtidProbe)
		if id != "" {
			taskIDs = append(taskIDs, id)
		}
		if err != nil {
			return taskIDs, fmt.Errorf("read independent GTID state on %s: %w", node.MachineID, err)
		}
		if gtidSetSubset(strings.TrimSpace(output), selected.ExecutedGTIDSet) {
			continue
		}
		businessObjectsSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema','gmha','percona')"
		id, output, err = s.runOneArchitectureProbe(ctx, machines[node.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, businessObjectsSQL))
		if id != "" {
			taskIDs = append(taskIDs, id)
		}
		if err != nil {
			return taskIDs, fmt.Errorf("inspect business schemas on independent target %s: %w", node.MachineID, err)
		}
		businessObjects, parseErr := strconv.Atoi(strings.TrimSpace(output))
		if parseErr != nil {
			return taskIDs, fmt.Errorf("independent target %s returned invalid business-object count %q", node.MachineID, strings.TrimSpace(output))
		}
		if businessObjects != 0 {
			return taskIDs, fmt.Errorf("target replica %s has divergent GTID history and %d business table/view(s); clone or explicitly reconcile it before assigning replication", node.MachineID, businessObjects)
		}
		reset := replicationStopResetShell(client) +
			"if ! " + client + " --execute='RESET BINARY LOGS AND GTIDS' >/dev/null 2>&1; then " + client + " --execute='RESET MASTER'; fi; "
		if strings.TrimSpace(selected.ExecutedGTIDSet) != "" {
			reset += client + " --batch --skip-column-names --execute=" + shellQuote("SET GLOBAL GTID_PURGED="+sqlLiteral(selected.ExecutedGTIDSet)+"; SELECT IF(GTID_SUBSET("+sqlLiteral(selected.ExecutedGTIDSet)+",@@GLOBAL.gtid_executed),'GTID_BASELINE_OK','GTID_BASELINE_BAD');") + " | grep -Fxq GTID_BASELINE_OK"
		} else {
			reset += "echo GTID_BASELINE_OK"
		}
		created, resetErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], reset)
		taskIDs = append(taskIDs, created...)
		if resetErr != nil {
			return taskIDs, fmt.Errorf("align GTID baseline on empty target replica %s: %w", node.MachineID, resetErr)
		}
	}
	return taskIDs, nil
}

func (s *HAService) validateLiveIndependentTargets(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	serverIDs := make(map[int]string, len(req.Nodes))
	var taskIDs []string
	for _, node := range req.Nodes {
		probeSQL := "SELECT CONCAT_WS('|',@@server_id,@@global.gtid_mode,@@read_only,@@super_read_only);"
		id, output, err := s.runOneArchitectureProbe(ctx, machines[node.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, probeSQL))
		if id != "" {
			taskIDs = append(taskIDs, id)
		}
		if err != nil {
			return taskIDs, fmt.Errorf("independent target probe failed on %s: %w", node.MachineID, err)
		}
		fields := strings.Split(strings.TrimSpace(output), "|")
		if len(fields) < 4 {
			return taskIDs, fmt.Errorf("independent target %s returned malformed MySQL state", node.MachineID)
		}
		serverID, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil || serverID <= 0 {
			return taskIDs, fmt.Errorf("independent target %s has invalid server_id", node.MachineID)
		}
		if other, duplicate := serverIDs[serverID]; duplicate {
			return taskIDs, fmt.Errorf("duplicate server_id %d on %s and %s", serverID, other, node.MachineID)
		}
		serverIDs[serverID] = node.MachineID
		if !strings.EqualFold(fields[1], "ON") {
			return taskIDs, fmt.Errorf("independent target %s must have GTID mode enabled", node.MachineID)
		}
	}
	return taskIDs, nil
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
	password := architectureRootPassword(req, node.MachineID)
	isDualMaster := req.Architecture != hadomain.ArchitectureMasterSlave && strings.EqualFold(node.Role, "M")
	if node.MachineID == selectedPrimaryID && !isDualMaster {
		return mysqlArchitectureCommand(password, node.Port, "SELECT IF(@@read_only=0 AND @@super_read_only=0,'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
	}
	expectedReadOnly := "1"
	if isDualMaster {
		expectedReadOnly = "0"
	}
	if node.DelaySeconds > 0 && !isDualMaster {
		return delayedReplicationHealthCommand(password, node.Port, node.DelaySeconds) + " && " + mysqlArchitectureCommand(password, node.Port, "SELECT IF(@@read_only=1 AND @@super_read_only=1,'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
	}
	return replicationCatchupCommand(password, node.Port) + " && " + mysqlArchitectureCommand(password, node.Port, "SELECT IF(@@read_only="+expectedReadOnly+" AND @@super_read_only="+expectedReadOnly+",'ROLE_OK','ROLE_BAD');") + " | grep -Fxq ROLE_OK"
}

func architectureRootPassword(req hadomain.ArchitectureAdjustmentRequest, machineID string) string {
	if password := req.RootPasswords[machineID]; password != "" {
		return password
	}
	return req.RootPassword
}

func resumeArchitectureRun(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun) {
	run.Status, run.Error, run.FinishedAt = hadomain.ArchitectureRunRunning, "", nil
	run.UpdatedAt = time.Now().UTC()
	_ = runs.SaveArchitectureRun(ctx, *run)
}

// verifyArchitectureDataWithPT is a mandatory data-consistency gate whenever
// replication is created or removed. A successful MySQL thread/GTID check is
// not sufficient: pt-table-checksum must also prove that every participating
// replica contains the same table data as the selected source.
func (s *HAService) verifyArchitectureDataWithPT(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, primaryID string, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, primaryID)
	if !ok {
		return nil, errors.New("PT verification source node not found")
	}
	var ids []string
	for _, node := range req.Nodes {
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], installCompatiblePTCommand(architectureRootPassword(req, node.MachineID), node.Port))
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("Percona Toolkit validation failed on %s: %w", node.MachineID, err)
		}
	}
	checksumCommand := ptChecksumCommand(architectureRootPassword(req, primary.MachineID), primary.Port)
	created, err := s.runOneArchitectureCommand(ctx, machines[primaryID], checksumCommand)
	ids = append(ids, created...)
	if err != nil {
		return ids, fmt.Errorf("pt-table-checksum failed on source %s: %w", primaryID, err)
	}
	for _, node := range req.Nodes {
		if node.MachineID == primaryID {
			continue
		}
		password := architectureRootPassword(req, node.MachineID)
		check := replicationCatchupCommand(password, node.Port) + " && " + ptChecksumVerificationCommand(password, node.Port)
		created, err = s.runOneArchitectureCommand(ctx, machines[node.MachineID], check)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("PT checksum reports data differences on %s: %w", node.MachineID, err)
		}
	}
	return ids, nil
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
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], installCompatiblePTCommand(architectureRootPassword(req, node.MachineID), node.Port))
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
		options := ptArchitectureOptions(architectureRootPassword(req, node.MachineID), node.Port)
		created, runErr := s.runOneArchitectureCommand(ctx, machines[node.MachineID], "pt-replica-restart "+options+" --max-sleep=5 --run-time=60s")
		ids = append(ids, created...)
		if runErr != nil {
			return ids, fmt.Errorf("PT replica restart failed on %s: %w", machines[node.MachineID].Name, runErr)
		}
	}
	checksumCommand := ptChecksumCommand(architectureRootPassword(req, primary.MachineID), primary.Port)
	created, err := s.runOneArchitectureCommand(ctx, machines[primaryID], checksumCommand)
	ids = append(ids, created...)
	if err != nil {
		return ids, err
	}
	for _, node := range req.Nodes {
		if node.MachineID == primaryID && req.Architecture == hadomain.ArchitectureMasterSlave {
			continue
		}
		options := ptArchitectureOptions(architectureRootPassword(req, node.MachineID), node.Port)
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
		check := mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SELECT IF(COUNT(*)=0,'PT_SYNC_OK','PT_SYNC_DIFF') FROM percona.checksums WHERE master_crc<>this_crc OR master_cnt<>this_cnt;") + " | grep -Fxq PT_SYNC_OK"
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
	return "mysql_version=$(" + client + " --batch --skip-column-names --execute='SELECT VERSION()' | cut -d- -f1); case \"$mysql_version\" in 5.7.*) min_pt=3.5.0 ;; 8.*|9.*) min_pt=3.7.1 ;; *) echo \"unsupported MySQL version for automatic PT repair: $mysql_version\" >&2; exit 76 ;; esac; " +
		"command -v perl >/dev/null 2>&1 && perl -MDBI -MDBD::mysql -e 1 >/dev/null 2>&1 || { echo 'Percona Toolkit offline dependencies are missing; install PT from the Manager offline bundle first' >&2; exit 77; }; " +
		"command -v pt-table-sync >/dev/null 2>&1 || { echo 'Percona Toolkit is not installed; enable offline PT installation for this MySQL instance' >&2; exit 77; }; " +
		"pt_version=$(pt-table-sync --version | awk '{print $NF}'); [ \"$(printf '%s\\n' \"$min_pt\" \"$pt_version\" | sort -V | head -n1)\" = \"$min_pt\" ] || { echo \"Percona Toolkit $pt_version is below required $min_pt\" >&2; exit 78; }; command -v pt-replica-restart >/dev/null; command -v pt-table-checksum >/dev/null"
}

func ptArchitectureOptions(password string, port int) string {
	if password == "" {
		return fmt.Sprintf("--defaults-file=__GMHA_MYSQL_DEFAULTS_FILE__ --host=127.0.0.1 --port=%d", port)
	}
	return fmt.Sprintf("--host=127.0.0.1 --port=%d --user=root --password=%s", port, shellQuote(password))
}

func ptChecksumCommand(password string, port int) string {
	client := mysqlArchitectureClient(password, port) + " --batch --skip-column-names"
	countSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema','gmha','percona') AND table_type='BASE TABLE'"
	return "business_tables=$(" + client + " --execute=" + shellQuote(countSQL) + ") || exit 70; " +
		"if [ \"$business_tables\" = 0 ]; then echo GMHA_PT_NO_BUSINESS_TABLES; exit 0; fi; " +
		"pt-table-checksum " + ptArchitectureOptions(password, port) + " --replicate=percona.checksums --create-replicate-table --no-check-replication-filters --no-check-binlog-format --ignore-databases=mysql,sys,performance_schema,information_schema,gmha,percona"
}

func ptChecksumVerificationCommand(password string, port int) string {
	client := mysqlArchitectureClient(password, port) + " --batch --skip-column-names"
	countSQL := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema','gmha','percona') AND table_type='BASE TABLE'"
	checkSQL := "SELECT IF(COALESCE(SUM(master_crc<>this_crc OR master_cnt<>this_cnt),0)=0,'PT_VERIFY_OK','PT_VERIFY_DIFF') FROM percona.checksums WHERE db NOT IN ('mysql','sys','performance_schema','information_schema','gmha','percona');"
	return "business_tables=$(" + client + " --execute=" + shellQuote(countSQL) + ") || exit 70; " +
		"if [ \"$business_tables\" = 0 ]; then echo PT_VERIFY_OK; exit 0; fi; " +
		mysqlArchitectureCommand(password, port, checkSQL) + " | grep -Fxq PT_VERIFY_OK"
}

func (s *HAService) checkArchitectureVIPConflict(ctx context.Context, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, vip := range configs {
		probeIDs, holders, probeErr := s.probeArchitectureVIPHolders(ctx, vip, machines)
		ids = append(ids, probeIDs...)
		if probeErr != nil {
			return ids, probeErr
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

func (s *HAService) allClusterVIPMachines(ctx context.Context, clusterID string, selected map[string]machinedomain.Machine) (map[string]machinedomain.Machine, error) {
	items, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]machinedomain.Machine, len(items)+len(selected))
	for id, machine := range selected {
		result[id] = machine
	}
	for _, machine := range items {
		if machine.Cluster == clusterID {
			result[machine.ID] = machine
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("cluster %s has no machines available for VIP split-brain checks", clusterID)
	}
	return result, nil
}

func (s *HAService) probeArchitectureVIPHolders(ctx context.Context, vip hadomain.ClusterVIPConfig, machines map[string]machinedomain.Machine) ([]string, []string, error) {
	var taskIDs, holders []string
	for _, machine := range sortedArchitectureMachines(machines) {
		command := "if ip -o -4 addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq " + shellQuote(vip.VIPAddress) + "; then echo BOUND; else echo UNBOUND; fi"
		id, output, err := s.runOneArchitectureProbe(ctx, machine, command)
		if id != "" {
			taskIDs = append(taskIDs, id)
		}
		if err != nil {
			return taskIDs, holders, fmt.Errorf("cannot prove VIP %s state on %s: %w", vip.VIPAddress, machine.Name, err)
		}
		if strings.TrimSpace(output) == "BOUND" {
			holders = append(holders, machine.ID)
		}
	}
	return taskIDs, holders, nil
}

func (s *HAService) withdrawArchitectureVIP(ctx context.Context, run hadomain.ArchitectureRun, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, vip := range configs {
		mode := vip.VIPRouteMode
		if mode == "" {
			mode = run.Plan.VIPRouteMode
		}
		for _, machine := range sortedArchitectureMachines(machines) {
			command := l2VIPRemoveCommand(vip)
			if mode == hadomain.VipRouteModeBGP {
				command = bgpVIPWithdrawCommand(vip)
			} else if mode != hadomain.VipRouteModeL2ARP {
				return ids, fmt.Errorf("unsupported VIP route mode %s", mode)
			}
			created, runErr := s.runOneArchitectureCommand(ctx, machine, command)
			ids = append(ids, created...)
			if runErr != nil {
				return ids, fmt.Errorf("withdraw VIP %s on %s: %w", vip.VIPAddress, machine.Name, runErr)
			}
		}
	}
	return ids, nil
}

func (s *HAService) verifyArchitectureVIPZero(ctx context.Context, run hadomain.ArchitectureRun, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, vip := range configs {
		probeIDs, holders, probeErr := s.probeArchitectureVIPHolders(ctx, vip, machines)
		ids = append(ids, probeIDs...)
		if probeErr != nil {
			return ids, probeErr
		}
		if len(holders) != 0 {
			return ids, fmt.Errorf("VIP %s zero-holder barrier failed; holders=%s", vip.VIPAddress, strings.Join(holders, ","))
		}
	}
	return ids, nil
}

func (s *HAService) bindArchitectureVIP(ctx context.Context, run hadomain.ArchitectureRun, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
	if err != nil {
		return nil, err
	}
	target, ok := machines[run.Plan.SelectedCandidate.MachineID]
	if !ok {
		return nil, errors.New("selected VIP target machine is unavailable")
	}
	var ids []string
	for _, vip := range configs {
		mode := vip.VIPRouteMode
		if mode == "" {
			mode = run.Plan.VIPRouteMode
		}
		command := l2VIPBindCommand(vip)
		if mode == hadomain.VipRouteModeBGP {
			command = bgpVIPAnnounceCommand(vip, target)
		} else if mode != hadomain.VipRouteModeL2ARP {
			return ids, fmt.Errorf("unsupported VIP route mode %s", mode)
		}
		created, bindErr := s.runOneArchitectureCommand(ctx, target, command)
		ids = append(ids, created...)
		if bindErr != nil {
			return ids, fmt.Errorf("bind VIP %s on %s: %w", vip.VIPAddress, target.Name, bindErr)
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
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
	if err != nil {
		return nil, err
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
		case hadomain.VipRouteModeBGP:
			for _, machine := range sortedArchitectureMachines(machines) {
				created, runErr := s.runOneArchitectureCommand(ctx, machine, bgpVIPWithdrawCommand(vip))
				ids = append(ids, created...)
				if runErr != nil {
					return ids, fmt.Errorf("BGP withdrawal failed on %s: %w", machine.Name, runErr)
				}
			}
		default:
			return ids, fmt.Errorf("unsupported VIP route mode %s", mode)
		}
		barrierIDs, holders, barrierErr := s.probeArchitectureVIPHolders(ctx, vip, machines)
		ids = append(ids, barrierIDs...)
		if barrierErr != nil {
			return ids, barrierErr
		}
		if len(holders) != 0 {
			return ids, fmt.Errorf("VIP %s zero-holder barrier failed after withdrawal; holders=%s", vip.VIPAddress, strings.Join(holders, ","))
		}
		bindCommand := l2VIPBindCommand(vip)
		if mode == hadomain.VipRouteModeBGP {
			bindCommand = bgpVIPAnnounceCommand(vip, target)
		}
		created, bindErr := s.runOneArchitectureCommand(ctx, target, bindCommand)
		ids = append(ids, created...)
		if bindErr != nil {
			return ids, bindErr
		}
		var verifiedHolders []string
		var verifyErr error
		for round := 1; round <= 2; round++ {
			var verifyIDs []string
			verifyIDs, verifiedHolders, verifyErr = s.probeArchitectureVIPHolders(ctx, vip, machines)
			ids = append(ids, verifyIDs...)
			if verifyErr != nil || len(verifiedHolders) != 1 || verifiedHolders[0] != target.ID {
				break
			}
		}
		if verifyErr != nil || len(verifiedHolders) != 1 || verifiedHolders[0] != target.ID {
			// Compensate on the new target. Leaving no VIP is safer than leaving two
			// possible holders when the cluster-wide proof is inconclusive.
			rollback := l2VIPRemoveCommand(vip)
			if mode == hadomain.VipRouteModeBGP {
				rollback = bgpVIPWithdrawCommand(vip)
			}
			rolledBack, _ := s.runOneArchitectureCommand(context.WithoutCancel(ctx), target, rollback)
			ids = append(ids, rolledBack...)
			_ = s.repo.UpsertVIPBindingState(context.WithoutCancel(ctx), hadomain.VIPBindingState{ClusterID: run.ClusterID, VIPConfigID: vip.ID, VIPAddress: vip.VIPAddress, ExpectedHolderMachineID: target.ID, VIPStatus: hadomain.VipStatusFailed, DetectedHolders: strings.Join(verifiedHolders, ","), LastError: "cluster-wide single-holder proof failed; new target was withdrawn"})
			if verifyErr != nil {
				return ids, verifyErr
			}
			return ids, fmt.Errorf("VIP %s single-holder proof failed; holders=%s; target binding rolled back", vip.VIPAddress, strings.Join(verifiedHolders, ","))
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

func (s *HAService) verifyArchitectureVIP(ctx context.Context, run hadomain.ArchitectureRun, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	configs, err := s.repo.ListVIPConfigs(ctx, run.ClusterID)
	if err != nil {
		return nil, err
	}
	machines, err = s.allClusterVIPMachines(ctx, run.ClusterID, machines)
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
		target := machines[run.Plan.SelectedCandidate.MachineID]
		_ = s.repo.UpsertVIPBindingState(ctx, hadomain.VIPBindingState{ClusterID: run.ClusterID, VIPConfigID: vip.ID, VIPAddress: vip.VIPAddress, ExpectedHolderInstanceID: run.Plan.SelectedCandidate.InstanceID, ExpectedHolderMachineID: target.ID, CurrentHolderInstanceID: run.Plan.SelectedCandidate.InstanceID, CurrentHolderMachineID: target.ID, CurrentInterface: vip.DefaultInterface, VIPStatus: hadomain.VipStatusBound, DetectedHolders: target.ID, LastCheckResult: "single holder verified after VIP workflow"})
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
	// Discover the live holder interface instead of trusting the saved interface.
	// This also removes stale bindings left on a renamed or previously selected NIC.
	return fmt.Sprintf("ip -o -4 addr show | awk -v vip=%s '$4 ~ (\"^\" vip \"/\") {print $2, $4}' | while read -r iface cidr; do ip addr del \"$cidr\" dev \"$iface\" || exit 1; done; ! ip -o -4 addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq %s", shellQuote(vip.VIPAddress), shellQuote(vip.VIPAddress))
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
	if len(taskIDs) > 0 {
		if attachErr := s.tasks.AttachChildTasks(ctx, run.RunID, taskIDs); attachErr != nil && err == nil {
			err = attachErr
		}
	}
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
		message := strings.TrimSpace(output)
		if message == "" {
			return detail.Task.ID, output, fmt.Errorf("agent task %s failed", detail.Task.ID)
		}
		return detail.Task.ID, output, fmt.Errorf("agent task %s failed: %s", detail.Task.ID, message)
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

func (s *HAService) repairArchitectureManagementPrivileges(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine, username, password string) ([]string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("MHA management account is not configured")
	}
	account := sqlLiteral(username) + "@'%'"
	modernPrivileges := strings.Join([]string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "CREATE USER", "ALTER", "DROP", "SHOW VIEW", "TRIGGER", "EVENT",
		"PROCESS", "RELOAD", "LOCK TABLES", "REPLICATION CLIENT", "REPLICATION SLAVE", "CONNECTION_ADMIN",
		"SYSTEM_VARIABLES_ADMIN", "REPLICATION_SLAVE_ADMIN", "BACKUP_ADMIN", "CLONE_ADMIN",
	}, ", ")
	legacyPrivileges := strings.Join([]string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "CREATE USER", "ALTER", "DROP", "SHOW VIEW", "TRIGGER", "EVENT",
		"PROCESS", "RELOAD", "LOCK TABLES", "REPLICATION CLIENT", "REPLICATION SLAVE", "SUPER",
	}, ", ")
	instances, err := s.instances.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list MySQL instances for MHA privilege repair: %w", err)
	}
	byMachine := make(map[string][]mysqlapp.Instance)
	for _, instance := range instances {
		byMachine[instance.MachineID] = append(byMachine[instance.MachineID], instance)
	}
	return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
		instance, found := architectureInstanceForNode(node, byMachine[node.MachineID])
		privileges := modernPrivileges
		instanceVersion := instance.Version
		if found && strings.TrimSpace(instanceVersion) == "" {
			instanceVersion, _ = mysqlapp.PackageVersion(instance.PackageName)
		}
		if found && !mysqlapp.SupportsDynamicPrivilegeForVersion(instanceVersion, "CONNECTION_ADMIN") {
			privileges = legacyPrivileges
		}
		sql := fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s; GRANT %s ON *.* TO %s;", account, sqlLiteral(password), privileges, account)
		if found && strings.TrimSpace(instance.SocketPath) != "" {
			return mysqlArchitectureRootSocketClient(req.RootPassword, instance.SocketPath) + " --batch --raw --execute=" + shellQuote(sql)
		}
		return mysqlArchitectureCommand(req.RootPassword, node.Port, sql)
	})
}

func (s *HAService) configureStandaloneArchitecture(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	var ids []string
	for _, node := range req.Nodes {
		command := standaloneDetachCommand(architectureRootPassword(req, node.MachineID), node.Port)
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, fmt.Errorf("detach replication on %s: %w", node.MachineID, err)
		}
	}
	return ids, nil
}

func (s *HAService) restoreIndependentWriters(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) {
	_, _ = s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
		return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF; SET GLOBAL offline_mode=OFF;")
	})
}

func (s *HAService) resumeArchitectureBusinessConnections(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, machines map[string]machinedomain.Machine) ([]string, error) {
	return s.runOnArchitectureNodes(ctx, req.Nodes, machines, func(node hadomain.ArchitectureNodeRequest, _ machinedomain.Machine) string {
		return mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, "SET GLOBAL offline_mode=OFF; SELECT IF(@@offline_mode=0,'ONLINE','OFFLINE');") + " | grep -Fxq ONLINE"
	})
}

func standaloneDetachCommand(password string, port int) string {
	client := mysqlArchitectureClient(password, port)
	return client + " --batch --raw --execute=" + shellQuote("SET GLOBAL offline_mode=ON;") + "; " +
		replicationStopResetShell(client) +
		"(" + client + " --batch --raw --execute=" + shellQuote("SET PERSIST auto_increment_increment=1; SET PERSIST auto_increment_offset=1;") + " >/dev/null 2>&1 || " + client + " --batch --raw --execute=" + shellQuote("SET GLOBAL auto_increment_increment=1; SET GLOBAL auto_increment_offset=1;") + "); " +
		mysqlRolePersistenceCommand(client, false)
}

func verifyIndependentNodeCommand(password string, port int) string {
	sql := "SELECT IF(@@read_only=0 AND @@super_read_only=0 AND @@auto_increment_increment=1 AND @@auto_increment_offset=1 AND NOT EXISTS(SELECT 1 FROM performance_schema.replication_connection_configuration),'INDEPENDENT_OK','INDEPENDENT_BAD');"
	return mysqlArchitectureCommand(password, port, sql) + " | grep -Fxq INDEPENDENT_OK"
}

func (s *HAService) configureArchitectureTopology(ctx context.Context, req hadomain.ArchitectureAdjustmentRequest, primaryID string, machines map[string]machinedomain.Machine) ([]string, error) {
	primary, ok := architectureNode(req.Nodes, primaryID)
	if !ok {
		return nil, errors.New("selected primary node not found")
	}
	var ids []string
	masters := make([]hadomain.ArchitectureNodeRequest, 0, 2)
	managementUser, _ := s.architectureManagementAccount(ctx)
	reuseManagementAccount := strings.EqualFold(strings.TrimSpace(req.ReplicationUser), strings.TrimSpace(managementUser))
	for _, node := range req.Nodes {
		if strings.EqualFold(node.Role, "M") {
			masters = append(masters, node)
			if reuseManagementAccount {
				continue
			}
			accountSQL := fmt.Sprintf("CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED BY %s; ALTER USER %s@'%%' IDENTIFIED BY %s; GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO %s@'%%';", sqlIdentifier(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), sqlIdentifier(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), sqlIdentifier(req.ReplicationUser))
			created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], mysqlArchitectureCommand(architectureRootPassword(req, node.MachineID), node.Port, accountSQL))
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
		client := mysqlArchitectureClient(architectureRootPassword(req, node.MachineID), node.Port)
		reset := replicationStopResetShell(client)
		modernSQL := fmt.Sprintf("SET GLOBAL offline_mode=ON; CHANGE REPLICATION SOURCE TO SOURCE_HOST=%s,SOURCE_PORT=%d,SOURCE_USER=%s,SOURCE_PASSWORD=%s,SOURCE_AUTO_POSITION=1,SOURCE_DELAY=%d,GET_SOURCE_PUBLIC_KEY=1; START REPLICA;", sqlLiteral(sourceMachine.IP), source.Port, sqlLiteral(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), node.DelaySeconds)
		legacySQL := fmt.Sprintf("SET GLOBAL offline_mode=ON; CHANGE MASTER TO MASTER_HOST=%s,MASTER_PORT=%d,MASTER_USER=%s,MASTER_PASSWORD=%s,MASTER_AUTO_POSITION=1,MASTER_DELAY=%d; START SLAVE;", sqlLiteral(sourceMachine.IP), source.Port, sqlLiteral(req.ReplicationUser), sqlLiteral(req.ReplicationPassword), node.DelaySeconds)
		command := reset + "(" + client + " --batch --raw --execute=" + shellQuote(modernSQL) + " >/dev/null 2>&1 || " + client + " --batch --raw --execute=" + shellQuote(legacySQL) + "); "
		if isMaster {
			offset := 1
			for index, master := range masters {
				if master.MachineID == node.MachineID {
					offset = index + 1
					break
				}
			}
			persistSQL := fmt.Sprintf("SET PERSIST auto_increment_increment=%d; SET PERSIST auto_increment_offset=%d;", len(masters), offset)
			globalSQL := fmt.Sprintf("SET GLOBAL auto_increment_increment=%d; SET GLOBAL auto_increment_offset=%d;", len(masters), offset)
			command += "(" + client + " --batch --raw --execute=" + shellQuote(persistSQL) + " >/dev/null 2>&1 || " + client + " --batch --raw --execute=" + shellQuote(globalSQL) + "); "
			command += mysqlRolePersistenceCommand(client, false)
		} else {
			command += mysqlRolePersistenceCommand(client, true)
		}
		created, err := s.runOneArchitectureCommand(ctx, machines[node.MachineID], command)
		ids = append(ids, created...)
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func mysqlArchitectureCommand(password string, port int, sql string) string {
	return mysqlArchitectureClient(password, port) + " --batch --raw --skip-column-names --execute=" + shellQuote(sql)
}

func replicationStopResetShell(client string) string {
	return "(" + client + " --execute='STOP REPLICA' >/dev/null 2>&1 || " + client + " --execute='STOP SLAVE' >/dev/null 2>&1 || true); " +
		"(" + client + " --execute='RESET REPLICA ALL' >/dev/null 2>&1 || " + client + " --execute='RESET SLAVE ALL' >/dev/null 2>&1 || true); "
}

func mysqlRolePersistenceCommand(client string, readOnly bool) string {
	persistSQL := "SET PERSIST super_read_only=OFF; SET PERSIST read_only=OFF;"
	globalSQL := "SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;"
	if readOnly {
		persistSQL = "SET PERSIST read_only=ON; SET PERSIST super_read_only=ON;"
		globalSQL = "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;"
	}
	return "(" + client + " --batch --raw --execute=" + shellQuote(persistSQL) + " >/dev/null 2>&1 || " +
		client + " --batch --raw --execute=" + shellQuote(globalSQL) + ")"
}

func architecturePreflightCommand(password string, port int) string {
	client := mysqlArchitectureClient(password, port)
	probe := client + " --batch --raw --execute=" + shellQuote("SELECT @@hostname,@@port,@@server_id,@@read_only,@@super_read_only,@@offline_mode,@@global.gtid_mode; SELECT COUNT(*) FROM performance_schema.threads;")
	grants := client + " --batch --skip-column-names --execute=" + shellQuote("SHOW GRANTS FOR CURRENT_USER")
	return probe + " || exit 70; grant_output=$(" + grants + ") || exit 70; " +
		"printf '%s\\n' \"$grant_output\" | grep -Eiq 'GRANT (ALL PRIVILEGES|.*SUPER)|SYSTEM_VARIABLES_ADMIN' || { echo 'MySQL management account requires SUPER or SYSTEM_VARIABLES_ADMIN for architecture changes' >&2; exit 77; }; " +
		"printf '%s\\n' \"$grant_output\" | grep -Eiq 'GRANT ALL PRIVILEGES|REPLICATION (SLAVE|REPLICA)' || { echo 'MySQL management account requires REPLICATION SLAVE for architecture changes' >&2; exit 77; }"
}

func mysqlArchitectureClient(password string, port int) string {
	if port <= 0 {
		port = 3306
	}
	if password == "" {
		return fmt.Sprintf("mysql --defaults-extra-file=__GMHA_MYSQL_DEFAULTS_FILE__ --protocol=tcp --host=127.0.0.1 --port=%d --connect-timeout=5", port)
	}
	return fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=tcp --host=127.0.0.1 --port=%d --user=root --connect-timeout=5", shellQuote(password), port)
}

func mysqlArchitectureRootSocketClient(password, socket string) string {
	return fmt.Sprintf("MYSQL_PWD=%s mysql --protocol=socket --socket=%s --user=root --connect-timeout=5", shellQuote(password), shellQuote(socket))
}

func killBusinessSessionsCommand(req hadomain.ArchitectureAdjustmentRequest, machineID string, port int) string {
	users := append([]string{"root", "mysql.sys", "mysql.session", "mysql.infoschema", "event_scheduler", "system user", req.ReplicationUser}, req.ManagementUsers...)
	seen, literals := map[string]bool{}, make([]string, 0, len(users))
	for _, user := range users {
		if user = strings.TrimSpace(user); user != "" && !seen[user] {
			seen[user] = true
			literals = append(literals, sqlLiteral(user))
		}
	}
	filter := "ID<>CONNECTION_ID() AND USER IS NOT NULL AND COMMAND<>'Daemon' AND USER NOT IN (" + strings.Join(literals, ",") + ")"
	killQuery := "SELECT CONCAT('KILL CONNECTION ',ID,';') FROM information_schema.PROCESSLIST WHERE " + filter
	verifyQuery := "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE " + filter
	client := mysqlArchitectureClient(architectureRootPassword(req, machineID), port)
	list := client + " --batch --skip-column-names --execute=" + shellQuote(killQuery)
	apply := client + " --batch --skip-column-names"
	verify := client + " --batch --skip-column-names --execute=" + shellQuote(verifyQuery)
	return "kill_errors=$(mktemp); trap 'rm -f \"$kill_errors\"' EXIT; " +
		"kill_sql=$(" + list + ") || exit 70; " +
		"if [ -n \"$kill_sql\" ]; then printf '%s\\n' \"$kill_sql\" | " + apply + " >/dev/null 2>\"$kill_errors\" || true; fi; " +
		"remaining=$(" + verify + ") || exit 70; " +
		"if [ \"$remaining\" != 0 ]; then cat \"$kill_errors\" >&2; echo \"$remaining business session(s) remain after offline fencing\" >&2; exit 79; fi; " +
		"echo GMHA_BUSINESS_SESSIONS_CLEARED"
}

func replicationCatchupCommand(password string, port int) string {
	client := mysqlArchitectureClient(password, port)
	scalarClient := client + " --batch --skip-column-names"
	gtidSQL := "SELECT IF(GTID_SUBSET(COALESCE((SELECT RECEIVED_TRANSACTION_SET FROM performance_schema.replication_connection_status LIMIT 1),''),@@GLOBAL.gtid_executed),'YES','NO')"
	return "i=0; while [ $i -lt 60 ]; do status=$(" + client + " -e 'SHOW REPLICA STATUS\\G' 2>/dev/null || " + client + " -e 'SHOW SLAVE STATUS\\G' 2>/dev/null) || exit 70; lag=$(printf '%s\\n' \"$status\" | awk -F': ' '/Seconds_Behind_(Source|Master)/ {print $2; exit}'); io=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_IO_Running|Slave_IO_Running/ {print $2; exit}'); sql=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_SQL_Running|Slave_SQL_Running/ {print $2; exit}'); gtid=$(" + scalarClient + " -e " + shellQuote(gtidSQL) + " 2>/dev/null) || exit 70; if [ \"$lag\" = 0 ] && [ \"$io\" = Yes ] && [ \"$sql\" = Yes ] && [ \"$gtid\" = YES ]; then echo GMHA_REPLICATION_CAUGHT_UP; exit 0; fi; i=$((i+1)); sleep 1; done; echo GMHA_REPLICATION_TIMEOUT >&2; exit 75"
}

func delayedReplicationHealthCommand(password string, port, expectedDelay int) string {
	client := mysqlArchitectureClient(password, port)
	return "status=$(" + client + " -e 'SHOW REPLICA STATUS\\G' 2>/dev/null || " + client + " -e 'SHOW SLAVE STATUS\\G' 2>/dev/null) || exit 70; configured=$(printf '%s\\n' \"$status\" | awk -F': ' '/SQL_Delay/ {print $2; exit}'); io=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_IO_Running|Slave_IO_Running/ {print $2; exit}'); sql=$(printf '%s\\n' \"$status\" | awk -F': ' '/Replica_SQL_Running|Slave_SQL_Running/ {print $2; exit}'); [ \"$configured\" = " + fmt.Sprint(expectedDelay) + " ] && [ \"$io\" = Yes ] && [ \"$sql\" = Yes ]"
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

func (s *HAService) succeedArchitectureRun(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun) {
	now := time.Now().UTC()
	run.Status, run.CurrentStep, run.Error = hadomain.ArchitectureRunSucceeded, "release_lock", ""
	run.UpdatedAt, run.FinishedAt = now, &now
	s.syncArchitectureTrackingTask(ctx, *run)
	_ = runs.SaveArchitectureRun(ctx, *run)
}

func (s *HAService) failArchitectureRun(ctx context.Context, runs architectureRunRepository, run *hadomain.ArchitectureRun, step string, err error) {
	now := time.Now().UTC()
	run.Status, run.CurrentStep, run.Error = hadomain.ArchitectureRunFailed, step, err.Error()
	run.UpdatedAt, run.FinishedAt = now, &now
	if ctx.Err() != nil {
		ctx = context.Background()
	}
	s.syncArchitectureTrackingTask(ctx, *run)
	_ = runs.SaveArchitectureRun(ctx, *run)
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
