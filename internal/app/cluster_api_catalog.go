package app

import (
	"encoding/json"
	"net/http"
)

// ClusterAPIEndpoint is the machine-readable contract for every operation
// exposed by the cluster-management workspace. InvocationMode is never an
// availability flag: it tells an AI client which approval/input channel must
// be used instead of inventing an endpoint or putting a secret in model text.
type ClusterAPIEndpoint struct {
	ID                  string   `json:"id"`
	Group               string   `json:"group"`
	Method              string   `json:"method"`
	Path                string   `json:"path"`
	Description         string   `json:"description"`
	InvocationMode      string   `json:"invocation_mode"`
	AIActionID          string   `json:"ai_action_id,omitempty"`
	SensitiveParameters []string `json:"sensitive_parameters,omitempty"`
}

var clusterAPICatalog = []ClusterAPIEndpoint{
	{ID: "clusters.list", Group: "cluster", Method: http.MethodGet, Path: "/api/v1/clusters", Description: "查询集群列表", InvocationMode: "read"},
	{ID: "clusters.create", Group: "cluster", Method: http.MethodPost, Path: "/api/v1/clusters", Description: "创建集群登记", InvocationMode: "ai_action", AIActionID: "create_cluster"},
	{ID: "clusters.get", Group: "cluster", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}", Description: "查询集群详情", InvocationMode: "read"},
	{ID: "clusters.update", Group: "cluster", Method: http.MethodPut, Path: "/api/v1/clusters/{cluster_name}", Description: "更新集群名称或说明", InvocationMode: "ai_action", AIActionID: "update_cluster"},
	{ID: "clusters.delete", Group: "cluster", Method: http.MethodDelete, Path: "/api/v1/clusters/{cluster_name}", Description: "删除空集群登记", InvocationMode: "ai_action", AIActionID: "delete_cluster"},
	{ID: "clusters.cleanup", Group: "cluster", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/cleanup", Description: "一键清理资源并删除集群", InvocationMode: "ai_action", AIActionID: "cleanup_cluster"},
	{ID: "clusters.machines", Group: "member", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/machines", Description: "查询集群成员机器", InvocationMode: "read"},
	{ID: "clusters.members.add", Group: "member", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/members", Description: "批量添加集群成员", InvocationMode: "ai_action", AIActionID: "register_cluster_members"},
	{ID: "clusters.members.assign", Group: "member", Method: http.MethodPost, Path: "/api/v1/machines/{machine_id}/assign-cluster", Description: "设置单机集群归属", InvocationMode: "approval_api"},
	{ID: "clusters.members.remove", Group: "member", Method: http.MethodDelete, Path: "/api/v1/machines/{machine_id}/assign-cluster", Description: "移出集群成员", InvocationMode: "ai_action", AIActionID: "remove_cluster_members"},
	{ID: "clusters.topology", Group: "observability", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/topology", Description: "查询复制拓扑与概览指标", InvocationMode: "read"},
	{ID: "clusters.performance.catalog", Group: "observability", Method: http.MethodGet, Path: "/api/v1/performance/catalog", Description: "查询指标目录", InvocationMode: "read"},
	{ID: "clusters.performance.metrics", Group: "observability", Method: http.MethodGet, Path: "/api/v1/performance/metrics", Description: "查询集群指标时序", InvocationMode: "read"},
	{ID: "clusters.machine.static_info", Group: "observability", Method: http.MethodPost, Path: "/api/v1/machines/{machine_id}/static-info", Description: "采集并读取机器静态信息", InvocationMode: "approval_api"},

	{ID: "clusters.mysql.install", Group: "mysql", Method: http.MethodPost, Path: "/api/v1/tasks/cluster-mysql-install", Description: "批量安装集群 MySQL", InvocationMode: "secure_input_api", SensitiveParameters: []string{"root_password", "accounts[].password"}},
	{ID: "clusters.mysql.uninstall", Group: "mysql", Method: http.MethodPost, Path: "/api/v1/tasks/cluster-mysql-uninstall", Description: "批量卸载集群 MySQL", InvocationMode: "ai_action", AIActionID: "uninstall_cluster_mysql"},
	{ID: "clusters.mysql.upgrade.plan", Group: "mysql", Method: http.MethodPost, Path: "/api/v1/tasks/mysql-cluster-upgrade/plan", Description: "生成集群滚动升级计划", InvocationMode: "approval_api"},
	{ID: "clusters.mysql.upgrade.start", Group: "mysql", Method: http.MethodPost, Path: "/api/v1/tasks/mysql-cluster-upgrade/start", Description: "启动集群滚动升级", InvocationMode: "ai_action", AIActionID: "rolling_upgrade_cluster_mysql"},
	{ID: "clusters.mysql.upgrade.get", Group: "mysql", Method: http.MethodGet, Path: "/api/v1/tasks/mysql-cluster-upgrade", Description: "查询集群滚动升级状态", InvocationMode: "read"},

	{ID: "clusters.vip.config.list", Group: "vip", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/vip/config", Description: "查询 VIP 配置", InvocationMode: "read"},
	{ID: "clusters.vip.config.apply", Group: "vip", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/vip/config", Description: "保存、绑定并复检 VIP", InvocationMode: "ai_action", AIActionID: "configure_cluster_vip"},
	{ID: "clusters.vip.config.remove", Group: "vip", Method: http.MethodDelete, Path: "/api/v1/clusters/{cluster_name}/vip/config", Description: "撤销并删除 VIP", InvocationMode: "ai_action", AIActionID: "remove_cluster_vip"},
	{ID: "clusters.vip.status", Group: "vip", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/vip/status", Description: "查询 VIP 实机状态", InvocationMode: "read"},
	{ID: "clusters.vip.scan", Group: "vip", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/vip/scan", Description: "扫描 VIP 实机持有者", InvocationMode: "approval_api"},
	{ID: "clusters.vip.adopt", Group: "vip", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/vip/adopt", Description: "采纳现有 VIP 持有者", InvocationMode: "approval_api"},
	{ID: "clusters.vip.validate", Group: "vip", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/vip/validate", Description: "复检全部 VIP", InvocationMode: "ai_action", AIActionID: "scan_cluster_vip"},

	{ID: "clusters.architecture.plan", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/architecture/plan", Description: "生成架构调整计划", InvocationMode: "approval_api"},
	{ID: "clusters.architecture.start", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/architecture/start", Description: "启动架构调整", InvocationMode: "ai_action", AIActionID: "configure_cluster_architecture"},
	{ID: "clusters.architecture.get", Group: "ha", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/architecture/{run_id}", Description: "查询架构调整状态", InvocationMode: "read"},
	{ID: "clusters.architecture.force", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/architecture/{run_id}/force", Description: "人工确认强制继续架构切换", InvocationMode: "approval_api"},
	{ID: "clusters.failover.plan", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/failover/plan", Description: "生成故障切换计划", InvocationMode: "approval_api"},
	{ID: "clusters.failover.start", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/failover/start", Description: "启动受保护的故障切换", InvocationMode: "approval_api"},
	{ID: "clusters.failover.get", Group: "ha", Method: http.MethodGet, Path: "/api/v1/clusters/{cluster_name}/failover/{failover_id}", Description: "查询故障切换状态", InvocationMode: "read"},
	{ID: "clusters.bootstrap", Group: "ha", Method: http.MethodPost, Path: "/api/v1/clusters/{cluster_name}/bootstrap", Description: "安装并初始化集群架构", InvocationMode: "secure_input_api", SensitiveParameters: []string{"root_password", "replication_password", "accounts[].password"}},

	{ID: "clusters.backup.targets", Group: "backup", Method: http.MethodGet, Path: "/api/v1/backup/targets", Description: "查询备份目标", InvocationMode: "read"},
	{ID: "clusters.backup.policies.list", Group: "backup", Method: http.MethodGet, Path: "/api/v1/backup/policies", Description: "查询备份策略", InvocationMode: "read"},
	{ID: "clusters.backup.policies.create", Group: "backup", Method: http.MethodPost, Path: "/api/v1/backup/policies", Description: "创建备份策略", InvocationMode: "secure_input_api", SensitiveParameters: []string{"mysql_password"}},
	{ID: "clusters.backup.policies.get", Group: "backup", Method: http.MethodGet, Path: "/api/v1/backup/policies/{policy_id}", Description: "查询备份策略详情", InvocationMode: "read"},
	{ID: "clusters.backup.policies.update", Group: "backup", Method: http.MethodPut, Path: "/api/v1/backup/policies/{policy_id}", Description: "更新备份策略", InvocationMode: "secure_input_api", SensitiveParameters: []string{"mysql_password"}},
	{ID: "clusters.backup.policies.delete", Group: "backup", Method: http.MethodDelete, Path: "/api/v1/backup/policies/{policy_id}", Description: "删除备份策略", InvocationMode: "approval_api"},
	{ID: "clusters.backup.policies.run", Group: "backup", Method: http.MethodPost, Path: "/api/v1/backup/policies/{policy_id}/run", Description: "立即运行单个备份策略", InvocationMode: "approval_api"},
	{ID: "clusters.backup.runs.list", Group: "backup", Method: http.MethodGet, Path: "/api/v1/backup/runs", Description: "查询备份运行记录", InvocationMode: "read"},
	{ID: "clusters.backup.runs.get", Group: "backup", Method: http.MethodGet, Path: "/api/v1/backup/runs/{run_id}", Description: "查询备份运行详情", InvocationMode: "read"},
	{ID: "clusters.backup.runs.batch", Group: "backup", Method: http.MethodPost, Path: "/api/v1/backup/cluster-runs", Description: "立即运行集群已启用策略", InvocationMode: "ai_action", AIActionID: "run_cluster_backup"},
	{ID: "clusters.backup.restore", Group: "backup", Method: http.MethodPost, Path: "/api/v1/backup/runs/{run_id}/restore", Description: "创建恢复或闪回任务", InvocationMode: "secure_input_api", SensitiveParameters: []string{"mysql_password"}},

	{ID: "clusters.automation.start", Group: "automation", Method: http.MethodPost, Path: "/api/v1/tasks/cluster-automation", Description: "启动集群采集、巡检或批量运维", InvocationMode: "secure_input_api", SensitiveParameters: []string{"target_password"}},
	{ID: "clusters.automation.results", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/cluster-automation/results", Description: "查询自动化结构化结果", InvocationMode: "read"},
	{ID: "clusters.automation.report", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/cluster-automation/report", Description: "导出自动化报告", InvocationMode: "read"},
	{ID: "clusters.automation.artifact", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/cluster-automation/artifacts/{task_id}/{file_name}", Description: "下载自动化产物", InvocationMode: "read"},
	{ID: "clusters.inspection.results", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/database-inspection/results", Description: "查询数据库巡检结果", InvocationMode: "read"},
	{ID: "clusters.inspection.report", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/database-inspection/report", Description: "导出数据库巡检报告", InvocationMode: "read"},
	{ID: "clusters.inspection.data", Group: "automation", Method: http.MethodGet, Path: "/api/v1/tasks/database-inspection/data", Description: "查询数据库巡检数据", InvocationMode: "read"},
}

// ClusterAPICatalog returns a deep copy so callers cannot mutate the
// authoritative capability contract.
func ClusterAPICatalog() []ClusterAPIEndpoint {
	raw, _ := json.Marshal(clusterAPICatalog)
	var out []ClusterAPIEndpoint
	_ = json.Unmarshal(raw, &out)
	return out
}
