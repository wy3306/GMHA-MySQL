package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	collectdomain "gmha/internal/collect"
	clusterdomain "gmha/internal/domain/cluster"
	credentialdomain "gmha/internal/domain/credential"
	dynamicdomain "gmha/internal/domain/dynamic"
	machinedomain "gmha/internal/domain/machine"
	mysqlapp "gmha/internal/mysql"
	machineusecase "gmha/internal/usecase/machine"
	taskusecase "gmha/internal/usecase/task"
)

// MachineService 是机器管理服务，负责机器纳管、集群管理、SSH 凭据管理、
// 机器信息采集、静态信息采集和动态指标查询等。
type MachineService struct {
	onboard      *machineusecase.OnboardUsecase
	machineRepo  machinedomain.Repository
	clusterRepo  clusterdomain.Repository
	credRepo     credentialdomain.Repository
	infoRepo     MachineInfoRepository
	staticRepo   StaticInfoRepository
	recoveryRepo machineDataCleaner
	sshClient    machineusecase.SSHClient
	agentSvc     *AgentService
	taskSvc      *TaskService
}

// MachineInfoRepository 定义了机器采集信息的持久化接口。
type MachineInfoRepository interface {
	Save(ctx context.Context, item collectdomain.MachineInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.MachineInfo, bool, error)
}

// StaticInfoRepository 定义了静态信息的持久化接口。
type StaticInfoRepository interface {
	Save(ctx context.Context, item collectdomain.StaticInfo) error
	Get(ctx context.Context, machineID string) (collectdomain.StaticInfo, bool, error)
}

type machineDataCleaner interface {
	DeleteByMachineID(ctx context.Context, machineID string) error
}

type mysqlInstanceMachineCleaner interface {
	List(ctx context.Context) ([]mysqlapp.Instance, error)
	DeleteByMachineID(ctx context.Context, machineID string) error
}

// ClusterCleanupMachineResult 是集群清理时单台机器的清理结果。
type ClusterCleanupMachineResult struct {
	MachineID          string   `json:"machine_id"`
	Name               string   `json:"name"`
	IP                 string   `json:"ip"`
	MySQLUninstallTask []string `json:"mysql_uninstall_tasks,omitempty"`
	MySQLPorts         []int    `json:"mysql_ports,omitempty"`
	AgentUninstalled   bool     `json:"agent_uninstalled"`
	LocalCleaned       bool     `json:"local_cleaned"`
	Error              string   `json:"error,omitempty"`
}

// ClusterCleanupResult 是集群清理的总结果，包含每台机器的清理详情。
type ClusterCleanupResult struct {
	Cluster string                        `json:"cluster"`
	Items   []ClusterCleanupMachineResult `json:"items"`
	Failed  int                           `json:"failed"`
}

// SSHCredentialView 是 SSH 凭据的展示视图。
type SSHCredentialView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SSHUser   string `json:"ssh_user"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ClusterView 是集群的展示视图，包含集群名称、描述和所属机器列表。
type ClusterView struct {
	Name        string
	Description string
	Machines    []string
	CreatedAt   string
}

// DynamicMetricsView 是动态指标的展示视图，关联了机器信息和心跳状态。
type DynamicMetricsView struct {
	MachineID       string                       `json:"machine_id"`
	MachineName     string                       `json:"machine_name"`
	MachineIP       string                       `json:"machine_ip"`
	HeartbeatState  string                       `json:"heartbeat_state"`
	LastHeartbeatAt string                       `json:"last_heartbeat_at"`
	Metrics         []dynamicdomain.MetricResult `json:"metrics"`
}

// NewMachineService 创建机器管理服务实例。
func NewMachineService(onboard *machineusecase.OnboardUsecase, machineRepo machinedomain.Repository, clusterRepo clusterdomain.Repository, credRepo credentialdomain.Repository, infoRepo MachineInfoRepository, staticRepo StaticInfoRepository, recoveryRepo machineDataCleaner, sshClient machineusecase.SSHClient, agentSvc *AgentService, taskSvc *TaskService) *MachineService {
	return &MachineService{
		onboard:      onboard,
		machineRepo:  machineRepo,
		clusterRepo:  clusterRepo,
		credRepo:     credRepo,
		infoRepo:     infoRepo,
		staticRepo:   staticRepo,
		recoveryRepo: recoveryRepo,
		sshClient:    sshClient,
		agentSvc:     agentSvc,
		taskSvc:      taskSvc,
	}
}

// Onboard 纳管一台新机器，自动解析 SSH 凭据并执行 SSH 连接和信任建立。
func (s *MachineService) Onboard(ctx context.Context, req machineusecase.OnboardMachineRequest) (machineusecase.OnboardMachineResponse, error) {
	credentialSelector := strings.TrimSpace(req.CredentialID)
	if credentialSelector == "" {
		credentialSelector = strings.TrimSpace(req.CredentialName)
	}
	if credentialSelector != "" {
		cred, ok, err := s.resolveCredential(ctx, credentialSelector)
		if err != nil {
			return machineusecase.OnboardMachineResponse{}, err
		}
		if !ok {
			return machineusecase.OnboardMachineResponse{}, errors.New("ssh credential not found")
		}
		if strings.TrimSpace(req.SSHUser) != "" && strings.TrimSpace(req.SSHUser) != cred.SSHUser {
			return machineusecase.OnboardMachineResponse{}, errors.New("ssh_user does not match selected credential")
		}
		req.SSHUser = cred.SSHUser
		if strings.TrimSpace(req.SSHPassword) == "" {
			req.SSHPassword = cred.SSHPassword
		}
		req.CredentialID = cred.ID
	}
	return s.onboard.Execute(ctx, req)
}

// CreateSSHCredential 创建 SSH 凭据。
func (s *MachineService) CreateSSHCredential(ctx context.Context, name, sshUser, sshPassword string) (SSHCredentialView, error) {
	if s.credRepo == nil {
		return SSHCredentialView{}, errors.New("ssh credential repository not configured")
	}
	name = strings.TrimSpace(name)
	sshUser = strings.TrimSpace(sshUser)
	if name == "" {
		return SSHCredentialView{}, errors.New("credential name is required")
	}
	if sshUser == "" {
		return SSHCredentialView{}, errors.New("ssh_user is required")
	}
	if strings.TrimSpace(sshPassword) == "" {
		return SSHCredentialView{}, errors.New("ssh_password is required")
	}
	item, err := s.credRepo.Save(ctx, credentialdomain.SSHCredential{
		Name:        name,
		SSHUser:     sshUser,
		SSHPassword: sshPassword,
	})
	if err != nil {
		return SSHCredentialView{}, err
	}
	return credentialView(item), nil
}

// ListSSHCredentials 列出所有 SSH 凭据。
func (s *MachineService) ListSSHCredentials(ctx context.Context) ([]SSHCredentialView, error) {
	if s.credRepo == nil {
		return nil, errors.New("ssh credential repository not configured")
	}
	items, err := s.credRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SSHCredentialView, 0, len(items))
	for _, item := range items {
		out = append(out, credentialView(item))
	}
	return out, nil
}

// DeleteSSHCredential 按 ID 或名称删除 SSH 凭据。
func (s *MachineService) DeleteSSHCredential(ctx context.Context, selector string) error {
	if s.credRepo == nil {
		return errors.New("ssh credential repository not configured")
	}
	item, ok, err := s.resolveCredential(ctx, selector)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("ssh credential not found")
	}
	return s.credRepo.Delete(ctx, item.ID)
}

// ListMachines 返回所有机器列表。
func (s *MachineService) ListMachines(ctx context.Context) ([]machinedomain.Machine, error) {
	return s.machineRepo.List(ctx)
}

// CreateCluster 创建新集群。
func (s *MachineService) CreateCluster(ctx context.Context, name, description string) error {
	return s.clusterRepo.Create(ctx, clusterdomain.Cluster{Name: name, Description: description})
}

// ListClusters 列出所有集群及其关联的机器信息。
func (s *MachineService) ListClusters(ctx context.Context) ([]ClusterView, error) {
	clusters, err := s.clusterRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	byCluster := make(map[string][]string)
	for _, item := range machines {
		if strings.TrimSpace(item.Cluster) == "" {
			continue
		}
		byCluster[item.Cluster] = append(byCluster[item.Cluster], fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	out := make([]ClusterView, 0, len(clusters))
	for _, item := range clusters {
		out = append(out, ClusterView{
			Name:        item.Name,
			Description: item.Description,
			Machines:    byCluster[item.Name],
			CreatedAt:   item.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out, nil
}

// CheckSSHTrust 检查目标机器的 SSH 免密连接是否可用。
func (s *MachineService) CheckSSHTrust(ctx context.Context, ip string, sshPort int, sshUser string) (bool, error) {
	return s.sshClient.CheckTrustConnection(ctx, machinedomain.Endpoint{
		IP:      ip,
		SSHPort: sshPort,
	}, sshUser)
}

// UpdateMachine 更新机器的基本信息（名称、IP、SSH 端口、SSH 用户）。
func (s *MachineService) UpdateMachine(ctx context.Context, machineID, name, ip string, sshPort int, sshUser string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(ip) == "" || strings.TrimSpace(sshUser) == "" || sshPort <= 0 {
		return errors.New("name, ip, ssh_port and ssh_user are required")
	}
	_, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	return s.machineRepo.UpdateBasics(ctx, machinedomain.Machine{
		ID:      machineID,
		Name:    strings.TrimSpace(name),
		IP:      strings.TrimSpace(ip),
		SSHPort: sshPort,
		SSHUser: strings.TrimSpace(sshUser),
	})
}

// DeleteMachine 删除机器，会先卸载 Agent，再清理本地关联数据。
func (s *MachineService) DeleteMachine(ctx context.Context, machineID string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	if s.agentSvc != nil {
		if _, err := s.agentSvc.UninstallByIP(ctx, machine.IP); err != nil {
			return fmt.Errorf("agent uninstall failed: %w", err)
		}
	}
	return s.cleanupMachineLocalData(ctx, machineID, true)
}

// AssignMachineCluster 将机器分配到指定集群，分配后自动安装 Agent 并采集静态信息。
func (s *MachineService) AssignMachineCluster(ctx context.Context, machineID, clusterName string) error {
	if strings.TrimSpace(machineID) == "" {
		return errors.New("machine id is required")
	}
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return errors.New("cluster name is required")
	}
	_, ok, err := s.machineRepo.GetByID(ctx, machineID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("machine not found")
	}
	ok, err = s.clusterRepo.Exists(ctx, clusterName)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.machineRepo.AssignCluster(ctx, machineID, clusterName); err != nil {
		return err
	}
	if s.agentSvc != nil {
		if err := s.agentSvc.EnsureInstalledForMachine(ctx, machineID); err != nil {
			return fmt.Errorf("cluster assigned but auto agent install failed: %w", err)
		}
	}
	if s.taskSvc != nil {
		machine, ok, err := s.machineRepo.GetByID(ctx, machineID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("machine not found")
		}
		if _, err := s.RefreshStaticInfo(ctx, machine.IP); err != nil {
			return fmt.Errorf("cluster assigned but static collect failed: %w", err)
		}
	}
	return nil
}

// UpdateCluster 更新集群名称和描述，同时更新关联机器的集群引用。
func (s *MachineService) UpdateCluster(ctx context.Context, oldName, newName, description string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return errors.New("old_name and new_name are required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.clusterRepo.Update(ctx, oldName, clusterdomain.Cluster{Name: newName, Description: description}); err != nil {
		return err
	}
	return s.machineRepo.RebindCluster(ctx, oldName, newName)
}

// DeleteCluster 删除集群，会先清除关联机器的集群字段。
func (s *MachineService) DeleteCluster(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("cluster name is required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("cluster not found")
	}
	if err := s.machineRepo.ClearCluster(ctx, name); err != nil {
		return err
	}
	return s.clusterRepo.Delete(ctx, name)
}

// CleanupCluster 清理集群，按顺序卸载每台机器上的 MySQL 实例和 Agent，清理本地数据后删除集群。
func (s *MachineService) CleanupCluster(ctx context.Context, name string) (ClusterCleanupResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ClusterCleanupResult{}, errors.New("cluster name is required")
	}
	_, ok, err := s.clusterRepo.Get(ctx, name)
	if err != nil {
		return ClusterCleanupResult{}, err
	}
	if !ok {
		return ClusterCleanupResult{}, errors.New("cluster not found")
	}
	if s.taskSvc == nil {
		return ClusterCleanupResult{}, errors.New("task service not configured")
	}
	machines, err := s.machineRepo.List(ctx)
	if err != nil {
		return ClusterCleanupResult{}, err
	}
	instancesByMachineID, err := s.mysqlInstancesByMachineID(ctx)
	if err != nil {
		return ClusterCleanupResult{}, err
	}

	result := ClusterCleanupResult{Cluster: name}
	for _, machine := range machines {
		if machine.Cluster != name {
			continue
		}
		item := ClusterCleanupMachineResult{MachineID: machine.ID, Name: machine.Name, IP: machine.IP}
		for _, instance := range instancesByMachineID[machine.ID] {
			item.MySQLPorts = append(item.MySQLPorts, instance.Port)
			detail, err := s.taskSvc.CreateMySQLUninstallTask(ctx, taskusecase.CreateMySQLUninstallTaskRequest{
				Machine: machine.IP,
				Port:    instance.Port,
			})
			if err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载任务创建失败: %v", instance.Port, err))
				continue
			}
			item.MySQLUninstallTask = append(item.MySQLUninstallTask, detail.Task.ID)
			finished, err := s.taskSvc.WaitForTask(ctx, detail.Task.ID, 2*time.Minute)
			if err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载等待失败: %v", instance.Port, err))
				continue
			}
			if finished.Task.Status != "success" {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("MySQL %d 卸载失败: %s", instance.Port, emptyTaskError(finished)))
			}
		}
		if item.Error == "" && s.agentSvc != nil {
			if _, err := s.agentSvc.UninstallByIP(ctx, machine.IP); err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("Agent 卸载失败: %v", err))
			} else {
				item.AgentUninstalled = true
			}
		}
		if item.Error == "" {
			if err := s.cleanupMachineLocalData(ctx, machine.ID, false); err != nil {
				item.Error = appendCleanupError(item.Error, fmt.Sprintf("本地记录清理失败: %v", err))
			} else {
				item.LocalCleaned = true
			}
		}
		if item.Error != "" {
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	if len(result.Items) == 0 {
		return ClusterCleanupResult{}, fmt.Errorf("cluster %s has no machines", name)
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("cluster cleanup failed for %d machine(s)", result.Failed)
	}
	if err := s.machineRepo.ClearCluster(ctx, name); err != nil {
		return result, err
	}
	if err := s.clusterRepo.Delete(ctx, name); err != nil {
		return result, err
	}
	return result, nil
}

func (s *MachineService) mysqlInstancesByMachineID(ctx context.Context) (map[string][]mysqlapp.Instance, error) {
	out := make(map[string][]mysqlapp.Instance)
	if s.taskSvc == nil || s.taskSvc.mysqlInstance == nil {
		return out, nil
	}
	items, err := s.taskSvc.mysqlInstance.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		out[item.MachineID] = append(out[item.MachineID], item)
	}
	return out, nil
}

func (s *MachineService) cleanupMachineLocalData(ctx context.Context, machineID string, deleteMachine bool) error {
	if s.agentSvc != nil {
		if s.agentSvc.heartbeat != nil {
			if err := s.agentSvc.heartbeat.RemoveMachine(ctx, machineID); err != nil {
				return err
			}
		}
		if s.agentSvc.repo != nil {
			if err := s.agentSvc.repo.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
	}
	if s.taskSvc != nil && s.taskSvc.mysqlInstance != nil {
		if cleaner, ok := s.taskSvc.mysqlInstance.(mysqlInstanceMachineCleaner); ok {
			if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
	}
	if cleaner, ok := s.staticRepo.(machineDataCleaner); ok {
		if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
			return err
		}
	}
	if s.recoveryRepo != nil {
		if err := s.recoveryRepo.DeleteByMachineID(ctx, machineID); err != nil {
			return err
		}
	}
	if deleteMachine {
		if cleaner, ok := s.infoRepo.(machineDataCleaner); ok {
			if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
				return err
			}
		}
		if s.taskSvc != nil {
			if cleaner, ok := s.taskSvc.repo.(machineDataCleaner); ok {
				if err := cleaner.DeleteByMachineID(ctx, machineID); err != nil {
					return err
				}
			}
		}
		return s.machineRepo.Delete(ctx, machineID)
	}
	return nil
}

func appendCleanupError(current, next string) string {
	if strings.TrimSpace(current) == "" {
		return next
	}
	return current + "; " + next
}

// SaveMachineInfo 保存机器采集信息。
func (s *MachineService) SaveMachineInfo(ctx context.Context, item collectdomain.MachineInfo) error {
	if s.infoRepo == nil {
		return errors.New("machine info repository not configured")
	}
	return s.infoRepo.Save(ctx, item)
}

// GetMachineInfo 获取指定机器的采集信息（按 IP 或名称查找）。
func (s *MachineService) GetMachineInfo(ctx context.Context, machineSelector string) (collectdomain.MachineInfo, error) {
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if !ok {
		return collectdomain.MachineInfo{}, errors.New("machine not found")
	}
	if s.infoRepo == nil {
		return collectdomain.MachineInfo{}, errors.New("machine info repository not configured")
	}
	item, ok, err := s.infoRepo.Get(ctx, machine.ID)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if !ok {
		return collectdomain.MachineInfo{}, errors.New("machine info not found")
	}
	return item, nil
}

// RefreshMachineInfo 触发机器信息采集任务并等待完成后返回结果。
func (s *MachineService) RefreshMachineInfo(ctx context.Context, machineSelector string) (collectdomain.MachineInfo, error) {
	if s.taskSvc == nil {
		return collectdomain.MachineInfo{}, errors.New("task service not configured")
	}
	taskDetail, err := s.taskSvc.CreateCollectMachineInfoTask(ctx, machineSelector)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	finished, err := s.taskSvc.WaitForTask(ctx, taskDetail.Task.ID, 40*time.Second)
	if err != nil {
		return collectdomain.MachineInfo{}, err
	}
	if finished.Task.Status != "success" {
		return collectdomain.MachineInfo{}, fmt.Errorf("collect task failed: %s", emptyTaskError(finished))
	}
	return s.GetMachineInfo(ctx, machineSelector)
}

// GetStaticInfo 获取指定机器的静态信息。
func (s *MachineService) GetStaticInfo(ctx context.Context, machineSelector string) (collectdomain.StaticInfo, error) {
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if !ok {
		return collectdomain.StaticInfo{}, errors.New("machine not found")
	}
	if s.staticRepo == nil {
		return collectdomain.StaticInfo{}, errors.New("static info repository not configured")
	}
	item, ok, err := s.staticRepo.Get(ctx, machine.ID)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if !ok {
		return collectdomain.StaticInfo{}, errors.New("static info not found")
	}
	return item, nil
}

// RefreshStaticInfo 触发静态信息采集任务并等待完成后返回结果。
func (s *MachineService) RefreshStaticInfo(ctx context.Context, machineSelector string) (collectdomain.StaticInfo, error) {
	if s.taskSvc == nil {
		return collectdomain.StaticInfo{}, errors.New("task service not configured")
	}
	taskDetail, err := s.taskSvc.CreateCollectStaticInfoTask(ctx, taskusecase.CreateCollectStaticInfoRequest{Machine: machineSelector})
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	finished, err := s.taskSvc.WaitForTask(ctx, taskDetail.Task.ID, 60*time.Second)
	if err != nil {
		return collectdomain.StaticInfo{}, err
	}
	if finished.Task.Status != "success" {
		return collectdomain.StaticInfo{}, fmt.Errorf("static collect task failed: %s", emptyTaskError(finished))
	}
	return s.GetStaticInfo(ctx, machineSelector)
}

// GetMachineDynamicMetrics 获取指定机器的主机动态指标。
func (s *MachineService) GetMachineDynamicMetrics(ctx context.Context, machineSelector string) (DynamicMetricsView, error) {
	return s.getDynamicMetrics(ctx, machineSelector, false)
}

// GetMySQLDynamicMetrics 获取指定机器的 MySQL 动态指标。
func (s *MachineService) GetMySQLDynamicMetrics(ctx context.Context, machineSelector string) (DynamicMetricsView, error) {
	return s.getDynamicMetrics(ctx, machineSelector, true)
}

func (s *MachineService) getDynamicMetrics(ctx context.Context, machineSelector string, mysqlOnly bool) (DynamicMetricsView, error) {
	if s.agentSvc == nil {
		return DynamicMetricsView{}, errors.New("agent service not configured")
	}
	machineTarget, mysqlPort := splitMachinePortSelector(machineSelector)
	machine, ok, err := s.resolveMachine(ctx, machineSelector)
	if !ok && mysqlPort > 0 {
		machine, ok, err = s.resolveMachine(ctx, machineTarget)
	}
	if err != nil {
		return DynamicMetricsView{}, err
	}
	if !ok {
		return DynamicMetricsView{}, errors.New("machine not found")
	}
	agentView, ok, err := s.agentSvc.GetViewByIP(ctx, machine.IP)
	if err != nil {
		return DynamicMetricsView{}, err
	}
	if !ok {
		return DynamicMetricsView{}, errors.New("agent heartbeat not found")
	}
	metrics := make([]dynamicdomain.MetricResult, 0, len(agentView.Metrics))
	for _, item := range agentView.Metrics {
		isMySQL := isMySQLDynamicMetric(item)
		if mysqlOnly && mysqlPort > 0 && !metricMatchesMySQLPort(item, mysqlPort) {
			continue
		}
		if mysqlOnly == isMySQL {
			metrics = append(metrics, item)
		}
	}
	return DynamicMetricsView{
		MachineID:       machine.ID,
		MachineName:     machine.Name,
		MachineIP:       machine.IP,
		HeartbeatState:  agentView.HeartbeatState,
		LastHeartbeatAt: agentView.LastHeartbeatAt,
		Metrics:         metrics,
	}, nil
}

func metricMatchesMySQLPort(item dynamicdomain.MetricResult, port int) bool {
	if port <= 0 {
		return true
	}
	labelPort := strings.TrimSpace(item.Labels["mysql_port"])
	if labelPort == "" {
		return port == 3306
	}
	return labelPort == strconv.Itoa(port)
}

func isMySQLDynamicMetric(item dynamicdomain.MetricResult) bool {
	switch item.Labels["metric_scope"] {
	case "mysql_dynamic":
		return true
	case "machine_dynamic":
		return false
	}
	if strings.HasPrefix(item.Name, "mysql_") {
		return true
	}
	switch item.Category {
	case "mysql", "connection", "variables", "replication", "topology", "performance", "storage":
		return true
	default:
		return false
	}
}

func splitMachinePortSelector(selector string) (string, int) {
	selector = strings.TrimSpace(selector)
	host, portText, ok := strings.Cut(selector, ":")
	if !ok || strings.TrimSpace(host) == "" || strings.TrimSpace(portText) == "" {
		return selector, 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port <= 0 {
		return selector, 0
	}
	return strings.TrimSpace(host), port
}

func (s *MachineService) resolveMachine(ctx context.Context, selector string) (machinedomain.Machine, bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return machinedomain.Machine{}, false, errors.New("machine selector is required")
	}
	if item, ok, err := s.machineRepo.GetByIP(ctx, selector); err != nil {
		return machinedomain.Machine{}, false, err
	} else if ok {
		return item, true, nil
	}
	items, err := s.machineRepo.List(ctx)
	if err != nil {
		return machinedomain.Machine{}, false, err
	}
	for _, item := range items {
		if item.Name == selector {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}

func (s *MachineService) resolveCredential(ctx context.Context, selector string) (credentialdomain.SSHCredential, bool, error) {
	if s.credRepo == nil {
		return credentialdomain.SSHCredential{}, false, errors.New("ssh credential repository not configured")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return credentialdomain.SSHCredential{}, false, errors.New("ssh credential selector is required")
	}
	if item, ok, err := s.credRepo.GetByID(ctx, selector); err != nil {
		return credentialdomain.SSHCredential{}, false, err
	} else if ok {
		return item, true, nil
	}
	return s.credRepo.GetByName(ctx, selector)
}

func credentialView(item credentialdomain.SSHCredential) SSHCredentialView {
	return SSHCredentialView{
		ID:        item.ID,
		Name:      item.Name,
		SSHUser:   item.SSHUser,
		CreatedAt: item.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		UpdatedAt: item.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
	}
}

func emptyTaskError(detail TaskDetail) string {
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if strings.TrimSpace(detail.Events[i].Content) != "" {
			return detail.Events[i].Content
		}
	}
	return "unknown error"
}
