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
)

// ClusterTopologyHandler 为 Web 提供与 CLI“查看当前集群架构”一致的只读拓扑视图。
type ClusterTopologyHandler struct {
	machines  *app.MachineService
	mysql     *app.MySQLService
	heartbeat *app.HeartbeatService
	backup    *app.BackupService
}

func NewClusterTopologyHandler(machines *app.MachineService, mysql *app.MySQLService, heartbeat *app.HeartbeatService, backup ...*app.BackupService) *ClusterTopologyHandler {
	var backupService *app.BackupService
	if len(backup) > 0 {
		backupService = backup[0]
	}
	return &ClusterTopologyHandler{machines: machines, mysql: mysql, heartbeat: heartbeat, backup: backupService}
}

type clusterTopologyView struct {
	Cluster  string                `json:"cluster"`
	Nodes    []clusterTopologyNode `json:"nodes"`
	Edges    []clusterTopologyEdge `json:"edges"`
	Overview clusterOverviewView   `json:"overview"`
}

type clusterTopologyNode struct {
	MachineID   string `json:"machine_id"`
	Name        string `json:"name"`
	IP          string `json:"ip"`
	Port        int    `json:"port"`
	Role        string `json:"role"`
	ServerID    int    `json:"server_id"`
	ReadOnly    string `json:"read_only"`
	SuperRO     string `json:"super_read_only"`
	Heartbeat   string `json:"heartbeat"`
	Version     string `json:"version"`
	QPS         string `json:"qps,omitempty"`
	TPS         string `json:"tps,omitempty"`
	Connections string `json:"connections,omitempty"`
	Uptime      string `json:"uptime,omitempty"`
	LastUpdated string `json:"last_updated"`
	Error       string `json:"error,omitempty"`
}

type clusterTopologyEdge struct {
	SourceIP   string `json:"source_ip"`
	SourcePort int    `json:"source_port"`
	TargetIP   string `json:"target_ip"`
	TargetPort int    `json:"target_port"`
	SourceName string `json:"source_name,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	IORunning  string `json:"io_running,omitempty"`
	SQLRunning string `json:"sql_running,omitempty"`
	Lag        string `json:"lag,omitempty"`
	SQLDelay   int    `json:"sql_delay"`
	LastError  string `json:"last_error,omitempty"`
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
					view.Edges = append(view.Edges, edge)
				}
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
