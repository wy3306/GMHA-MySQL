package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	hadomain "gmha/internal/domain/ha"
)

// ClusterTopologyHandler 为 Web 提供与 CLI“查看当前集群架构”一致的只读拓扑视图。
type ClusterTopologyHandler struct {
	machines  *app.MachineService
	mysql     *app.MySQLService
	heartbeat *app.HeartbeatService
	backup    *app.BackupService
	ha        *app.HAService
}

func (h *ClusterTopologyHandler) SetHAService(service *app.HAService) { h.ha = service }

func NewClusterTopologyHandler(machines *app.MachineService, mysql *app.MySQLService, heartbeat *app.HeartbeatService, backup ...*app.BackupService) *ClusterTopologyHandler {
	var backupService *app.BackupService
	if len(backup) > 0 {
		backupService = backup[0]
	}
	return &ClusterTopologyHandler{machines: machines, mysql: mysql, heartbeat: heartbeat, backup: backupService}
}

type clusterTopologyView struct {
	Cluster           string                `json:"cluster"`
	Architecture      string                `json:"architecture"`
	Nodes             []clusterTopologyNode `json:"nodes"`
	Edges             []clusterTopologyEdge `json:"edges"`
	Overview          clusterOverviewView   `json:"overview"`
	TopologySource    string                `json:"topology_source"`
	Consistency       string                `json:"consistency"`
	ConsistencyDetail string                `json:"consistency_detail,omitempty"`
	expectedEdges     int
}

type clusterTopologyNode struct {
	MachineID        string `json:"machine_id"`
	Name             string `json:"name"`
	IP               string `json:"ip"`
	Port             int    `json:"port"`
	Role             string `json:"role"`
	ServerID         int    `json:"server_id"`
	ReadOnly         string `json:"read_only"`
	SuperRO          string `json:"super_read_only"`
	Heartbeat        string `json:"heartbeat"`
	Version          string `json:"version"`
	QPS              string `json:"qps,omitempty"`
	TPS              string `json:"tps,omitempty"`
	Connections      string `json:"connections,omitempty"`
	Uptime           string `json:"uptime,omitempty"`
	LastUpdated      string `json:"last_updated"`
	Error            string `json:"error,omitempty"`
	GroupName        string `json:"group_name,omitempty"`
	GroupState       string `json:"group_state,omitempty"`
	GroupRole        string `json:"group_role,omitempty"`
	GroupMemberID    string `json:"group_member_id,omitempty"`
	GroupMembers     int    `json:"group_members,omitempty"`
	GroupOnline      int    `json:"group_online,omitempty"`
	GroupQuorum      bool   `json:"group_quorum,omitempty"`
	ApplyQueue       int    `json:"apply_queue,omitempty"`
	RemoteApplyQueue int    `json:"remote_apply_queue,omitempty"`
}

type clusterTopologyEdge struct {
	SourceIP        string `json:"source_ip"`
	SourcePort      int    `json:"source_port"`
	TargetIP        string `json:"target_ip"`
	TargetPort      int    `json:"target_port"`
	SourceName      string `json:"source_name,omitempty"`
	TargetName      string `json:"target_name,omitempty"`
	IORunning       string `json:"io_running,omitempty"`
	SQLRunning      string `json:"sql_running,omitempty"`
	Lag             string `json:"lag,omitempty"`
	SQLDelay        int    `json:"sql_delay"`
	LastError       string `json:"last_error,omitempty"`
	ReplicationType string `json:"replication_type,omitempty"`
	Observed        bool   `json:"observed"`
}

// HandleTopology 返回指定集群的 MySQL 节点与实时复制关系；没有实例时返回空拓扑而非错误。
func (h *ClusterTopologyHandler) HandleTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/"), "/")
	cluster := strings.Trim(strings.TrimSuffix(path, "/topology"), "/")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, http.ErrMissingFile)
		return
	}
	rangeMinutes, _ := strconv.Atoi(r.URL.Query().Get("range_minutes"))
	if rangeMinutes < 1 || rangeMinutes > 10080 {
		rangeMinutes = 60
	}
	endAt := time.Now().UTC()
	if value := strings.TrimSpace(r.URL.Query().Get("end_at")); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid end_at: %w", parseErr))
			return
		}
		endAt = parsed.UTC()
		if endAt.After(time.Now().UTC()) {
			endAt = time.Now().UTC()
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("start_at")); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid start_at: %w", parseErr))
			return
		}
		duration := endAt.Sub(parsed.UTC())
		if duration <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("start_at must be earlier than end_at"))
			return
		}
		rangeMinutes = int(math.Ceil(duration.Minutes()))
		if rangeMinutes > 10080 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("time range cannot exceed 7 days"))
			return
		}
	}
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if instance == "all" {
		instance = ""
	}
	view, err := h.buildAt(r.Context(), cluster, rangeMinutes, endAt, instance)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *ClusterTopologyHandler) buildAt(ctx context.Context, cluster string, rangeMinutes int, endAt time.Time, instanceSelectors ...string) (clusterTopologyView, error) {
	instances, err := h.mysql.ListInstanceViews(ctx)
	if err != nil {
		return clusterTopologyView{}, err
	}
	view := clusterTopologyView{Cluster: cluster, Nodes: make([]clusterTopologyNode, 0), Edges: make([]clusterTopologyEdge, 0)}
	for _, instance := range instances {
		if instance.Cluster != cluster {
			continue
		}
		view.Nodes = append(view.Nodes, clusterTopologyNode{
			MachineID: instance.MachineID, Name: instance.MachineName, IP: instance.MachineIP,
			Port: instance.Port, Role: "standalone", ServerID: instance.ServerID,
			Heartbeat: instance.HeartbeatStatus, Version: instance.PackageName, LastUpdated: instance.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	byEndpoint := make(map[string]*clusterTopologyNode, len(view.Nodes))
	for i := range view.Nodes {
		byEndpoint[topologyEndpoint(view.Nodes[i].IP, view.Nodes[i].Port)] = &view.Nodes[i]
	}
	for i := range view.Nodes {
		node := &view.Nodes[i]
		metrics, err := h.machines.GetMySQLDynamicMetrics(ctx, topologyEndpoint(node.IP, node.Port))
		if err != nil {
			node.Error = err.Error()
			continue
		}
		if metrics.HeartbeatState != "" {
			node.Heartbeat = metrics.HeartbeatState
		}
		if metrics.LastHeartbeatAt != "" {
			node.LastUpdated = metrics.LastHeartbeatAt
		}
		for _, metric := range metrics.Metrics {
			switch metric.Name {
			case "mysql_server_id":
				if serverID, ok := topologyInt(metric.Value); ok {
					node.ServerID = serverID
				}
			case "mysql_read_only":
				node.ReadOnly = topologyString(metric.Value)
			case "mysql_super_read_only":
				node.SuperRO = topologyString(metric.Value)
			case "mysql_replication_thread_status":
				if edge, ok := topologyEdgeFromMetric(*node, metric.Value); ok {
					edge.Observed = true
					view.Edges = append(view.Edges, edge)
				}
			case "mysql_group_replication_status":
				applyTopologyGroupReplication(node, metric.Value)
			case "mysql_qps":
				node.QPS = topologyString(metric.Value)
			case "mysql_tps":
				node.TPS = topologyString(metric.Value)
			case "mysql_threads_connected":
				node.Connections = topologyString(metric.Value)
			case "mysql_uptime":
				node.Uptime = topologyString(metric.Value)
			}
		}
	}
	incoming, outgoing := map[string]bool{}, map[string]bool{}
	for i := range view.Edges {
		edge := &view.Edges[i]
		if source := byEndpoint[topologyEndpoint(edge.SourceIP, edge.SourcePort)]; source != nil {
			edge.SourceName = source.Name
			outgoing[topologyEndpoint(source.IP, source.Port)] = true
		}
		if target := byEndpoint[topologyEndpoint(edge.TargetIP, edge.TargetPort)]; target != nil {
			edge.TargetName = target.Name
			incoming[topologyEndpoint(target.IP, target.Port)] = true
		}
	}
	for i := range view.Nodes {
		node := &view.Nodes[i]
		key := topologyEndpoint(node.IP, node.Port)
		if node.GroupRole != "" {
			node.Role = "MGR_" + strings.ToUpper(node.GroupRole)
			view.Architecture = "mgr_router"
			continue
		}
		switch {
		case incoming[key] && outgoing[key]:
			node.Role = "M/S"
		case outgoing[key]:
			node.Role = "M"
		case incoming[key]:
			node.Role = "S"
		case strings.EqualFold(node.ReadOnly, "true") || strings.EqualFold(node.ReadOnly, "on"):
			node.Role = "readonly"
		}
	}
	if view.Architecture == "mgr_router" {
		// SHOW REPLICA STATUS can expose the internal distributed-recovery
		// channel. It is not an asynchronous business topology edge.
		view.Edges = view.Edges[:0]
		var primary *clusterTopologyNode
		for i := range view.Nodes {
			if strings.EqualFold(view.Nodes[i].GroupRole, "PRIMARY") && strings.EqualFold(view.Nodes[i].GroupState, "ONLINE") {
				primary = &view.Nodes[i]
				break
			}
		}
		if primary != nil {
			for i := range view.Nodes {
				target := &view.Nodes[i]
				if target.MachineID == primary.MachineID || target.GroupName != primary.GroupName {
					continue
				}
				view.Edges = append(view.Edges, clusterTopologyEdge{
					SourceIP: primary.IP, SourcePort: primary.Port, TargetIP: target.IP, TargetPort: target.Port,
					SourceName: primary.Name, TargetName: target.Name, IORunning: target.GroupState,
					SQLRunning: target.GroupState, ReplicationType: "group_replication",
					Observed: true,
				})
			}
		}
	}
	view.TopologySource = "runtime"
	if h.ha != nil {
		if intent, found, intentErr := h.ha.GetTopologyIntent(ctx, cluster); intentErr == nil && found {
			applyTopologyIntent(&view, intent)
		}
	}
	if view.Architecture == "" {
		view.Architecture = hadomain.ArchitectureStandalone
	}
	evaluateTopologyConsistency(&view)
	instanceSelector := ""
	if len(instanceSelectors) > 0 {
		instanceSelector = strings.TrimSpace(instanceSelectors[0])
	}
	overviewNodes := view.Nodes
	if instanceSelector != "" {
		overviewNodes = make([]clusterTopologyNode, 0, 1)
		for _, node := range view.Nodes {
			if overviewInstanceSelector(node) == instanceSelector {
				overviewNodes = append(overviewNodes, node)
				break
			}
		}
		if len(overviewNodes) == 0 {
			return clusterTopologyView{}, fmt.Errorf("instance %q not found in cluster %q", instanceSelector, cluster)
		}
	}
	view.Overview = h.buildOverview(ctx, cluster, overviewNodes, rangeMinutes, endAt, instanceSelector)
	return view, nil
}

func applyTopologyGroupReplication(node *clusterTopologyNode, value any) {
	status := topologyMap(value)
	active, _ := status["active"].(bool)
	if !active {
		return
	}
	self := topologyMap(status["self"])
	node.GroupName = topologyFirstString(status, "group_name")
	node.GroupMemberID = topologyFirstString(self, "member_id", "MEMBER_ID")
	node.GroupState = topologyFirstString(self, "member_state", "MEMBER_STATE")
	node.GroupRole = topologyFirstString(self, "member_role", "MEMBER_ROLE")
	node.GroupMembers, _ = topologyInt(status["member_count"])
	node.GroupOnline, _ = topologyInt(status["online_count"])
	node.GroupQuorum, _ = status["quorum"].(bool)
	node.ApplyQueue, _ = topologyInt(self["transactions_in_queue"])
	node.RemoteApplyQueue, _ = topologyInt(self["remote_applier_queue"])
}

func applyTopologyIntent(view *clusterTopologyView, intent hadomain.TopologyIntent) {
	if strings.TrimSpace(intent.Architecture) == "" {
		return
	}
	view.Architecture = intent.Architecture
	for _, node := range intent.Nodes {
		if strings.TrimSpace(node.SourceMachineID) != "" {
			view.expectedEdges++
		}
	}
	if intent.Architecture == hadomain.ArchitectureMGRRouter && len(intent.Nodes) > 0 {
		view.expectedEdges = len(intent.Nodes) - 1
		if !hasObservedMGR(view.Nodes) {
			for index := range view.Nodes {
				if view.Nodes[index].MachineID == intent.PrimaryMachineID {
					view.Nodes[0], view.Nodes[index] = view.Nodes[index], view.Nodes[0]
					break
				}
			}
		}
	}
	byMachine := make(map[string]*clusterTopologyNode, len(view.Nodes))
	for i := range view.Nodes {
		byMachine[view.Nodes[i].MachineID] = &view.Nodes[i]
	}
	for _, desired := range intent.Nodes {
		node := byMachine[desired.MachineID]
		if node == nil {
			continue
		}
		if intent.Architecture == hadomain.ArchitectureMGRRouter {
			if node.GroupRole == "" {
				if desired.MachineID == intent.PrimaryMachineID || strings.EqualFold(desired.Role, "M") {
					node.Role = "MGR_PRIMARY"
				} else {
					node.Role = "MGR_SECONDARY"
				}
			}
			if node.GroupName == "" {
				node.GroupName = intent.MGRGroupName
			}
		} else if intent.Architecture == hadomain.ArchitectureDualMaster || intent.Architecture == hadomain.ArchitectureMultiMaster {
			if node.Role == "standalone" || node.Role == "readonly" {
				if strings.EqualFold(desired.Role, "M") {
					node.Role = "M/S"
				} else {
					node.Role = "S"
				}
			}
		} else if node.Role == "standalone" || node.Role == "readonly" {
			node.Role = strings.ToUpper(desired.Role)
		}
	}
	if view.TopologySource == "runtime" && (len(view.Edges) > 0 || hasObservedMGR(view.Nodes)) {
		view.TopologySource = "runtime+desired_state"
		return
	}
	view.TopologySource = "desired_state"
	view.Edges = view.Edges[:0]
	for _, desired := range intent.Nodes {
		target, source := byMachine[desired.MachineID], byMachine[desired.SourceMachineID]
		if intent.Architecture == hadomain.ArchitectureMGRRouter {
			source = byMachine[intent.PrimaryMachineID]
		}
		if source == nil || target == nil || source.MachineID == target.MachineID {
			continue
		}
		view.Edges = append(view.Edges, clusterTopologyEdge{
			SourceIP: source.IP, SourcePort: source.Port, SourceName: source.Name,
			TargetIP: target.IP, TargetPort: target.Port, TargetName: target.Name,
			IORunning: "UNKNOWN", SQLRunning: "UNKNOWN", SQLDelay: desired.DelaySeconds,
			ReplicationType: map[bool]string{true: "group_replication", false: "async"}[intent.Architecture == hadomain.ArchitectureMGRRouter],
			Observed:        false,
		})
	}
}

func hasObservedMGR(nodes []clusterTopologyNode) bool {
	for _, node := range nodes {
		if node.GroupState != "" {
			return true
		}
	}
	return false
}

func evaluateTopologyConsistency(view *clusterTopologyView) {
	if view.TopologySource == "desired_state" {
		view.Consistency, view.ConsistencyDetail = "unknown", "节点尚未恢复实时复制状态，当前展示持久化的目标架构"
		return
	}
	if view.Architecture == hadomain.ArchitectureMGRRouter {
		primary, online, queue := 0, 0, 0
		groupName, groupMismatch := "", false
		for _, node := range view.Nodes {
			if strings.EqualFold(node.GroupState, "ONLINE") {
				online++
			}
			if strings.EqualFold(node.GroupRole, "PRIMARY") {
				primary++
			}
			queue += node.ApplyQueue + node.RemoteApplyQueue
			if node.GroupName != "" {
				if groupName == "" {
					groupName = node.GroupName
				} else if groupName != node.GroupName {
					groupMismatch = true
				}
			}
		}
		if online == len(view.Nodes) && primary == 1 && queue == 0 && !groupMismatch {
			view.Consistency = "consistent"
		} else {
			view.Consistency, view.ConsistencyDetail = "inconsistent", fmt.Sprintf("MGR ONLINE %d/%d，PRIMARY %d，待应用事务 %d，组标识冲突 %t", online, len(view.Nodes), primary, queue, groupMismatch)
		}
		return
	}
	if len(view.Edges) == 0 {
		view.Consistency = "unknown"
		return
	}
	if view.expectedEdges > 0 && len(view.Edges) != view.expectedEdges {
		view.Consistency, view.ConsistencyDetail = "inconsistent", fmt.Sprintf("实时复制链路 %d/%d，部分链路尚未恢复", len(view.Edges), view.expectedEdges)
		return
	}
	delayed := false
	for _, edge := range view.Edges {
		lag, lagErr := strconv.Atoi(strings.TrimSpace(edge.Lag))
		if !edge.Observed || !strings.EqualFold(edge.IORunning, "Yes") || !strings.EqualFold(edge.SQLRunning, "Yes") || lagErr != nil || (edge.SQLDelay == 0 && lag != 0) || strings.TrimSpace(edge.LastError) != "" {
			view.Consistency, view.ConsistencyDetail = "inconsistent", "复制线程、延迟或最近错误未通过一致性检查"
			return
		}
		delayed = delayed || edge.SQLDelay > 0
	}
	if delayed {
		view.Consistency, view.ConsistencyDetail = "delayed", "复制线程正常，但包含按策略延迟应用的副本"
		return
	}
	view.Consistency = "consistent"
}

func (h *ClusterTopologyHandler) build(ctx context.Context, cluster string, ranges ...int) (clusterTopologyView, error) {
	rangeMinutes := 60
	if len(ranges) > 0 && ranges[0] > 0 {
		rangeMinutes = ranges[0]
	}
	return h.buildAt(ctx, cluster, rangeMinutes, time.Now().UTC())
}

func topologyEdgeFromMetric(node clusterTopologyNode, value any) (clusterTopologyEdge, bool) {
	status := topologyMap(value)
	replicaStatus := topologyMap(status["replica_status"])
	if len(replicaStatus) == 0 {
		return clusterTopologyEdge{}, false
	}
	sourceIP := topologyFirstString(replicaStatus, "Source_Host", "Master_Host")
	if sourceIP == "" {
		return clusterTopologyEdge{}, false
	}
	sourcePort := node.Port
	if port, ok := topologyInt(replicaStatus["Source_Port"]); ok && port > 0 {
		sourcePort = port
	} else if port, ok := topologyInt(replicaStatus["Master_Port"]); ok && port > 0 {
		sourcePort = port
	}
	sqlDelay, _ := topologyInt(replicaStatus["SQL_Delay"])
	return clusterTopologyEdge{SourceIP: sourceIP, SourcePort: sourcePort, TargetIP: node.IP, TargetPort: node.Port, TargetName: node.Name, IORunning: topologyFirstString(status, "io_running"), SQLRunning: topologyFirstString(status, "sql_running"), Lag: topologyFirstString(status, "lag_seconds"), SQLDelay: sqlDelay, LastError: topologyFirstString(status, "last_error")}, true
}

func topologyEndpoint(ip string, port int) string {
	return strings.TrimSpace(ip) + ":" + strconv.Itoa(port)
}

func overviewInstanceSelector(node clusterTopologyNode) string {
	return strings.TrimSpace(node.MachineID) + ":" + strconv.Itoa(node.Port)
}

func topologyMap(value any) map[string]any {
	if direct, ok := value.(map[string]any); ok {
		return direct
	}
	content, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if json.Unmarshal(content, &result) != nil {
		return map[string]any{}
	}
	return result
}

func topologyString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(fmt.Sprint(value), "\""))
}

func topologyInt(value any) (int, bool) {
	switch item := value.(type) {
	case int:
		return item, true
	case int64:
		return int(item), true
	case float64:
		return int(item), true
	default:
		parsed, err := strconv.Atoi(topologyString(item))
		return parsed, err == nil
	}
}

func topologyFirstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := topologyString(values[key]); value != "" {
			return value
		}
	}
	return ""
}
