// Package ha 定义了高可用 (High Availability) 领域的实体和值对象。
// 包含集群信息、VIP 配置、故障转移策略、隔离策略、网络策略、故障转移事件和候选评分等核心概念。
package ha

import "time"

const (
	DefaultFailoverMode    = "safe"
	DefaultSwitchStrategy  = "safe-wait-replay-auto"
	VipRouteModeL2ARP      = "L2_ARP"
	VipRouteModeManual     = "MANUAL"
	VipRouteModeBGP        = "BGP"
	VipRouteModeCloudAPI   = "CLOUD_API"
	VipRouteModeKeepalived = "KEEPALIVED"

	VipStatusUnknown  = "UNKNOWN"
	VipStatusUnbound  = "UNBOUND"
	VipStatusBound    = "BOUND"
	VipStatusConflict = "CONFLICT"
	VipStatusMismatch = "MISMATCH"
	VipStatusFailed   = "FAILED"

	FailoverStatusInit                 = "INIT"
	FailoverStatusCheckOldMaster       = "CHECK_OLD_MASTER"
	FailoverStatusAcquireLock          = "ACQUIRE_FAILOVER_LOCK"
	FailoverStatusFenceOldMaster       = "FENCE_OLD_MASTER"
	FailoverStatusCheckVIPConflict     = "CHECK_VIP_CONFLICT"
	FailoverStatusSelectFirstCandidate = "SELECT_FIRST_CANDIDATE"
	FailoverStatusWaitRelayReplay      = "WAIT_RELAY_REPLAY"
	FailoverStatusReselectCandidate    = "RESELECT_CANDIDATE"
	FailoverStatusBinlogRescue         = "BINLOG_RESCUE"
	FailoverStatusPromoteNewMaster     = "PROMOTE_NEW_MASTER"
	FailoverStatusMoveVIP              = "MOVE_VIP"
	FailoverStatusVerifyNewMaster      = "VERIFY_NEW_MASTER"
	FailoverStatusRepointReplicas      = "REPOINT_REPLICAS"
	FailoverStatusDone                 = "DONE"
	FailoverStatusFailed               = "FAILED"
)

// ClusterInfo 存储集群的基本信息和配置，包括集群类型、HA 开关、Binlog 救援等。
type ClusterInfo struct {
	ClusterID             string    `json:"cluster_id"`
	ClusterType           string    `json:"cluster_type"`
	ClusterStatus         string    `json:"cluster_status"`
	DefaultFailoverMode   string    `json:"default_failover_mode"`
	DefaultSwitchStrategy string    `json:"default_switch_strategy"`
	EnableVIP             bool      `json:"enable_vip"`
	EnableBinlogRescue    bool      `json:"enable_binlog_rescue"`
	EnableAutoFailover    bool      `json:"enable_auto_failover"`
	Description           string    `json:"description"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// ClusterVIPConfig 存储集群的 VIP 配置，包括 VIP 地址、路由模式、管理方式等。
type ClusterVIPConfig struct {
	ID                   int64     `json:"id"`
	ClusterID            string    `json:"cluster_id"`
	VIPName              string    `json:"vip_name"`
	VIPAddress           string    `json:"vip_address"`
	VIPPrefix            int       `json:"vip_prefix"`
	VIPRouteMode         string    `json:"vip_route_mode"`
	VIPManageMode        string    `json:"vip_manage_mode"`
	DefaultInterface     string    `json:"default_interface"`
	AllowManualAdopt     bool      `json:"allow_manual_adopt"`
	PreemptEnabled       bool      `json:"preempt_enabled"`
	ArpingEnabled        bool      `json:"arping_enabled"`
	ArpingCount          int       `json:"arping_count"`
	CheckAfterBind       bool      `json:"check_after_bind"`
	ExternalCheckEnabled bool      `json:"external_check_enabled"`
	BGPEnabled           bool      `json:"bgp_enabled"`
	BGPLocalAS           int       `json:"bgp_local_as,omitempty"`
	BGPPeerAS            int       `json:"bgp_peer_as,omitempty"`
	BGPPeerAddress       string    `json:"bgp_peer_address,omitempty"`
	BGPRouterID          string    `json:"bgp_router_id,omitempty"`
	BGPCommunity         string    `json:"bgp_community,omitempty"`
	Enabled              bool      `json:"enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// VIPBindingState 记录 VIP 的绑定状态，包括期望持有者、实际持有者和检测结果。
type VIPBindingState struct {
	ID                       int64     `json:"id"`
	ClusterID                string    `json:"cluster_id"`
	VIPConfigID              int64     `json:"vip_config_id"`
	VIPAddress               string    `json:"vip_address"`
	ExpectedHolderInstanceID string    `json:"expected_holder_instance_id"`
	ExpectedHolderMachineID  string    `json:"expected_holder_machine_id"`
	CurrentHolderInstanceID  string    `json:"current_holder_instance_id"`
	CurrentHolderMachineID   string    `json:"current_holder_machine_id"`
	CurrentInterface         string    `json:"current_interface"`
	VIPStatus                string    `json:"vip_status"`
	DetectedHolders          string    `json:"detected_holders"`
	LastCheckResult          string    `json:"last_check_result"`
	LastError                string    `json:"last_error"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// FailoverPolicy 定义故障转移策略，包括切换模式、等待回放、数据丢失容忍等。
type FailoverPolicy struct {
	ClusterID                     string `json:"cluster_id"`
	FailoverMode                  string `json:"failover_mode"`
	SwitchStrategy                string `json:"switch_strategy"`
	AutoFailoverEnabled           bool   `json:"auto_failover_enabled"`
	WaitRelayReplayEnabled        bool   `json:"wait_relay_replay_enabled"`
	WaitRelayReplayTimeoutSeconds int    `json:"wait_relay_replay_timeout_seconds"`
	RequireDelayZeroBeforePromote bool   `json:"require_delay_zero_before_promote"`
	MaxAllowedDelaySeconds        int    `json:"max_allowed_delay_seconds"`
	ReselectCandidateAfterReplay  bool   `json:"reselect_candidate_after_replay"`
	RequireOldMasterFence         bool   `json:"require_old_master_fence"`
	BinlogRescueEnabled           bool   `json:"binlog_rescue_enabled"`
	BinlogRescueTimeoutSeconds    int    `json:"binlog_rescue_timeout_seconds"`
	AllowDataLoss                 bool   `json:"allow_data_loss"`
	StopOnBinlogRescueFailure     bool   `json:"stop_on_binlog_rescue_failure"`
}

// FencingPolicy 定义旧主隔离策略，用于故障转移时确保旧主不再写入数据。
type FencingPolicy struct {
	ClusterID                             string `json:"cluster_id"`
	RequireOldMasterFence                 bool   `json:"require_old_master_fence"`
	AgentFenceEnabled                     bool   `json:"agent_fence_enabled"`
	SSHFenceEnabled                       bool   `json:"ssh_fence_enabled"`
	SetReadOnlyEnabled                    bool   `json:"set_readonly_enabled"`
	StopMySQLEnabled                      bool   `json:"stop_mysql_enabled"`
	DelVIPEnabled                         bool   `json:"del_vip_enabled"`
	AllowFailoverWhenOldMasterUnreachable bool   `json:"allow_failover_when_old_master_unreachable"`
	CheckVIPConflictBeforeMove            bool   `json:"check_vip_conflict_before_move"`
	CheckVIPConflictAfterMove             bool   `json:"check_vip_conflict_after_move"`
}

// NetworkPolicy 定义网络策略，包括网络拓扑、VIP 路由模式、子网要求等。
type NetworkPolicy struct {
	ClusterID                 string `json:"cluster_id"`
	NetworkTopology           string `json:"network_topology"`
	VIPRouteMode              string `json:"vip_route_mode"`
	RequireSameSubnetForL2VIP bool   `json:"require_same_subnet_for_l2_vip"`
	AllowMultiNIC             bool   `json:"allow_multi_nic"`
	AutoDetectVIPInterface    bool   `json:"auto_detect_vip_interface"`
	BusinessNetworkCIDR       string `json:"business_network_cidr"`
	ReplicationNetworkCIDR    string `json:"replication_network_cidr"`
	ManagementNetworkCIDR     string `json:"management_network_cidr"`
}

type MachineNetworkInterface struct {
	MachineID       string `json:"machine_id"`
	InterfaceName   string `json:"interface_name"`
	MACAddress      string `json:"mac_address"`
	IPv4Addresses   string `json:"ipv4_addresses"`
	IPv6Addresses   string `json:"ipv6_addresses"`
	NetworkRole     string `json:"network_role"`
	IsUp            bool   `json:"is_up"`
	MTU             int    `json:"mtu"`
	SpeedMbps       int    `json:"speed_mbps"`
	Gateway         string `json:"gateway"`
	VLANID          string `json:"vlan_id"`
	SubnetCIDR      string `json:"subnet_cidr"`
	CanBindVIP      bool   `json:"can_bind_vip"`
	VIPBindPriority int    `json:"vip_bind_priority"`
}

// FailoverEvent 记录一次故障转移事件的完整过程，包括新旧主信息、状态、风险等级等。
type FailoverEvent struct {
	FailoverID               string    `json:"failover_id"`
	ClusterID                string    `json:"cluster_id"`
	OldMasterInstanceID      string    `json:"old_master_instance_id"`
	OldMasterMachineID       string    `json:"old_master_machine_id"`
	OldMasterIP              string    `json:"old_master_ip"`
	FirstCandidateInstanceID string    `json:"first_candidate_instance_id"`
	FirstCandidateMachineID  string    `json:"first_candidate_machine_id"`
	FinalNewMasterInstanceID string    `json:"final_new_master_instance_id"`
	FinalNewMasterMachineID  string    `json:"final_new_master_machine_id"`
	FinalNewMasterIP         string    `json:"final_new_master_ip"`
	Mode                     string    `json:"mode"`
	SwitchStrategy           string    `json:"switch_strategy"`
	Status                   string    `json:"status"`
	Reason                   string    `json:"reason"`
	RiskLevel                string    `json:"risk_level"`
	RiskSummary              string    `json:"risk_summary"`
	OldMasterFenced          bool      `json:"old_master_fenced"`
	RelayReplayWaited        bool      `json:"relay_replay_waited"`
	RelayReplaySuccess       bool      `json:"relay_replay_success"`
	BinlogRescueAttempted    bool      `json:"binlog_rescue_attempted"`
	BinlogRescueSuccess      bool      `json:"binlog_rescue_success"`
	VIPMoved                 bool      `json:"vip_moved"`
	StartedAt                time.Time `json:"started_at"`
	UpdatedAt                time.Time `json:"updated_at"`
	FinishedAt               time.Time `json:"finished_at,omitempty"`
}

// CandidateScore 存储故障转移候选节点的评分信息，用于选择最优的新主节点。
type CandidateScore struct {
	ClusterID          string   `json:"cluster_id"`
	InstanceID         string   `json:"instance_id"`
	MachineID          string   `json:"machine_id"`
	Hostname           string   `json:"hostname"`
	IP                 string   `json:"ip"`
	Port               int      `json:"port"`
	Eligible           bool     `json:"eligible"`
	RejectReasons      []string `json:"reject_reasons"`
	DataFreshnessScore int      `json:"data_freshness_score"`
	RelayReceivedScore int      `json:"relay_received_score"`
	RelayExecutedScore int      `json:"relay_executed_score"`
	DelaySeconds       int      `json:"delay_seconds"`
	ElectionPriority   int      `json:"election_priority"`
	HealthScore        int      `json:"health_score"`
	RiskPenalty        int      `json:"risk_penalty"`
	FinalScore         int      `json:"final_score"`
	GTIDMode           bool     `json:"gtid_mode"`
	ExecutedGTIDSet    string   `json:"executed_gtid_set"`
	RetrievedGTIDSet   string   `json:"retrieved_gtid_set"`
	MissingGTIDSet     string   `json:"missing_gtid_set"`
	RelayMasterLogFile string   `json:"relay_master_log_file"`
	ExecMasterLogPos   int64    `json:"exec_master_log_pos"`
	ReadMasterLogPos   int64    `json:"read_master_log_pos"`
	NeedRelayReplay    bool     `json:"need_relay_replay"`
	CanBindVIP         bool     `json:"can_bind_vip"`
	VIPInterface       string   `json:"vip_interface"`
}
