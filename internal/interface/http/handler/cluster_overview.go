package handler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	dynamicdomain "gmha/internal/domain/dynamic"
	heartbeatdomain "gmha/internal/domain/heartbeat"
)

type clusterOverviewView struct {
	GeneratedAt  string                   `json:"generated_at"`
	RangeMinutes int                      `json:"range_minutes"`
	HasHistory   bool                     `json:"has_history"`
	DataSource   string                   `json:"data_source"`
	Instance     string                   `json:"instance"`
	Summary      clusterOverviewSummary   `json:"summary"`
	Series       []clusterOverviewPoint   `json:"series"`
	Machines     []clusterOverviewMachine `json:"machines"`
	Storage      []clusterOverviewStorage `json:"storage"`
}

type clusterOverviewSummary struct {
	QPS                  float64 `json:"qps"`
	TPS                  float64 `json:"tps"`
	ConnectedSessions    float64 `json:"connected_sessions"`
	ActiveSessions       float64 `json:"active_sessions"`
	RunningThreads       float64 `json:"running_threads"`
	SleepSessions        float64 `json:"sleep_sessions"`
	ConnectionUsage      float64 `json:"connection_usage_percent"`
	LockWaitSessions     float64 `json:"lock_wait_sessions"`
	BlockedSessions      float64 `json:"blocked_sessions"`
	RowLockWaits         float64 `json:"row_lock_waits"`
	MetadataLockWaits    float64 `json:"metadata_lock_waits"`
	ActiveTransactions   float64 `json:"active_transactions"`
	LongestTransaction   float64 `json:"longest_transaction_seconds"`
	SlowQueriesPerMinute float64 `json:"slow_queries_per_min"`
	DeadlocksPerMinute   float64 `json:"deadlocks_per_min"`
	ReplicationLag       float64 `json:"replication_lag_seconds"`
	BufferPoolHitRatio   float64 `json:"buffer_pool_hit_ratio"`
	TableScanRatio       float64 `json:"table_scan_ratio"`
	TmpDiskTableRatio    float64 `json:"tmp_disk_table_ratio"`
	CPUPercent           float64 `json:"cpu_percent"`
	IOBusyPercent        float64 `json:"io_busy_percent"`
	IOReadBytes          float64 `json:"io_read_bytes_sec"`
	IOWriteBytes         float64 `json:"io_write_bytes_sec"`
	DiskUsedPercent      float64 `json:"disk_used_percent"`
	NetworkReceiveBytes  float64 `json:"network_receive_bytes_sec"`
	NetworkTransmitBytes float64 `json:"network_transmit_bytes_sec"`
	DataBytes            float64 `json:"data_bytes"`
	IndexBytes           float64 `json:"index_bytes"`
	FragmentBytes        float64 `json:"fragment_bytes"`
	FragmentPercent      float64 `json:"fragment_percent"`
	Architecture         string  `json:"architecture"`
	InstanceCount        int     `json:"instance_count"`
	MachineCount         int     `json:"machine_count"`
	bufferPoolHitSum     float64
	bufferPoolHitCount   int
	connectionUsageSum   float64
	connectionUsageCount int
	tableScanRatioSum    float64
	tableScanRatioCount  int
	tmpDiskRatioSum      float64
	tmpDiskRatioCount    int
}

type clusterOverviewPoint struct {
	Timestamp            string  `json:"timestamp"`
	QPS                  float64 `json:"qps"`
	TPS                  float64 `json:"tps"`
	ConnectedSessions    float64 `json:"connected_sessions"`
	ActiveSessions       float64 `json:"active_sessions"`
	RunningThreads       float64 `json:"running_threads"`
	LockWaitSessions     float64 `json:"lock_wait_sessions"`
	BlockedSessions      float64 `json:"blocked_sessions"`
	MetadataLockWaits    float64 `json:"metadata_lock_waits"`
	ActiveTransactions   float64 `json:"active_transactions"`
	LongestTransaction   float64 `json:"longest_transaction_seconds"`
	SlowQueriesPerMinute float64 `json:"slow_queries_per_min"`
	DeadlocksPerMinute   float64 `json:"deadlocks_per_min"`
	ReplicationLag       float64 `json:"replication_lag_seconds"`
	BufferPoolHitRatio   float64 `json:"buffer_pool_hit_ratio"`
	CPUPercent           float64 `json:"cpu_percent"`
	IOBusyPercent        float64 `json:"io_busy_percent"`
	IOReadBytes          float64 `json:"io_read_bytes_sec"`
	IOWriteBytes         float64 `json:"io_write_bytes_sec"`
	DiskUsedPercent      float64 `json:"disk_used_percent"`
	NetworkReceiveBytes  float64 `json:"network_receive_bytes_sec"`
	NetworkTransmitBytes float64 `json:"network_transmit_bytes_sec"`
}

type clusterOverviewMachine struct {
	MachineID       string  `json:"machine_id"`
	Name            string  `json:"name"`
	IP              string  `json:"ip"`
	CPUPercent      float64 `json:"cpu_percent"`
	IOBusyPercent   float64 `json:"io_busy_percent"`
	DiskUsedPercent float64 `json:"disk_used_percent"`
	NetworkBytes    float64 `json:"network_bytes_sec"`
	Status          string  `json:"status"`
}

type clusterOverviewStorage struct {
	ID             string   `json:"id"`
	MachineID      string   `json:"machine_id"`
	MachineName    string   `json:"machine_name"`
	IP             string   `json:"ip"`
	Mount          string   `json:"mount"`
	Source         string   `json:"source,omitempty"`
	FSType         string   `json:"fs_type,omitempty"`
	Purposes       []string `json:"purposes"`
	Paths          []string `json:"paths"`
	Ports          []int    `json:"ports,omitempty"`
	TotalBytes     uint64   `json:"total_bytes"`
	UsedBytes      uint64   `json:"used_bytes"`
	AvailableBytes uint64   `json:"available_bytes"`
	UsedPercent    float64  `json:"used_percent"`
	Available      bool     `json:"available"`
}

type overviewStorageUsage struct {
	path, mount, source, fsType           string
	totalBytes, usedBytes, availableBytes uint64
	usedPercent                           float64
	available                             bool
}

type overviewBucket struct {
	at                                                   time.Time
	qps, tps, cpu, ioBusy, ioRead, ioWrite, netRX, netTX map[string]*overviewAverage
	connected, active, running, lockWait, blocked, metadataLock,
	activeTransactions, longestTransaction, replicationLag, bufferPoolHit map[string]*overviewAverage
	slowQueriesPerMinute, deadlocksPerMinute map[string]*overviewAverage
	disk                                     float64
}
type overviewAverage struct {
	sum   float64
	count int
}
type overviewCounter struct {
	value float64
	at    time.Time
}

func (h *ClusterTopologyHandler) buildOverview(ctx context.Context, cluster string, nodes []clusterTopologyNode, rangeMinutes int, now time.Time, instanceSelectors ...string) clusterOverviewView {
	instanceSelector := ""
	if len(instanceSelectors) > 0 {
		instanceSelector = strings.TrimSpace(instanceSelectors[0])
	}
	view := clusterOverviewView{GeneratedAt: now.Local().Format("2006-01-02 15:04:05"), RangeMinutes: rangeMinutes, DataSource: "waiting", Instance: instanceSelector, Series: []clusterOverviewPoint{}, Machines: []clusterOverviewMachine{}, Storage: []clusterOverviewStorage{}}
	view.Summary.InstanceCount = len(nodes)
	roles := map[string]int{}
	machineNodes := map[string]clusterTopologyNode{}
	for _, node := range nodes {
		roles[strings.ToUpper(node.Role)]++
		if _, ok := machineNodes[node.MachineID]; !ok {
			machineNodes[node.MachineID] = node
		}
	}
	if machines, err := h.machines.ListMachines(ctx); err == nil {
		for _, machine := range machines {
			if machine.Cluster != cluster {
				continue
			}
			if instanceSelector != "" {
				if _, selected := machineNodes[machine.ID]; !selected {
					continue
				}
			}
			if existing, ok := machineNodes[machine.ID]; ok {
				existing.Name, existing.IP = machine.Name, machine.IP
				machineNodes[machine.ID] = existing
				continue
			}
			machineNodes[machine.ID] = clusterTopologyNode{MachineID: machine.ID, Name: machine.Name, IP: machine.IP, Heartbeat: string(machine.Status)}
		}
	}
	view.Summary.MachineCount = len(machineNodes)
	view.Summary.Architecture = overviewArchitecture(roles, len(nodes))
	if len(nodes) > 0 {
		view.DataSource = "current_estimate"
		// Existing Agents already report monotonic Questions/transaction counters
		// and uptime. Before two persisted snapshots exist, expose a useful
		// startup-average rate instead of an empty dashboard.
		view.Summary.QPS, view.Summary.TPS = overviewStartupRates(nodes)
	} else if len(machineNodes) > 0 {
		view.DataSource = "host_only"
	}

	// Capacity and current host gauges come from the latest heartbeat cache, so
	// a fresh installation has useful cards before enough history is accumulated.
	hostFilesystems := make(map[string][]overviewStorageUsage, len(machineNodes))
	storageByKey := make(map[string]*clusterOverviewStorage)
	for _, node := range machineNodes {
		machine := clusterOverviewMachine{MachineID: node.MachineID, Name: node.Name, IP: node.IP, Status: node.Heartbeat}
		metrics, err := h.machines.GetMachineDynamicMetrics(ctx, node.IP)
		if err == nil {
			if metrics.HeartbeatState != "" {
				machine.Status = metrics.HeartbeatState
			}
			for _, metric := range metrics.Metrics {
				applyCurrentHostMetric(&view.Summary, &machine, metric)
				switch metric.Name {
				case "filesystem_usage":
					hostFilesystems[node.MachineID] = overviewStorageUsages(metric.Value)
				case "swap_usage":
					for _, usage := range overviewStorageUsages(metric.Value) {
						if usage.mount == "" {
							usage.mount = "swap"
						}
						if usage.source == "" {
							usage.source = "/proc/swaps"
						}
						usage.fsType = "swap"
						addOverviewStorage(storageByKey, node, usage, "Swap", "", 0)
					}
				}
			}
		}
		view.Machines = append(view.Machines, machine)
	}
	if len(view.Machines) > 0 {
		var cpu float64
		for _, machine := range view.Machines {
			cpu += machine.CPUPercent
		}
		view.Summary.CPUPercent = roundOverview(cpu / float64(len(view.Machines)))
	}
	machinesByID := make(map[string]*clusterOverviewMachine, len(view.Machines))
	for i := range view.Machines {
		machinesByID[view.Machines[i].MachineID] = &view.Machines[i]
	}
	for _, node := range nodes {
		metrics, err := h.machines.GetMySQLDynamicMetrics(ctx, topologyEndpoint(node.IP, node.Port))
		if err != nil {
			continue
		}
		for _, metric := range metrics.Metrics {
			applyCurrentMySQLMetric(&view.Summary, machinesByID[node.MachineID], metric)
			purpose := overviewStoragePurpose(metric.Name)
			if purpose == "" || !metric.Success {
				continue
			}
			usage, ok := overviewSingleStorageUsage(metric.Value)
			if !ok {
				continue
			}
			path := usage.path
			if filesystem, found := overviewFilesystemForPath(hostFilesystems[node.MachineID], path); found {
				usage = filesystem
				usage.path = path
			}
			addOverviewStorage(storageByKey, machineNodes[node.MachineID], usage, purpose, path, node.Port)
		}
	}
	if h.backup != nil {
		if policies, err := h.backup.ListPolicies(ctx, cluster); err == nil {
			for _, policy := range policies {
				node, ok := machineNodes[policy.MachineID]
				if !ok {
					continue
				}
				if instanceSelector != "" && !overviewPolicyMatchesNodes(policy.MachineID, policy.Port, nodes) {
					continue
				}
				usage := overviewStorageUsage{path: policy.BackupLocation}
				if filesystem, found := overviewFilesystemForPath(hostFilesystems[policy.MachineID], policy.BackupLocation); found {
					usage = filesystem
					usage.path = policy.BackupLocation
				}
				addOverviewStorage(storageByKey, node, usage, "备份/NAS", policy.BackupLocation, policy.Port)
			}
		}
	}
	for machineID, filesystems := range hostFilesystems {
		node := machineNodes[machineID]
		for _, usage := range filesystems {
			key := overviewStorageKey(machineID, usage)
			item := storageByKey[key]
			switch {
			case usage.mount == "/":
				addOverviewStorage(storageByKey, node, usage, "系统", usage.mount, 0)
			case overviewNetworkFilesystem(usage.fsType) && item == nil:
				addOverviewStorage(storageByKey, node, usage, "NAS", usage.mount, 0)
			case item == nil:
				addOverviewStorage(storageByKey, node, usage, "其他挂载", usage.mount, 0)
			}
		}
	}
	for _, item := range storageByKey {
		sort.Strings(item.Purposes)
		sort.Strings(item.Paths)
		sort.Ints(item.Ports)
		view.Storage = append(view.Storage, *item)
	}
	sort.Slice(view.Storage, func(i, j int) bool {
		leftPriority := overviewStoragePriority(view.Storage[i])
		rightPriority := overviewStoragePriority(view.Storage[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if view.Storage[i].Mount != view.Storage[j].Mount {
			return view.Storage[i].Mount < view.Storage[j].Mount
		}
		if view.Storage[i].MachineName != view.Storage[j].MachineName {
			return view.Storage[i].MachineName < view.Storage[j].MachineName
		}
		if view.Storage[i].UsedPercent != view.Storage[j].UsedPercent {
			return view.Storage[i].UsedPercent > view.Storage[j].UsedPercent
		}
		return view.Storage[i].ID < view.Storage[j].ID
	})
	if view.Summary.bufferPoolHitCount > 0 {
		view.Summary.BufferPoolHitRatio = roundOverview(view.Summary.bufferPoolHitSum / float64(view.Summary.bufferPoolHitCount))
	}
	if view.Summary.connectionUsageCount > 0 {
		view.Summary.ConnectionUsage = roundOverview(view.Summary.connectionUsageSum / float64(view.Summary.connectionUsageCount))
	}
	if view.Summary.tableScanRatioCount > 0 {
		view.Summary.TableScanRatio = roundOverview(view.Summary.tableScanRatioSum / float64(view.Summary.tableScanRatioCount))
	}
	if view.Summary.tmpDiskRatioCount > 0 {
		view.Summary.TmpDiskTableRatio = roundOverview(view.Summary.tmpDiskRatioSum / float64(view.Summary.tmpDiskRatioCount))
	}
	if total := view.Summary.DataBytes + view.Summary.IndexBytes; total > 0 {
		view.Summary.FragmentPercent = roundOverview(100 * view.Summary.FragmentBytes / total)
	}

	if h.heartbeat != nil {
		startAt := now.Add(-time.Duration(rangeMinutes) * time.Minute)
		snapshots, err := h.heartbeat.MetricHistoryRange(ctx, cluster, startAt, now, 20000)
		if err == nil {
			if instanceSelector != "" && len(nodes) == 1 {
				snapshots = filterOverviewSnapshotsForInstance(snapshots, nodes[0])
			}
			view.Series = aggregateOverviewHistory(snapshots, rangeMinutes, startAt, now)
			view.HasHistory = len(view.Series) > 1
			if view.HasHistory {
				view.DataSource = "history"
			}
		}
	}
	if len(view.Series) > 0 {
		last := view.Series[len(view.Series)-1]
		if view.HasHistory {
			view.Summary.QPS, view.Summary.TPS = last.QPS, last.TPS
			view.Summary.ConnectedSessions = last.ConnectedSessions
			view.Summary.ActiveSessions = last.ActiveSessions
			view.Summary.RunningThreads = last.RunningThreads
			view.Summary.LockWaitSessions = last.LockWaitSessions
			view.Summary.BlockedSessions = last.BlockedSessions
			view.Summary.MetadataLockWaits = last.MetadataLockWaits
			view.Summary.ActiveTransactions = last.ActiveTransactions
			view.Summary.LongestTransaction = last.LongestTransaction
			view.Summary.SlowQueriesPerMinute = last.SlowQueriesPerMinute
			view.Summary.DeadlocksPerMinute = last.DeadlocksPerMinute
			view.Summary.ReplicationLag = last.ReplicationLag
			view.Summary.BufferPoolHitRatio = last.BufferPoolHitRatio
		}
		if last.CPUPercent > 0 {
			view.Summary.CPUPercent = last.CPUPercent
		}
		view.Summary.IOBusyPercent, view.Summary.IOReadBytes, view.Summary.IOWriteBytes = last.IOBusyPercent, last.IOReadBytes, last.IOWriteBytes
		if last.DiskUsedPercent > 0 {
			view.Summary.DiskUsedPercent = last.DiskUsedPercent
		}
		view.Summary.NetworkReceiveBytes, view.Summary.NetworkTransmitBytes = last.NetworkReceiveBytes, last.NetworkTransmitBytes
	}
	sort.Slice(view.Machines, func(i, j int) bool { return view.Machines[i].Name < view.Machines[j].Name })
	return view
}

func overviewStartupRates(nodes []clusterTopologyNode) (qps, tps float64) {
	for _, node := range nodes {
		uptime, uptimeOK := overviewNumber(node.Uptime)
		questions, qpsOK := overviewNumber(node.QPS)
		transactions, tpsOK := overviewNumber(node.TPS)
		if !uptimeOK || uptime <= 0 {
			continue
		}
		if qpsOK {
			qps += questions / uptime
		}
		if tpsOK {
			tps += transactions / uptime
		}
	}
	return roundOverview(qps), roundOverview(tps)
}

func overviewArchitecture(roles map[string]int, total int) string {
	if total == 0 {
		return "尚未部署实例"
	}
	primary := roles["M"] + roles["M/S"]
	replica := roles["S"] + roles["M/S"] + roles["READONLY"]
	switch {
	case primary == 1 && replica > 0:
		return fmt.Sprintf("一主 %d 从", replica)
	case primary > 1:
		return fmt.Sprintf("%d 个主节点 · %d 个副本", primary, replica)
	case total == 1:
		return "单实例"
	default:
		return fmt.Sprintf("%d 个独立实例", total)
	}
}

func applyCurrentHostMetric(summary *clusterOverviewSummary, machine *clusterOverviewMachine, metric dynamicdomain.MetricResult) {
	if !metric.Success {
		return
	}
	switch metric.Name {
	case "cpu_usage_percent":
		machine.CPUPercent, _ = overviewNumber(metric.Value)
	case "io_status":
		busy, read, write := overviewIO(metric.Value)
		machine.IOBusyPercent = busy
		summary.IOBusyPercent = math.Max(summary.IOBusyPercent, busy)
		summary.IOReadBytes += read
		summary.IOWriteBytes += write
	case "network_throughput":
		rx, tx := overviewNetwork(metric.Value)
		machine.NetworkBytes = rx + tx
		summary.NetworkReceiveBytes += rx
		summary.NetworkTransmitBytes += tx
	}
}

func applyCurrentMySQLMetric(summary *clusterOverviewSummary, machine *clusterOverviewMachine, metric dynamicdomain.MetricResult) {
	if !metric.Success {
		return
	}
	value, ok := overviewNumber(metric.Value)
	switch metric.Name {
	case "mysql_table_data_total_bytes":
		if ok {
			summary.DataBytes += value
		}
	case "mysql_index_data_total_bytes":
		if ok {
			summary.IndexBytes += value
		}
	case "mysql_tablespace_fragment_total_bytes":
		if ok {
			summary.FragmentBytes += value
		}
	case "mysql_data_disk_usage":
		usedPercent := overviewDisk(metric.Value)
		summary.DiskUsedPercent = math.Max(summary.DiskUsedPercent, usedPercent)
		if machine != nil {
			machine.DiskUsedPercent = math.Max(machine.DiskUsedPercent, usedPercent)
		}
	case "mysql_threads_connected":
		if ok {
			summary.ConnectedSessions += value
		}
	case "mysql_active_connections":
		if ok {
			summary.ActiveSessions += value
		}
	case "mysql_threads_running":
		if ok {
			summary.RunningThreads += value
		}
	case "mysql_sleep_connections":
		if ok {
			summary.SleepSessions += value
		}
	case "mysql_connection_usage_percent":
		if ok {
			summary.connectionUsageSum += value
			summary.connectionUsageCount++
		}
	case "mysql_lock_wait_sessions":
		if ok {
			summary.LockWaitSessions += value
		}
	case "mysql_blocked_sessions":
		if ok {
			summary.BlockedSessions += value
		}
	case "mysql_row_lock_waits_current":
		if ok {
			summary.RowLockWaits += value
		}
	case "mysql_metadata_lock_waits":
		if ok {
			summary.MetadataLockWaits += value
		}
	case "mysql_active_transactions":
		if ok {
			summary.ActiveTransactions += value
		}
	case "mysql_longest_transaction_seconds":
		if ok {
			summary.LongestTransaction = math.Max(summary.LongestTransaction, value)
		}
	case "mysql_replication_lag":
		if ok {
			summary.ReplicationLag = math.Max(summary.ReplicationLag, value)
		}
	case "mysql_buffer_pool_hit_ratio":
		if ok {
			summary.bufferPoolHitSum += value
			summary.bufferPoolHitCount++
		}
	case "mysql_table_scan_ratio":
		if ok {
			summary.tableScanRatioSum += value
			summary.tableScanRatioCount++
		}
	case "mysql_tmp_disk_table_ratio":
		if ok {
			summary.tmpDiskRatioSum += value
			summary.tmpDiskRatioCount++
		}
	}
}

func aggregateOverviewHistory(snapshots []heartbeatdomain.MetricSnapshot, rangeMinutes int, bounds ...time.Time) []clusterOverviewPoint {
	if len(snapshots) == 0 {
		return []clusterOverviewPoint{}
	}
	bucketSize := time.Minute
	if rangeMinutes <= 15 {
		bucketSize = 15 * time.Second
	}
	if rangeMinutes >= 360 {
		bucketSize = 5 * time.Minute
	}
	if rangeMinutes >= 1440 {
		bucketSize = 15 * time.Minute
	}
	buckets := map[int64]*overviewBucket{}
	counters := map[string]overviewCounter{}
	for _, snapshot := range snapshots {
		at := snapshot.CollectedAt
		if len(bounds) >= 1 && at.Before(bounds[0]) {
			continue
		}
		if len(bounds) >= 2 && at.After(bounds[1]) {
			continue
		}
		bucketAt := at.Truncate(bucketSize)
		bucket := buckets[bucketAt.Unix()]
		if bucket == nil {
			bucket = newOverviewBucket(bucketAt)
			buckets[bucketAt.Unix()] = bucket
		}
		for _, metric := range snapshot.Metrics {
			if !metric.Success {
				continue
			}
			key := snapshot.MachineID
			if port := metric.Labels["mysql_port"]; port != "" {
				key += ":" + port
			}
			switch metric.Name {
			case "mysql_qps", "mysql_tps":
				value, ok := overviewNumber(metric.Value)
				if !ok {
					continue
				}
				counterKey := key + ":" + metric.Name
				metricAt := metric.CollectedAt
				if metricAt.IsZero() {
					metricAt = at
				}
				previous, found := counters[counterKey]
				if found && metricAt.After(previous.at) && value >= previous.value {
					rate := (value - previous.value) / metricAt.Sub(previous.at).Seconds()
					if metric.Name == "mysql_qps" {
						addOverviewAverage(bucket.qps, key, rate)
					} else {
						addOverviewAverage(bucket.tps, key, rate)
					}
				}
				if !found || metricAt.After(previous.at) {
					counters[counterKey] = overviewCounter{value: value, at: metricAt}
				}
			case "cpu_usage_percent":
				if value, ok := overviewNumber(metric.Value); ok {
					addOverviewAverage(bucket.cpu, key, value)
				}
			case "io_status":
				busy, read, write := overviewIO(metric.Value)
				addOverviewAverage(bucket.ioBusy, key, busy)
				addOverviewAverage(bucket.ioRead, key, read)
				addOverviewAverage(bucket.ioWrite, key, write)
			case "mysql_data_disk_usage":
				bucket.disk = math.Max(bucket.disk, overviewDisk(metric.Value))
			case "network_throughput":
				rx, tx := overviewNetwork(metric.Value)
				addOverviewAverage(bucket.netRX, key, rx)
				addOverviewAverage(bucket.netTX, key, tx)
			case "mysql_threads_connected":
				addOverviewMetric(bucket.connected, key, metric.Value)
			case "mysql_active_connections":
				addOverviewMetric(bucket.active, key, metric.Value)
			case "mysql_threads_running":
				addOverviewMetric(bucket.running, key, metric.Value)
			case "mysql_lock_wait_sessions", "mysql_row_lock_waits_current":
				addOverviewMetric(bucket.lockWait, key, metric.Value)
			case "mysql_blocked_sessions":
				addOverviewMetric(bucket.blocked, key, metric.Value)
			case "mysql_metadata_lock_waits":
				addOverviewMetric(bucket.metadataLock, key, metric.Value)
			case "mysql_active_transactions":
				addOverviewMetric(bucket.activeTransactions, key, metric.Value)
			case "mysql_longest_transaction_seconds":
				addOverviewMetric(bucket.longestTransaction, key, metric.Value)
			case "mysql_replication_lag":
				addOverviewMetric(bucket.replicationLag, key, metric.Value)
			case "mysql_buffer_pool_hit_ratio":
				addOverviewMetric(bucket.bufferPoolHit, key, metric.Value)
			case "mysql_slow_queries_per_min", "mysql_deadlocks":
				value, ok := overviewNumber(metric.Value)
				if !ok {
					continue
				}
				counterKey := key + ":" + metric.Name
				metricAt := metric.CollectedAt
				if metricAt.IsZero() {
					metricAt = at
				}
				previous, found := counters[counterKey]
				if found && metricAt.After(previous.at) && value >= previous.value {
					perMinute := 60 * (value - previous.value) / metricAt.Sub(previous.at).Seconds()
					if metric.Name == "mysql_slow_queries_per_min" {
						addOverviewAverage(bucket.slowQueriesPerMinute, key, perMinute)
					} else {
						addOverviewAverage(bucket.deadlocksPerMinute, key, perMinute)
					}
				}
				if !found || metricAt.After(previous.at) {
					counters[counterKey] = overviewCounter{value: value, at: metricAt}
				}
			}
		}
	}
	keys := make([]int64, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]clusterOverviewPoint, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		out = append(out, clusterOverviewPoint{
			Timestamp: bucket.at.Format(time.RFC3339), QPS: sumOverviewAverages(bucket.qps), TPS: sumOverviewAverages(bucket.tps),
			ConnectedSessions: sumOverviewAverages(bucket.connected), ActiveSessions: sumOverviewAverages(bucket.active),
			RunningThreads: sumOverviewAverages(bucket.running), LockWaitSessions: sumOverviewAverages(bucket.lockWait),
			BlockedSessions: sumOverviewAverages(bucket.blocked), MetadataLockWaits: sumOverviewAverages(bucket.metadataLock),
			ActiveTransactions: sumOverviewAverages(bucket.activeTransactions), LongestTransaction: maxOverviewAverages(bucket.longestTransaction),
			SlowQueriesPerMinute: sumOverviewAverages(bucket.slowQueriesPerMinute), DeadlocksPerMinute: sumOverviewAverages(bucket.deadlocksPerMinute),
			ReplicationLag: maxOverviewAverages(bucket.replicationLag), BufferPoolHitRatio: averageOverviewAverages(bucket.bufferPoolHit),
			CPUPercent: averageOverviewAverages(bucket.cpu), IOBusyPercent: maxOverviewAverages(bucket.ioBusy),
			IOReadBytes: sumOverviewAverages(bucket.ioRead), IOWriteBytes: sumOverviewAverages(bucket.ioWrite),
			DiskUsedPercent: roundOverview(bucket.disk), NetworkReceiveBytes: sumOverviewAverages(bucket.netRX),
			NetworkTransmitBytes: sumOverviewAverages(bucket.netTX),
		})
	}
	return out
}

func newOverviewBucket(at time.Time) *overviewBucket {
	return &overviewBucket{
		at: at, qps: map[string]*overviewAverage{}, tps: map[string]*overviewAverage{}, cpu: map[string]*overviewAverage{},
		ioBusy: map[string]*overviewAverage{}, ioRead: map[string]*overviewAverage{}, ioWrite: map[string]*overviewAverage{},
		netRX: map[string]*overviewAverage{}, netTX: map[string]*overviewAverage{}, connected: map[string]*overviewAverage{},
		active: map[string]*overviewAverage{}, running: map[string]*overviewAverage{}, lockWait: map[string]*overviewAverage{},
		blocked: map[string]*overviewAverage{}, metadataLock: map[string]*overviewAverage{}, activeTransactions: map[string]*overviewAverage{},
		longestTransaction: map[string]*overviewAverage{}, replicationLag: map[string]*overviewAverage{}, bufferPoolHit: map[string]*overviewAverage{},
		slowQueriesPerMinute: map[string]*overviewAverage{}, deadlocksPerMinute: map[string]*overviewAverage{},
	}
}
func addOverviewMetric(values map[string]*overviewAverage, key string, raw any) {
	if value, ok := overviewNumber(raw); ok {
		addOverviewAverage(values, key, value)
	}
}
func addOverviewAverage(values map[string]*overviewAverage, key string, value float64) {
	item := values[key]
	if item == nil {
		item = &overviewAverage{}
		values[key] = item
	}
	item.sum += value
	item.count++
}
func sumOverviewAverages(values map[string]*overviewAverage) float64 {
	var total float64
	for _, v := range values {
		if v.count > 0 {
			total += v.sum / float64(v.count)
		}
	}
	return roundOverview(total)
}
func averageOverviewAverages(values map[string]*overviewAverage) float64 {
	var total float64
	var count int
	for _, v := range values {
		if v.count > 0 {
			total += v.sum / float64(v.count)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return roundOverview(total / float64(count))
}
func maxOverviewAverages(values map[string]*overviewAverage) float64 {
	var max float64
	for _, v := range values {
		if v.count > 0 {
			max = math.Max(max, v.sum/float64(v.count))
		}
	}
	return roundOverview(max)
}

func overviewNumber(value any) (float64, bool) {
	switch item := value.(type) {
	case float64:
		return item, true
	case float32:
		return float64(item), true
	case int:
		return float64(item), true
	case int64:
		return float64(item), true
	case uint:
		return float64(item), true
	case uint64:
		return float64(item), true
	case string:
		v, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		return v, err == nil
	case map[string]any:
		if skipped, _ := item["skipped"].(bool); skipped {
			return 0, false
		}
		for _, key := range []string{"value", "used_percent"} {
			if v, ok := item[key]; ok {
				return overviewNumber(v)
			}
		}
	}
	return 0, false
}

func overviewDisk(value any) float64 {
	max := float64(0)
	visitOverviewMaps(value, func(item map[string]any) {
		if v, ok := overviewNumber(item["used_percent"]); ok {
			max = math.Max(max, v)
		}
	})
	if max == 0 {
		if v, ok := overviewNumber(value); ok {
			max = v
		}
	}
	return roundOverview(max)
}

func overviewStoragePurpose(metricName string) string {
	switch metricName {
	case "mysql_data_disk_usage":
		return "数据"
	case "mysql_binlog_disk_usage":
		return "Binlog"
	case "mysql_redo_disk_usage":
		return "Redo"
	case "mysql_undo_disk_usage":
		return "Undo"
	case "mysql_tmp_disk_usage":
		return "临时目录"
	default:
		return ""
	}
}

func overviewStoragePriority(item clusterOverviewStorage) int {
	priority := 5
	for _, purpose := range item.Purposes {
		candidate := 5
		switch purpose {
		case "数据":
			candidate = 0
		case "备份/NAS", "NAS":
			candidate = 1
		case "Binlog", "Redo", "Undo", "临时目录":
			candidate = 2
		case "系统":
			candidate = 3
		case "Swap":
			candidate = 4
		}
		if candidate < priority {
			priority = candidate
		}
	}
	return priority
}

func overviewPolicyMatchesNodes(machineID string, port int, nodes []clusterTopologyNode) bool {
	for _, node := range nodes {
		if node.MachineID == machineID && node.Port == port {
			return true
		}
	}
	return false
}

func filterOverviewSnapshotsForInstance(snapshots []heartbeatdomain.MetricSnapshot, node clusterTopologyNode) []heartbeatdomain.MetricSnapshot {
	out := make([]heartbeatdomain.MetricSnapshot, 0, len(snapshots))
	expectedPort := strconv.Itoa(node.Port)
	for _, snapshot := range snapshots {
		if snapshot.MachineID != node.MachineID {
			continue
		}
		filtered := snapshot
		filtered.Metrics = make([]dynamicdomain.MetricResult, 0, len(snapshot.Metrics))
		for _, metric := range snapshot.Metrics {
			if strings.HasPrefix(metric.Name, "mysql_") {
				port := strings.TrimSpace(metric.Labels["mysql_port"])
				if port == "" {
					port = "3306"
				}
				if port != expectedPort {
					continue
				}
			}
			filtered.Metrics = append(filtered.Metrics, metric)
		}
		if len(filtered.Metrics) > 0 {
			out = append(out, filtered)
		}
	}
	return out
}

func overviewStorageUsages(value any) []overviewStorageUsage {
	out := []overviewStorageUsage{}
	visitOverviewMaps(value, func(item map[string]any) {
		_, hasTotal := item["total_bytes"]
		_, hasPercent := item["used_percent"]
		if !hasTotal && !hasPercent {
			return
		}
		usage := overviewStorageUsage{
			path:      overviewString(item["path"]),
			mount:     overviewString(item["mount"]),
			source:    overviewString(item["source"]),
			fsType:    overviewString(item["fs_type"]),
			available: true,
		}
		if enabled, ok := item["enabled"].(bool); ok && !enabled {
			usage.available = false
		}
		if available, ok := item["available"].(bool); ok {
			usage.available = available
		}
		usage.totalBytes, _ = overviewUint64(item["total_bytes"])
		usage.usedBytes, _ = overviewUint64(item["used_bytes"])
		usage.availableBytes, _ = overviewUint64(item["available_bytes"])
		usage.usedPercent, _ = overviewNumber(item["used_percent"])
		if usage.totalBytes == 0 && usage.fsType != "swap" {
			usage.available = false
		}
		out = append(out, usage)
	})
	return out
}

func overviewSingleStorageUsage(value any) (overviewStorageUsage, bool) {
	items := overviewStorageUsages(value)
	if len(items) == 0 {
		return overviewStorageUsage{}, false
	}
	return items[0], true
}

func overviewFilesystemForPath(filesystems []overviewStorageUsage, target string) (overviewStorageUsage, bool) {
	target = strings.TrimSpace(target)
	bestLength := -1
	var best overviewStorageUsage
	for _, item := range filesystems {
		mount := strings.TrimRight(strings.TrimSpace(item.mount), "/")
		if mount == "" {
			mount = "/"
		}
		matches := mount == "/" && strings.HasPrefix(target, "/")
		if mount != "/" {
			matches = target == mount || strings.HasPrefix(target, mount+"/")
		}
		if matches && len(mount) > bestLength {
			best, bestLength = item, len(mount)
		}
	}
	return best, bestLength >= 0
}

func addOverviewStorage(items map[string]*clusterOverviewStorage, node clusterTopologyNode, usage overviewStorageUsage, purpose, path string, port int) {
	key := overviewStorageKey(node.MachineID, usage)
	item := items[key]
	if item == nil {
		item = &clusterOverviewStorage{
			ID:          key,
			MachineID:   node.MachineID,
			MachineName: node.Name,
			IP:          node.IP,
			Mount:       usage.mount,
			Source:      usage.source,
			FSType:      usage.fsType,
			Purposes:    []string{},
			Paths:       []string{},
			Ports:       []int{},
		}
		items[key] = item
	}
	if item.Mount == "" {
		item.Mount = usage.mount
	}
	if item.Source == "" {
		item.Source = usage.source
	}
	if item.FSType == "" {
		item.FSType = usage.fsType
	}
	if usage.totalBytes > 0 || !item.Available {
		item.TotalBytes = usage.totalBytes
		item.UsedBytes = usage.usedBytes
		item.AvailableBytes = usage.availableBytes
		item.UsedPercent = roundOverview(usage.usedPercent)
		item.Available = usage.available
	}
	item.Purposes = appendUniqueString(item.Purposes, purpose)
	item.Paths = appendUniqueString(item.Paths, strings.TrimSpace(path))
	if port > 0 {
		item.Ports = appendUniqueInt(item.Ports, port)
	}
}

func overviewStorageKey(machineID string, usage overviewStorageUsage) string {
	location := strings.TrimSpace(usage.mount)
	if location == "" {
		location = strings.TrimSpace(usage.path)
	}
	if location == "" {
		location = usage.fsType
	}
	return machineID + ":" + location
}

func overviewNetworkFilesystem(fsType string) bool {
	switch strings.ToLower(strings.TrimSpace(fsType)) {
	case "nfs", "nfs4", "cifs", "smb3", "sshfs", "fuse.sshfs", "ceph", "glusterfs":
		return true
	default:
		return false
	}
}

func overviewString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func overviewUint64(value any) (uint64, bool) {
	number, ok := overviewNumber(value)
	if !ok || number < 0 {
		return 0, false
	}
	return uint64(number), true
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueInt(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func overviewIO(value any) (busy, read, write float64) {
	visitOverviewMaps(value, func(item map[string]any) {
		if v, ok := overviewNumber(item["busy_ratio"]); ok {
			busy = math.Max(busy, v)
		}
		if v, ok := overviewNumber(item["read_bytes_sec"]); ok {
			read += v
		}
		if v, ok := overviewNumber(item["write_bytes_sec"]); ok {
			write += v
		}
	})
	return roundOverview(busy * 100), roundOverview(read), roundOverview(write)
}
func overviewNetwork(value any) (rx, tx float64) {
	visitOverviewMaps(value, func(item map[string]any) {
		if v, ok := overviewNumber(item["receive_bytes_sec"]); ok {
			rx += v
		}
		if v, ok := overviewNumber(item["transmit_bytes_sec"]); ok {
			tx += v
		}
	})
	return roundOverview(rx), roundOverview(tx)
}
func visitOverviewMaps(value any, fn func(map[string]any)) {
	switch item := value.(type) {
	case map[string]any:
		fn(item)
		for _, child := range item {
			if _, ok := child.(map[string]any); ok {
				visitOverviewMaps(child, fn)
			}
		}
	case []any:
		for _, child := range item {
			visitOverviewMaps(child, fn)
		}
	case []map[string]any:
		for _, child := range item {
			visitOverviewMaps(child, fn)
		}
	}
}
func roundOverview(value float64) float64 { return math.Round(value*100) / 100 }
