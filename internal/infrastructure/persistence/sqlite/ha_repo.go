package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	hadomain "gmha/internal/domain/ha"
)

// HARepository 是高可用领域实体的 SQLite 仓储实现，管理 VIP 配置、故障转移策略、事件等。
type HARepository struct {
	db *sql.DB
}

func NewHARepository(db *sql.DB) *HARepository {
	return &HARepository{db: db}
}

func (r *HARepository) Migrate() error {
	stmts := []string{
		`alter table clusters add column cluster_type text not null default 'mysql_replication'`,
		`alter table clusters add column cluster_status text not null default 'UNKNOWN'`,
		`alter table clusters add column default_failover_mode text not null default 'safe'`,
		`alter table clusters add column default_switch_strategy text not null default 'safe-wait-replay-auto'`,
		`alter table clusters add column enable_vip integer not null default 1`,
		`alter table clusters add column enable_binlog_rescue integer not null default 1`,
		`alter table clusters add column enable_auto_failover integer not null default 1`,
		`alter table clusters add column updated_at text not null default ''`,
		`alter table mysql_instances add column instance_id text not null default ''`,
		`alter table mysql_instances add column cluster_id text not null default ''`,
		`alter table mysql_instances add column role text not null default 'UNKNOWN'`,
		`alter table mysql_instances add column candidate_master integer not null default 1`,
		`alter table mysql_instances add column vip_allowed integer not null default 1`,
		`alter table mysql_instances add column vip_interface text not null default ''`,
		`alter table mysql_instances add column election_priority integer not null default 0`,
		`alter table mysql_instances add column maintenance_mode integer not null default 0`,
	}
	for _, stmt := range stmts {
		_, _ = r.db.Exec(stmt)
	}
	_, err := r.db.Exec(`
		create table if not exists cluster_vip_config (
			id integer primary key autoincrement,
			cluster_id text not null,
			vip_name text default 'default',
			vip_address text not null,
			vip_prefix integer not null default 24,
			vip_route_mode text not null default 'L2_ARP',
			vip_manage_mode text not null default 'GMHA_MANAGED',
			default_interface text,
			allow_manual_adopt integer default 1,
			preempt_enabled integer default 0,
			arping_enabled integer default 1,
			arping_count integer default 3,
			check_after_bind integer default 1,
			external_check_enabled integer default 1,
			bgp_enabled integer default 0,
			bgp_local_as integer,
			bgp_peer_as integer,
			bgp_peer_address text,
			bgp_router_id text,
			bgp_community text,
			cloud_provider text,
			cloud_resource_id text,
			enabled integer default 1,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP,
			unique(cluster_id, vip_address)
		);
		create table if not exists vip_binding_state (
			id integer primary key autoincrement,
			cluster_id text not null,
			vip_config_id integer not null,
			vip_address text not null,
			expected_holder_instance_id text,
			expected_holder_machine_id text,
			current_holder_instance_id text,
			current_holder_machine_id text,
			current_interface text,
			vip_status text not null default 'UNKNOWN',
			detected_holders text,
			last_check_result text,
			last_error text,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP,
			unique(cluster_id, vip_address)
		);
		create table if not exists cluster_failover_policy (
			id integer primary key autoincrement,
			cluster_id text not null unique,
			failover_mode text not null default 'safe',
			switch_strategy text not null default 'safe-wait-replay-auto',
			auto_failover_enabled integer default 1,
			wait_relay_replay_enabled integer default 1,
			wait_relay_replay_timeout_seconds integer default 60,
			require_delay_zero_before_promote integer default 1,
			max_allowed_delay_seconds integer default 0,
			reselect_candidate_after_replay integer default 1,
			require_old_master_fence integer default 1,
			binlog_rescue_enabled integer default 1,
			binlog_rescue_timeout_seconds integer default 120,
			allow_data_loss integer default 0,
			stop_on_binlog_rescue_failure integer default 1,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP
		);
		create table if not exists cluster_fencing_policy (
			id integer primary key autoincrement,
			cluster_id text not null unique,
			require_old_master_fence integer default 1,
			agent_fence_enabled integer default 1,
			ssh_fence_enabled integer default 1,
			set_readonly_enabled integer default 1,
			stop_mysql_enabled integer default 1,
			del_vip_enabled integer default 1,
			iptables_fence_enabled integer default 0,
			fence_device_enabled integer default 0,
			external_fence_enabled integer default 0,
			external_fence_command text,
			external_fence_timeout_seconds integer default 30,
			allow_failover_when_old_master_unreachable integer default 0,
			check_vip_conflict_before_move integer default 1,
			check_vip_conflict_after_move integer default 1,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP
		);
		create table if not exists cluster_network_policy (
			id integer primary key autoincrement,
			cluster_id text not null unique,
			network_topology text default 'L2',
			vip_route_mode text default 'L2_ARP',
			require_same_subnet_for_l2_vip integer default 1,
			allow_multi_nic integer default 1,
			auto_detect_vip_interface integer default 1,
			business_network_cidr text,
			replication_network_cidr text,
			management_network_cidr text,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP
		);
		create table if not exists machine_network_interface (
			id integer primary key autoincrement,
			machine_id text not null,
			interface_name text not null,
			mac_address text,
			ipv4_addresses text,
			ipv6_addresses text,
			network_role text,
			is_up integer default 0,
			mtu integer,
			speed_mbps integer,
			gateway text,
			vlan_id text,
			subnet_cidr text,
			can_bind_vip integer default 0,
			vip_bind_priority integer default 0,
			created_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP,
			unique(machine_id, interface_name)
		);
		create table if not exists failover_event (
			id integer primary key autoincrement,
			failover_id text not null unique,
			cluster_id text not null,
			old_master_instance_id text,
			old_master_machine_id text,
			old_master_ip text,
			first_candidate_instance_id text,
			first_candidate_machine_id text,
			final_new_master_instance_id text,
			final_new_master_machine_id text,
			final_new_master_ip text,
			mode text not null,
			switch_strategy text not null,
			status text not null,
			reason text,
			risk_level text,
			risk_summary text,
			old_master_fenced integer default 0,
			relay_replay_waited integer default 0,
			relay_replay_success integer default 0,
			binlog_rescue_attempted integer default 0,
			binlog_rescue_success integer default 0,
			vip_moved integer default 0,
			started_at text default CURRENT_TIMESTAMP,
			updated_at text default CURRENT_TIMESTAMP,
			finished_at text
		);
		create table if not exists failover_lock (
			id integer primary key autoincrement,
			cluster_id text not null unique,
			failover_id text not null,
			lock_owner text not null,
			locked_at text default CURRENT_TIMESTAMP,
			expires_at text
		);
		create table if not exists vip_operation_log (
			id integer primary key autoincrement,
			cluster_id text not null,
			failover_id text,
			vip_address text not null,
			operation text not null,
			target_machine_id text,
			target_host_ip text,
			target_interface text,
			command text,
			result_code integer,
			stdout text,
			stderr text,
			operator text,
			status text not null,
			created_at text default CURRENT_TIMESTAMP
		);
		create table if not exists binlog_rescue_log (
			id integer primary key autoincrement,
			cluster_id text not null,
			failover_id text not null,
			old_master_instance_id text,
			old_master_machine_id text,
			old_master_ip text,
			candidate_instance_id text,
			candidate_machine_id text,
			candidate_ip text,
			rescue_status text not null,
			gtid_mode integer default 0,
			missing_gtid_set text,
			start_binlog_file text,
			start_binlog_pos integer,
			end_binlog_file text,
			end_binlog_pos integer,
			rescued_binlog_path text,
			applied_sql_path text,
			error_message text,
			started_at text default CURRENT_TIMESTAMP,
			finished_at text
		);
	`)
	return err
}

func (r *HARepository) EnsureDefaultPolicies(ctx context.Context, clusterID string) error {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return errors.New("cluster_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		insert into cluster_failover_policy (cluster_id) values (?)
		on conflict(cluster_id) do nothing;
		insert into cluster_fencing_policy (cluster_id) values (?)
		on conflict(cluster_id) do nothing;
		insert into cluster_network_policy (cluster_id) values (?)
		on conflict(cluster_id) do nothing;
	`, clusterID, clusterID, clusterID)
	return err
}

func (r *HARepository) GetFailoverPolicy(ctx context.Context, clusterID string) (hadomain.FailoverPolicy, error) {
	if err := r.EnsureDefaultPolicies(ctx, clusterID); err != nil {
		return hadomain.FailoverPolicy{}, err
	}
	row := r.db.QueryRowContext(ctx, `
		select cluster_id, failover_mode, switch_strategy, auto_failover_enabled, wait_relay_replay_enabled,
			wait_relay_replay_timeout_seconds, require_delay_zero_before_promote, max_allowed_delay_seconds,
			reselect_candidate_after_replay, require_old_master_fence, binlog_rescue_enabled,
			binlog_rescue_timeout_seconds, allow_data_loss, stop_on_binlog_rescue_failure
		from cluster_failover_policy where cluster_id = ?
	`, strings.TrimSpace(clusterID))
	var p hadomain.FailoverPolicy
	var auto, wait, requireDelay, reselect, fence, rescue, allowLoss, stopOnRescue int
	if err := row.Scan(&p.ClusterID, &p.FailoverMode, &p.SwitchStrategy, &auto, &wait, &p.WaitRelayReplayTimeoutSeconds, &requireDelay, &p.MaxAllowedDelaySeconds, &reselect, &fence, &rescue, &p.BinlogRescueTimeoutSeconds, &allowLoss, &stopOnRescue); err != nil {
		return hadomain.FailoverPolicy{}, err
	}
	p.AutoFailoverEnabled = auto != 0
	p.WaitRelayReplayEnabled = wait != 0
	p.RequireDelayZeroBeforePromote = requireDelay != 0
	p.ReselectCandidateAfterReplay = reselect != 0
	p.RequireOldMasterFence = fence != 0
	p.BinlogRescueEnabled = rescue != 0
	p.AllowDataLoss = allowLoss != 0
	p.StopOnBinlogRescueFailure = stopOnRescue != 0
	return p, nil
}

func (r *HARepository) GetNetworkPolicy(ctx context.Context, clusterID string) (hadomain.NetworkPolicy, error) {
	if err := r.EnsureDefaultPolicies(ctx, clusterID); err != nil {
		return hadomain.NetworkPolicy{}, err
	}
	row := r.db.QueryRowContext(ctx, `
		select cluster_id, network_topology, vip_route_mode, require_same_subnet_for_l2_vip,
			allow_multi_nic, auto_detect_vip_interface, coalesce(business_network_cidr,''), coalesce(replication_network_cidr,''), coalesce(management_network_cidr,'')
		from cluster_network_policy where cluster_id = ?
	`, strings.TrimSpace(clusterID))
	var p hadomain.NetworkPolicy
	var sameSubnet, multiNIC, autoDetect int
	if err := row.Scan(&p.ClusterID, &p.NetworkTopology, &p.VIPRouteMode, &sameSubnet, &multiNIC, &autoDetect, &p.BusinessNetworkCIDR, &p.ReplicationNetworkCIDR, &p.ManagementNetworkCIDR); err != nil {
		return hadomain.NetworkPolicy{}, err
	}
	p.RequireSameSubnetForL2VIP = sameSubnet != 0
	p.AllowMultiNIC = multiNIC != 0
	p.AutoDetectVIPInterface = autoDetect != 0
	return p, nil
}

func (r *HARepository) ListVIPConfigs(ctx context.Context, clusterID string) ([]hadomain.ClusterVIPConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, cluster_id, coalesce(vip_name,''), vip_address, vip_prefix, vip_route_mode, vip_manage_mode,
			coalesce(default_interface,''), allow_manual_adopt, preempt_enabled, arping_enabled, arping_count,
			check_after_bind, external_check_enabled, enabled, coalesce(created_at,''), coalesce(updated_at,'')
		from cluster_vip_config where cluster_id = ? and enabled = 1 order by id
	`, strings.TrimSpace(clusterID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hadomain.ClusterVIPConfig
	for rows.Next() {
		var item hadomain.ClusterVIPConfig
		var adopt, preempt, arping, check, external, enabled int
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.ClusterID, &item.VIPName, &item.VIPAddress, &item.VIPPrefix, &item.VIPRouteMode, &item.VIPManageMode, &item.DefaultInterface, &adopt, &preempt, &arping, &item.ArpingCount, &check, &external, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.AllowManualAdopt = adopt != 0
		item.PreemptEnabled = preempt != 0
		item.ArpingEnabled = arping != 0
		item.CheckAfterBind = check != 0
		item.ExternalCheckEnabled = external != 0
		item.Enabled = enabled != 0
		item.CreatedAt, _ = parseDBTime(createdAt)
		item.UpdatedAt, _ = parseDBTime(updatedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HARepository) UpsertVIPBindingState(ctx context.Context, state hadomain.VIPBindingState) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if state.VIPStatus == "" {
		state.VIPStatus = hadomain.VipStatusUnknown
	}
	_, err := r.db.ExecContext(ctx, `
		insert into vip_binding_state (
			cluster_id, vip_config_id, vip_address, expected_holder_instance_id, expected_holder_machine_id,
			current_holder_instance_id, current_holder_machine_id, current_interface, vip_status,
			detected_holders, last_check_result, last_error, created_at, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(cluster_id, vip_address) do update set
			vip_config_id=excluded.vip_config_id,
			expected_holder_instance_id=excluded.expected_holder_instance_id,
			expected_holder_machine_id=excluded.expected_holder_machine_id,
			current_holder_instance_id=excluded.current_holder_instance_id,
			current_holder_machine_id=excluded.current_holder_machine_id,
			current_interface=excluded.current_interface,
			vip_status=excluded.vip_status,
			detected_holders=excluded.detected_holders,
			last_check_result=excluded.last_check_result,
			last_error=excluded.last_error,
			updated_at=excluded.updated_at
	`, state.ClusterID, state.VIPConfigID, state.VIPAddress, state.ExpectedHolderInstanceID, state.ExpectedHolderMachineID, state.CurrentHolderInstanceID, state.CurrentHolderMachineID, state.CurrentInterface, state.VIPStatus, state.DetectedHolders, state.LastCheckResult, state.LastError, now, now)
	return err
}

func (r *HARepository) GetVIPBindingStates(ctx context.Context, clusterID string) ([]hadomain.VIPBindingState, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, cluster_id, vip_config_id, vip_address, coalesce(expected_holder_instance_id,''), coalesce(expected_holder_machine_id,''),
			coalesce(current_holder_instance_id,''), coalesce(current_holder_machine_id,''), coalesce(current_interface,''),
			vip_status, coalesce(detected_holders,''), coalesce(last_check_result,''), coalesce(last_error,''), coalesce(created_at,''), coalesce(updated_at,'')
		from vip_binding_state where cluster_id = ? order by vip_address
	`, strings.TrimSpace(clusterID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hadomain.VIPBindingState
	for rows.Next() {
		var item hadomain.VIPBindingState
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.ClusterID, &item.VIPConfigID, &item.VIPAddress, &item.ExpectedHolderInstanceID, &item.ExpectedHolderMachineID, &item.CurrentHolderInstanceID, &item.CurrentHolderMachineID, &item.CurrentInterface, &item.VIPStatus, &item.DetectedHolders, &item.LastCheckResult, &item.LastError, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = parseDBTime(createdAt)
		item.UpdatedAt, _ = parseDBTime(updatedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HARepository) ListMachineInterfaces(ctx context.Context, machineID string) ([]hadomain.MachineNetworkInterface, error) {
	rows, err := r.db.QueryContext(ctx, `
		select machine_id, interface_name, coalesce(mac_address,''), coalesce(ipv4_addresses,''), coalesce(ipv6_addresses,''),
			coalesce(network_role,''), is_up, coalesce(mtu,0), coalesce(speed_mbps,0), coalesce(gateway,''), coalesce(vlan_id,''),
			coalesce(subnet_cidr,''), can_bind_vip, vip_bind_priority
		from machine_network_interface where machine_id = ? order by can_bind_vip desc, vip_bind_priority desc, interface_name asc
	`, strings.TrimSpace(machineID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hadomain.MachineNetworkInterface
	for rows.Next() {
		var item hadomain.MachineNetworkInterface
		var isUp, canBind int
		if err := rows.Scan(&item.MachineID, &item.InterfaceName, &item.MACAddress, &item.IPv4Addresses, &item.IPv6Addresses, &item.NetworkRole, &isUp, &item.MTU, &item.SpeedMbps, &item.Gateway, &item.VLANID, &item.SubnetCIDR, &canBind, &item.VIPBindPriority); err != nil {
			return nil, err
		}
		item.IsUp = isUp != 0
		item.CanBindVIP = canBind != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *HARepository) AcquireFailoverLock(ctx context.Context, clusterID, failoverID, owner string, ttl time.Duration) error {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl).Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, `
		insert into failover_lock (cluster_id, failover_id, lock_owner, locked_at, expires_at)
		select ?, ?, ?, ?, ?
		where not exists (
			select 1 from failover_lock
			where cluster_id = ? and (expires_at is null or expires_at = '' or expires_at > ?)
		)
	`, clusterID, failoverID, owner, now.Format(time.RFC3339), expiresAt, clusterID, now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("failover lock for cluster %s is already held", clusterID)
	}
	return nil
}

func (r *HARepository) ReleaseFailoverLock(ctx context.Context, clusterID, failoverID string) error {
	_, err := r.db.ExecContext(ctx, `delete from failover_lock where cluster_id = ? and failover_id = ?`, clusterID, failoverID)
	return err
}

func (r *HARepository) SaveFailoverEvent(ctx context.Context, event hadomain.FailoverEvent) error {
	now := time.Now().UTC().Format(time.RFC3339)
	startedAt := now
	if !event.StartedAt.IsZero() {
		startedAt = event.StartedAt.UTC().Format(time.RFC3339)
	}
	var finished any
	if !event.FinishedAt.IsZero() {
		finished = event.FinishedAt.UTC().Format(time.RFC3339)
	}
	_, err := r.db.ExecContext(ctx, `
		insert into failover_event (
			failover_id, cluster_id, old_master_instance_id, old_master_machine_id, old_master_ip,
			first_candidate_instance_id, first_candidate_machine_id, final_new_master_instance_id,
			final_new_master_machine_id, final_new_master_ip, mode, switch_strategy, status, reason,
			risk_level, risk_summary, old_master_fenced, relay_replay_waited, relay_replay_success,
			binlog_rescue_attempted, binlog_rescue_success, vip_moved, started_at, updated_at, finished_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(failover_id) do update set
			status=excluded.status,
			reason=excluded.reason,
			risk_level=excluded.risk_level,
			risk_summary=excluded.risk_summary,
			first_candidate_instance_id=excluded.first_candidate_instance_id,
			first_candidate_machine_id=excluded.first_candidate_machine_id,
			final_new_master_instance_id=excluded.final_new_master_instance_id,
			final_new_master_machine_id=excluded.final_new_master_machine_id,
			final_new_master_ip=excluded.final_new_master_ip,
			old_master_fenced=excluded.old_master_fenced,
			relay_replay_waited=excluded.relay_replay_waited,
			relay_replay_success=excluded.relay_replay_success,
			binlog_rescue_attempted=excluded.binlog_rescue_attempted,
			binlog_rescue_success=excluded.binlog_rescue_success,
			vip_moved=excluded.vip_moved,
			updated_at=excluded.updated_at,
			finished_at=excluded.finished_at
	`, event.FailoverID, event.ClusterID, event.OldMasterInstanceID, event.OldMasterMachineID, event.OldMasterIP, event.FirstCandidateInstanceID, event.FirstCandidateMachineID, event.FinalNewMasterInstanceID, event.FinalNewMasterMachineID, event.FinalNewMasterIP, event.Mode, event.SwitchStrategy, event.Status, event.Reason, event.RiskLevel, event.RiskSummary, haBoolInt(event.OldMasterFenced), haBoolInt(event.RelayReplayWaited), haBoolInt(event.RelayReplaySuccess), haBoolInt(event.BinlogRescueAttempted), haBoolInt(event.BinlogRescueSuccess), haBoolInt(event.VIPMoved), startedAt, now, finished)
	return err
}

func (r *HARepository) GetFailoverEvent(ctx context.Context, clusterID, failoverID string) (hadomain.FailoverEvent, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select failover_id, cluster_id, coalesce(old_master_instance_id,''), coalesce(old_master_machine_id,''), coalesce(old_master_ip,''),
			coalesce(first_candidate_instance_id,''), coalesce(first_candidate_machine_id,''), coalesce(final_new_master_instance_id,''),
			coalesce(final_new_master_machine_id,''), coalesce(final_new_master_ip,''), mode, switch_strategy, status, coalesce(reason,''),
			coalesce(risk_level,''), coalesce(risk_summary,''), old_master_fenced, relay_replay_waited, relay_replay_success,
			binlog_rescue_attempted, binlog_rescue_success, vip_moved, coalesce(started_at,''), coalesce(updated_at,''), coalesce(finished_at,'')
		from failover_event where cluster_id = ? and failover_id = ?
	`, clusterID, failoverID)
	var e hadomain.FailoverEvent
	var oldFenced, replayWaited, replayOK, rescueTried, rescueOK, vipMoved int
	var started, updated, finished string
	if err := row.Scan(&e.FailoverID, &e.ClusterID, &e.OldMasterInstanceID, &e.OldMasterMachineID, &e.OldMasterIP, &e.FirstCandidateInstanceID, &e.FirstCandidateMachineID, &e.FinalNewMasterInstanceID, &e.FinalNewMasterMachineID, &e.FinalNewMasterIP, &e.Mode, &e.SwitchStrategy, &e.Status, &e.Reason, &e.RiskLevel, &e.RiskSummary, &oldFenced, &replayWaited, &replayOK, &rescueTried, &rescueOK, &vipMoved, &started, &updated, &finished); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return hadomain.FailoverEvent{}, false, nil
		}
		return hadomain.FailoverEvent{}, false, err
	}
	e.OldMasterFenced = oldFenced != 0
	e.RelayReplayWaited = replayWaited != 0
	e.RelayReplaySuccess = replayOK != 0
	e.BinlogRescueAttempted = rescueTried != 0
	e.BinlogRescueSuccess = rescueOK != 0
	e.VIPMoved = vipMoved != 0
	e.StartedAt, _ = parseDBTime(started)
	e.UpdatedAt, _ = parseDBTime(updated)
	e.FinishedAt, _ = parseDBTime(finished)
	return e, true, nil
}

func (r *HARepository) InsertVIPOperationLog(ctx context.Context, clusterID, failoverID, vip, operation, machineID, hostIP, iface, command string, resultCode int, stdout, stderr, operator, status string) error {
	_, err := r.db.ExecContext(ctx, `
		insert into vip_operation_log (cluster_id, failover_id, vip_address, operation, target_machine_id, target_host_ip, target_interface, command, result_code, stdout, stderr, operator, status)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, clusterID, failoverID, vip, operation, machineID, hostIP, iface, command, resultCode, stdout, stderr, operator, status)
	return err
}

func haBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func parseDBTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}
