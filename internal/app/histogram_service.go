package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	machinedomain "gmha/internal/domain/machine"
	sqldomain "gmha/internal/domain/sqldiagnostic"
	mysqlapp "gmha/internal/mysql"
)

var ErrHistogramUnsupported = errors.New("histogram management is unsupported")

type HistogramInspectRequest struct {
	MachineID string
	Port      int
	Schema    string
	Table     string
}

type HistogramManageRequest struct {
	MachineID string
	Port      int
	Schema    string
	Table     string
	Columns   []string
	Buckets   int
}

// HistogramService manages optimizer column statistics for one registered
// MySQL instance. Credentials come only from the stored MHA account preset.
type HistogramService struct {
	instances MySQLInstanceRepository
	machines  machinedomain.Repository
	presets   MySQLAccountPresetRepository
	manager   mysqlapp.HistogramManager
}

func NewHistogramService(instances MySQLInstanceRepository, machines machinedomain.Repository, presets MySQLAccountPresetRepository) *HistogramService {
	return &HistogramService{
		instances: instances,
		machines:  machines,
		presets:   presets,
		manager: mysqlapp.HistogramClient{
			ConnectTimeout: 3 * time.Second,
			QueryTimeout:   2 * time.Minute,
		},
	}
}

func (s *HistogramService) Inspect(ctx context.Context, req HistogramInspectRequest) (mysqlapp.HistogramCatalog, error) {
	instance, err := s.target(ctx, req.MachineID, req.Port)
	if err != nil {
		return mysqlapp.HistogramCatalog{}, err
	}
	if strings.TrimSpace(instance.Version) != "" && !mysqlapp.SupportsHistogramForVersion(instance.Version) {
		return mysqlapp.HistogramCatalog{}, unsupportedHistogramVersion(instance.Version)
	}
	credential, err := s.credential(ctx)
	if err != nil {
		return mysqlapp.HistogramCatalog{}, err
	}
	catalog, err := s.manager.Inspect(ctx, instance, credential, strings.TrimSpace(req.Schema), strings.TrimSpace(req.Table))
	if errors.Is(err, mysqlapp.ErrHistogramVersionUnsupported) {
		return mysqlapp.HistogramCatalog{}, fmt.Errorf("%w: %v", ErrHistogramUnsupported, err)
	}
	return catalog, err
}

func (s *HistogramService) Update(ctx context.Context, req HistogramManageRequest) (mysqlapp.HistogramOperationResult, error) {
	if req.Buckets == 0 {
		req.Buckets = mysqlapp.DefaultHistogramBuckets
	}
	if req.Buckets < 1 || req.Buckets > mysqlapp.MaxHistogramBuckets {
		return mysqlapp.HistogramOperationResult{}, fmt.Errorf("直方图桶数必须在 1–%d 之间", mysqlapp.MaxHistogramBuckets)
	}
	instance, credential, _, err := s.validateManageRequest(ctx, req, false)
	if err != nil {
		return mysqlapp.HistogramOperationResult{}, err
	}
	result, err := s.manager.Update(ctx, instance, credential, strings.TrimSpace(req.Schema), strings.TrimSpace(req.Table), req.Columns, req.Buckets)
	if errors.Is(err, mysqlapp.ErrHistogramVersionUnsupported) {
		return result, fmt.Errorf("%w: %v", ErrHistogramUnsupported, err)
	}
	return result, err
}

func (s *HistogramService) Drop(ctx context.Context, req HistogramManageRequest) (mysqlapp.HistogramOperationResult, error) {
	instance, credential, _, err := s.validateManageRequest(ctx, req, true)
	if err != nil {
		return mysqlapp.HistogramOperationResult{}, err
	}
	result, err := s.manager.Drop(ctx, instance, credential, strings.TrimSpace(req.Schema), strings.TrimSpace(req.Table), req.Columns)
	if errors.Is(err, mysqlapp.ErrHistogramVersionUnsupported) {
		return result, fmt.Errorf("%w: %v", ErrHistogramUnsupported, err)
	}
	return result, err
}

func (s *HistogramService) validateManageRequest(ctx context.Context, req HistogramManageRequest, requireExisting bool) (sqldomain.Instance, mysqlapp.DiagnosticCredential, mysqlapp.HistogramCatalog, error) {
	instance, err := s.target(ctx, req.MachineID, req.Port)
	if err != nil {
		return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, err
	}
	if strings.TrimSpace(instance.Version) != "" && !mysqlapp.SupportsHistogramForVersion(instance.Version) {
		return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, unsupportedHistogramVersion(instance.Version)
	}
	credential, err := s.credential(ctx)
	if err != nil {
		return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, err
	}
	schema, table := strings.TrimSpace(req.Schema), strings.TrimSpace(req.Table)
	catalog, err := s.manager.Inspect(ctx, instance, credential, schema, table)
	if err != nil {
		if errors.Is(err, mysqlapp.ErrHistogramVersionUnsupported) {
			err = fmt.Errorf("%w: %v", ErrHistogramUnsupported, err)
		}
		return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, err
	}
	available := make(map[string]mysqlapp.HistogramColumn, len(catalog.Columns))
	for _, item := range catalog.Columns {
		available[item.Name] = item
	}
	existing := make(map[string]bool, len(catalog.Histograms))
	for _, item := range catalog.Histograms {
		if item.Schema == schema && item.Table == table {
			existing[item.Column] = true
		}
	}
	if len(req.Columns) == 0 {
		return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, errors.New("请至少选择一列")
	}
	for _, column := range req.Columns {
		item, ok := available[column]
		if !ok {
			return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, fmt.Errorf("列 %q 不存在于 %s.%s", column, schema, table)
		}
		if requireExisting {
			if !existing[column] {
				return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, fmt.Errorf("列 %q 当前没有可删除的直方图", column)
			}
			continue
		}
		if !item.Eligible {
			return sqldomain.Instance{}, mysqlapp.DiagnosticCredential{}, mysqlapp.HistogramCatalog{}, fmt.Errorf("列 %q 不能创建直方图：%s", column, item.IneligibleReason)
		}
	}
	return instance, credential, catalog, nil
}

func (s *HistogramService) target(ctx context.Context, machineID string, port int) (sqldomain.Instance, error) {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return sqldomain.Instance{}, errors.New("machine_id is required")
	}
	if port < 1 || port > 65535 {
		return sqldomain.Instance{}, errors.New("port must be between 1 and 65535")
	}
	item, ok, err := s.instances.Get(ctx, machineID, port)
	if err != nil {
		return sqldomain.Instance{}, err
	}
	if !ok {
		return sqldomain.Instance{}, fmt.Errorf("MySQL 实例 %s:%d 未登记", machineID, port)
	}
	machine, ok, err := s.machines.GetByID(ctx, machineID)
	if err != nil {
		return sqldomain.Instance{}, err
	}
	if !ok || strings.TrimSpace(machine.IP) == "" {
		return sqldomain.Instance{}, fmt.Errorf("实例 %s:%d 的机器地址不可用", machineID, port)
	}
	return sqldomain.Instance{
		MachineID: machineID, MachineName: machine.Name, MachineIP: machine.IP,
		Cluster: machine.Cluster, Port: port, Version: item.Version,
	}, nil
}

func (s *HistogramService) credential(ctx context.Context) (mysqlapp.DiagnosticCredential, error) {
	if s.presets == nil {
		return mysqlapp.DiagnosticCredential{}, errors.New("直方图管理需要已配置的 MHA 管理账号")
	}
	items, err := s.presets.List(ctx)
	if err != nil {
		return mysqlapp.DiagnosticCredential{}, err
	}
	for _, item := range normalizeMySQLAccountPresets(items) {
		if !item.Enabled || !strings.EqualFold(strings.TrimSpace(item.Role), mysqlapp.AccountRoleMHA) {
			continue
		}
		if strings.TrimSpace(item.Username) == "" || item.Password == "" {
			break
		}
		return mysqlapp.DiagnosticCredential{Username: strings.TrimSpace(item.Username), Password: item.Password}, nil
	}
	return mysqlapp.DiagnosticCredential{}, errors.New("直方图管理需要已启用且凭据完整的 MHA 管理账号")
}

func unsupportedHistogramVersion(version string) error {
	return fmt.Errorf("%w: MySQL %s 不支持直方图管理；该功能仅支持 MySQL 8.0 及以上版本，不兼容 MySQL 5.7", ErrHistogramUnsupported, strings.TrimSpace(version))
}
