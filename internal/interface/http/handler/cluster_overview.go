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
	Summary      clusterOverviewSummary   `json:"summary"`
	Series       []clusterOverviewPoint   `json:"series"`
	Machines     []clusterOverviewMachine `json:"machines"`
}

type clusterOverviewSummary struct {
	QPS                  float64 `json:"qps"`
	TPS                  float64 `json:"tps"`
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
}

type clusterOverviewPoint struct {
	Timestamp            string  `json:"timestamp"`
	QPS                  float64 `json:"qps"`
	TPS                  float64 `json:"tps"`
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

type overviewBucket struct {
	at                                                   time.Time
	qps, tps, cpu, ioBusy, ioRead, ioWrite, netRX, netTX map[string]*overviewAverage
	disk                                                 float64
}
type overviewAverage struct {
	sum   float64
	count int
}
type overviewCounter struct {
	value float64
	at    time.Time
}

func (h *ClusterTopologyHandler) buildOverview(ctx context.Context, cluster string, nodes []clusterTopologyNode, rangeMinutes int, now time.Time) clusterOverviewView {
	view := clusterOverviewView{GeneratedAt: now.Local().Format("2006-01-02 15:04:05"), RangeMinutes: rangeMinutes, DataSource: "waiting", Series: []clusterOverviewPoint{}, Machines: []clusterOverviewMachine{}}
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
	for _, node := range machineNodes {
		machine := clusterOverviewMachine{MachineID: node.MachineID, Name: node.Name, IP: node.IP, Status: node.Heartbeat}
		metrics, err := h.machines.GetMachineDynamicMetrics(ctx, node.IP)
		if err == nil {
			if metrics.HeartbeatState != "" {
				machine.Status = metrics.HeartbeatState
			}
			for _, metric := range metrics.Metrics {
				applyCurrentHostMetric(&view.Summary, &machine, metric)
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
	for _, node := range nodes {
		metrics, err := h.machines.GetMySQLDynamicMetrics(ctx, topologyEndpoint(node.IP, node.Port))
		if err != nil {
			continue
		}
		for _, metric := range metrics.Metrics {
			applyCurrentMySQLMetric(&view.Summary, metric)
		}
	}
	if total := view.Summary.DataBytes + view.Summary.IndexBytes; total > 0 {
		view.Summary.FragmentPercent = roundOverview(100 * view.Summary.FragmentBytes / total)
	}

	if h.heartbeat != nil {
		snapshots, err := h.heartbeat.MetricHistory(ctx, cluster, now.Add(-time.Duration(rangeMinutes)*time.Minute), 20000)
		if err == nil {
			view.Series = aggregateOverviewHistory(snapshots, rangeMinutes)
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
	case "filesystem_usage":
		machine.DiskUsedPercent = overviewDisk(metric.Value)
		summary.DiskUsedPercent = math.Max(summary.DiskUsedPercent, machine.DiskUsedPercent)
	case "network_throughput":
		rx, tx := overviewNetwork(metric.Value)
		machine.NetworkBytes = rx + tx
		summary.NetworkReceiveBytes += rx
		summary.NetworkTransmitBytes += tx
	}
}

func applyCurrentMySQLMetric(summary *clusterOverviewSummary, metric dynamicdomain.MetricResult) {
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
	case "mysql_data_disk_usage", "mysql_binlog_disk_usage", "mysql_redo_disk_usage", "mysql_tmp_disk_usage", "mysql_undo_disk_usage":
		summary.DiskUsedPercent = math.Max(summary.DiskUsedPercent, overviewDisk(metric.Value))
	}
}

func aggregateOverviewHistory(snapshots []heartbeatdomain.MetricSnapshot, rangeMinutes int) []clusterOverviewPoint {
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
			case "filesystem_usage":
				bucket.disk = math.Max(bucket.disk, overviewDisk(metric.Value))
			case "network_throughput":
				rx, tx := overviewNetwork(metric.Value)
				addOverviewAverage(bucket.netRX, key, rx)
				addOverviewAverage(bucket.netTX, key, tx)
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
		out = append(out, clusterOverviewPoint{Timestamp: bucket.at.Format(time.RFC3339), QPS: sumOverviewAverages(bucket.qps), TPS: sumOverviewAverages(bucket.tps), CPUPercent: averageOverviewAverages(bucket.cpu), IOBusyPercent: maxOverviewAverages(bucket.ioBusy), IOReadBytes: sumOverviewAverages(bucket.ioRead), IOWriteBytes: sumOverviewAverages(bucket.ioWrite), DiskUsedPercent: roundOverview(bucket.disk), NetworkReceiveBytes: sumOverviewAverages(bucket.netRX), NetworkTransmitBytes: sumOverviewAverages(bucket.netTX)})
	}
	return out
}

func newOverviewBucket(at time.Time) *overviewBucket {
	return &overviewBucket{at: at, qps: map[string]*overviewAverage{}, tps: map[string]*overviewAverage{}, cpu: map[string]*overviewAverage{}, ioBusy: map[string]*overviewAverage{}, ioRead: map[string]*overviewAverage{}, ioWrite: map[string]*overviewAverage{}, netRX: map[string]*overviewAverage{}, netTX: map[string]*overviewAverage{}}
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
