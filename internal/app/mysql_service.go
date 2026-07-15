package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	machinedomain "gmha/internal/domain/machine"
	taskdomain "gmha/internal/domain/task"
	mysqlapp "gmha/internal/mysql"
)

// MySQLInstanceRepository 定义了 MySQL 实例的持久化操作接口。
type MySQLInstanceRepository interface {
	List(ctx context.Context) ([]mysqlapp.Instance, error)
	Get(ctx context.Context, machineID string, port int) (mysqlapp.Instance, bool, error)
	Delete(ctx context.Context, machineID string, port int) error
	UpdateStatus(ctx context.Context, machineID string, port int, status string) error
	PruneUninstalled(ctx context.Context) (int64, error)
}

type MySQLAccountPresetRepository interface {
	List(ctx context.Context) ([]taskdomain.MySQLAccountSpec, error)
	Save(ctx context.Context, items []taskdomain.MySQLAccountSpec) error
}

// MySQLService 是 MySQL 实例管理服务，负责实例列表、视图聚合（关联机器和心跳）、遗忘实例等操作。
type MySQLService struct {
	repo      MySQLInstanceRepository
	machines  machinedomain.Repository
	heartbeat *HeartbeatService
	presets   MySQLAccountPresetRepository
}

// MySQLInstanceView 是 MySQL 实例的聚合视图，关联了机器名称、IP、集群和心跳状态。
type MySQLInstanceView struct {
	mysqlapp.Instance
	MachineName        string `json:"machine_name"`
	MachineIP          string `json:"machine_ip"`
	Cluster            string `json:"cluster"`
	HeartbeatStatus    string `json:"heartbeat_status"`
	HeartbeatDetail    string `json:"heartbeat_detail"`
	HeartbeatCheckedAt string `json:"heartbeat_checked_at"`
}

// NewMySQLService 创建 MySQL 服务实例。
func NewMySQLService(repo MySQLInstanceRepository, machines machinedomain.Repository, heartbeat *HeartbeatService, presets MySQLAccountPresetRepository) *MySQLService {
	return &MySQLService{repo: repo, machines: machines, heartbeat: heartbeat, presets: presets}
}

func (s *MySQLService) AccountPresets(ctx context.Context) ([]taskdomain.MySQLAccountSpec, error) {
	if s.presets == nil {
		return defaultMySQLAccountPresets(), nil
	}
	items, err := s.presets.List(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeMySQLAccountPresets(items), nil
}

func (s *MySQLService) SaveAccountPresets(ctx context.Context, items []taskdomain.MySQLAccountSpec) ([]taskdomain.MySQLAccountSpec, error) {
	if s.presets == nil {
		return nil, errors.New("mysql account preset repository is not configured")
	}
	items = normalizeMySQLAccountPresets(items)
	if err := s.presets.Save(ctx, items); err != nil {
		return nil, err
	}
	return s.AccountPresets(ctx)
}

func defaultMySQLAccountPresets() []taskdomain.MySQLAccountSpec {
	return []taskdomain.MySQLAccountSpec{{Role: "monitor", Username: "monitor", Password: "3306niubi", Host: "%", Enabled: true, Privileges: mysqlapp.DefaultPrivileges("monitor")}, {Role: "mha", Username: "mha", Password: "3306niubi", Host: "%", Enabled: true, Privileges: mysqlapp.DefaultPrivileges("mha")}, {Role: "backup", Username: "backup", Password: "3306niubi", Host: "%", Enabled: true, Privileges: mysqlapp.DefaultPrivileges("backup")}}
}

func normalizeMySQLAccountPresets(items []taskdomain.MySQLAccountSpec) []taskdomain.MySQLAccountSpec {
	defaults := defaultMySQLAccountPresets()
	byRole := make(map[string]taskdomain.MySQLAccountSpec, len(defaults))
	for _, item := range defaults {
		byRole[item.Role] = item
	}
	for _, item := range items {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		base, ok := byRole[role]
		if !ok {
			continue
		}
		if strings.TrimSpace(item.Username) != "" {
			base.Username = strings.TrimSpace(item.Username)
		}
		if item.Password != "" {
			base.Password = item.Password
		}
		if strings.TrimSpace(item.Host) != "" {
			base.Host = strings.TrimSpace(item.Host)
		}
		base.Enabled = item.Enabled
		base.ExtendedBackup = item.ExtendedBackup
		if item.Privileges != nil {
			base.Privileges = append([]string(nil), item.Privileges...)
		}
		byRole[role] = base
	}
	return []taskdomain.MySQLAccountSpec{byRole["monitor"], byRole["mha"], byRole["backup"]}
}

// ListInstances 返回所有 MySQL 实例列表，会自动清理已卸载的实例记录。
func (s *MySQLService) ListInstances(ctx context.Context) ([]mysqlapp.Instance, error) {
	if err := s.pruneUninstalled(ctx); err != nil {
		return nil, err
	}
	return s.repo.List(ctx)
}

// ListInstanceViews 返回所有 MySQL 实例的聚合视图，关联机器信息和心跳状态。
func (s *MySQLService) ListInstanceViews(ctx context.Context) ([]MySQLInstanceView, error) {
	if err := s.pruneUninstalled(ctx); err != nil {
		return nil, err
	}
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MySQLInstanceView, 0, len(items))
	heartbeatByMachineID := make(map[string]HeartbeatView)
	if s.heartbeat != nil {
		for _, item := range s.heartbeat.Snapshot() {
			heartbeatByMachineID[item.MachineID] = item
		}
	}
	for _, item := range items {
		view := MySQLInstanceView{Instance: item, HeartbeatStatus: "-", HeartbeatDetail: "-", HeartbeatCheckedAt: "-"}
		if s.machines != nil {
			machine, ok, err := s.machines.GetByID(ctx, item.MachineID)
			if err != nil {
				return nil, err
			}
			if ok {
				view.MachineName = machine.Name
				view.MachineIP = machine.IP
				view.Cluster = machine.Cluster
			}
		}
		if hb, ok := heartbeatByMachineID[item.MachineID]; ok {
			for _, check := range hb.Checks {
				if check.Name != fmt.Sprintf("mysql.heartbeat.%d", item.Port) {
					continue
				}
				view.HeartbeatStatus = string(check.Status)
				view.HeartbeatDetail = check.Detail
				if !check.CheckedAt.IsZero() {
					view.HeartbeatCheckedAt = check.CheckedAt.Local().Format("2006-01-02 15:04:05")
				}
				break
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *MySQLService) pruneUninstalled(ctx context.Context) error {
	_, err := s.repo.PruneUninstalled(ctx)
	return err
}

// ForgetInstance 从管理端遗忘指定机器上的 MySQL 实例记录（不执行远程卸载）。
func (s *MySQLService) ForgetInstance(ctx context.Context, machine string, port int) error {
	target := strings.TrimSpace(machine)
	if target == "" {
		return errors.New("machine is required")
	}
	if port <= 0 {
		return errors.New("port is required")
	}
	item, ok, err := s.resolveMachine(ctx, target)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("machine %s not found", target)
	}
	return s.repo.Delete(ctx, item.ID, port)
}

func (s *MySQLService) resolveMachine(ctx context.Context, target string) (machinedomain.Machine, bool, error) {
	if s.machines == nil {
		return machinedomain.Machine{}, false, errors.New("machine repository is not configured")
	}
	if machine, ok, err := s.machines.GetByIP(ctx, target); err != nil {
		return machinedomain.Machine{}, false, err
	} else if ok {
		return machine, true, nil
	}
	items, err := s.machines.List(ctx)
	if err != nil {
		return machinedomain.Machine{}, false, err
	}
	for _, item := range items {
		if strings.EqualFold(item.Name, target) || item.ID == target {
			return item, true, nil
		}
	}
	return machinedomain.Machine{}, false, nil
}
