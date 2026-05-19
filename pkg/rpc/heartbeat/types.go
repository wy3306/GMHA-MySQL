// Package heartbeat 定义了心跳服务的 RPC 数据类型和 gRPC 服务接口，用于 Agent 与服务端之间的双向流式心跳通信。
package heartbeat

import dynamicdomain "gmha/internal/domain/dynamic"

// HeartbeatRequest 表示心跳请求，包含 Agent 身份信息、运行时状态、健康检查结果及采集指标。
type HeartbeatRequest struct {
	Identity     AgentIdentity                    `json:"identity"`
	Runtime      AgentRuntime                     `json:"runtime"`
	Health       AgentHealth                      `json:"health"`
	Metrics      *dynamicdomain.MetricBatchResult `json:"metrics,omitempty"`
	MySQLMetrics *dynamicdomain.MetricBatchResult `json:"mysql_metrics,omitempty"`
}

// AgentIdentity 表示 Agent 的身份标识信息，包含 Agent ID、机器 ID、集群 ID、主机名等。
type AgentIdentity struct {
	AgentID   string `json:"agent_id"`
	MachineID string `json:"machine_id"`
	ClusterID string `json:"cluster_id"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
	BootID    string `json:"boot_id"`
}

// AgentRuntime 表示 Agent 的运行时信息，包含发送时间戳、序列号、运行时长和心跳间隔等。
type AgentRuntime struct {
	SentAtUnixMS        int64  `json:"sent_at_unix_ms"`
	Seq                 uint64 `json:"seq"`
	UptimeSec           uint64 `json:"uptime_sec"`
	HeartbeatIntervalMS uint32 `json:"heartbeat_interval_ms"`
	StreamID            string `json:"stream_id"`
}

// AgentHealth 表示 Agent 的整体健康状态，包含总体状态、摘要信息和各项检查结果。
type AgentHealth struct {
	Overall string        `json:"overall"`
	Summary string        `json:"summary"`
	Checks  []HealthCheck `json:"checks"`
}

// HealthCheck 表示单个健康检查项，包含检查名称、状态、详细信息和检查时间。
type HealthCheck struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	Detail          string `json:"detail"`
	CheckedAtUnixMS int64  `json:"checked_at_unix_ms"`
}

// HeartbeatResponse 表示心跳响应，包含服务器时间、状态信息和动态采集配置。
type HeartbeatResponse struct {
	ServerTimeUnixMS    int64                               `json:"server_time_unix_ms"`
	State               string                              `json:"state"`
	Message             string                              `json:"message"`
	DynamicCollect      *dynamicdomain.DynamicCollectConfig `json:"dynamic_collect,omitempty"`
	MySQLDynamicCollect *dynamicdomain.DynamicCollectConfig `json:"mysql_dynamic_collect,omitempty"`
}
