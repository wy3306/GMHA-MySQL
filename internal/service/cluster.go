package service

import (
	"GMHA-MySQL/internal/model"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// CreateClusterInput 创建集群的输入参数 (DTO)
// 这个结构体是“交互内核”的核心，无论是 Web、CLI 还是引导模式，最终都构造这个结构体传给 Service
type CreateClusterInput struct {
	Name        string
	Description string
	VIP         string
	VIPEnabled  bool
	HasL3Switch bool

	// 初始机器列表 (可选)
	Machines []CreateMachineInput
}

type CreateMachineInput struct {
	IP          string
	SSHPort     int
	SSHUser     string
	SSHPassword string
}

// ClusterService 定义集群相关的业务逻辑接口
type ClusterService interface {
	// CreateCluster 创建集群 (包含关联的机器)
	CreateCluster(ctx context.Context, input CreateClusterInput) (*model.Cluster, error)
	// ListClusters 列出集群
	ListClusters(ctx context.Context) ([]model.Cluster, error)
}

type clusterService struct {
	clusterRepo model.ClusterRepository
	machineRepo model.MachineRepository
	db          *gorm.DB // 用于事务
}

func NewClusterService(
	clusterRepo model.ClusterRepository,
	machineRepo model.MachineRepository,
	db *gorm.DB,
) ClusterService {
	return &clusterService{
		clusterRepo: clusterRepo,
		machineRepo: machineRepo,
		db:          db,
	}
}

func (s *clusterService) CreateCluster(ctx context.Context, input CreateClusterInput) (*model.Cluster, error) {
	// 1. 基础校验
	if input.Name == "" {
		return nil, errors.New("cluster name is required")
	}

	// 2. 构造模型对象
	cluster := &model.Cluster{
		ID:          generateID(), // 需要实现或使用 UUID
		Name:        input.Name,
		Description: input.Description,
		VIP:         input.VIP,
		VIPEnabled:  input.VIPEnabled,
		HasL3Switch: input.HasL3Switch,
		Status:      model.ClusterStatusUnknown, // 初始状态
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// 3. 开启事务保存
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 这里我们需要使用传入 tx 的 repo，或者确保 repo 内部能使用 tx
		// 由于目前的 repo 设计是持有一个 db 实例，我们需要一种方式在事务中使用 repo
		// 简单的做法是：Repo 方法接受 *gorm.DB 参数，或者 Service 直接操作 DB (不推荐)，
		// 或者 Repo 提供 WithTx 方法。
		// 为了简单起见，且由于我们有 AutoMigrate 和 GORM，我们可以直接用 s.clusterRepo.Create
		// 但要注意，如果不传递 tx 给 repo，那么事务将不会生效。
		//
		// 改进方案：Repo 接口通常不暴露 DB。
		// 在这里，为了演示“内核”逻辑，我们假设 Repo 是并发安全的且我们暂时不处理复杂的事务传递，
		// 或者我们使用 GORM 的关联插入特性 (Cluster 包含 Machines) 来一次性插入。

		// 构造机器列表
		var machines []model.Machine
		for _, mInput := range input.Machines {
			machines = append(machines, model.Machine{
				ID:          mInput.IP, // 假设 IP 为 ID
				IP:          mInput.IP,
				SSHPort:     mInput.SSHPort,
				SSHUser:     mInput.SSHUser,
				SSHPassword: mInput.SSHPassword,
				Status:      "Pending",
			})
		}
		cluster.Machines = machines

		// 使用 ClusterRepo 创建 (GORM 默认会处理关联创建)
		// 但是我们需要确保 Repo 使用的是当前的 TX。
		// 这是一个经典的 Go Clean Architecture 问题。
		// 临时解决方案：直接在 Service 里用 tx 创建，或者让 Repo 支持 WithTx。
		// 为了不修改 Repo 接口，我们这里直接用 GORM 的关联创建特性，
		// 只要 s.clusterRepo.Create 内部是 db.Create(c)，
		// 我们其实可以依赖 GORM 的 Association Autocreate。
		// 但如果 s.clusterRepo 持有的 db 不是 tx，那就不是事务。

		// 既然我们是 Senior Pair Programmer，我们应该做对。
		// 我们可以在 NewRepository 时传入 tx，但这很麻烦。
		// 既然 Cluster 包含 Machines，我们可以只调用 clusterRepo.Create(cluster)，
		// GORM 会自动插入 Machines。
		// 只要 Create 操作是原子的（单条 SQL 或 GORM 自动事务），就没问题。
		// GORM 的 Create 默认是事务的。

		return s.clusterRepo.Create(ctx, cluster)
	})

	if err != nil {
		return nil, err
	}

	return cluster, nil
}

func (s *clusterService) ListClusters(ctx context.Context) ([]model.Cluster, error) {
	return s.clusterRepo.List(ctx)
}

// 简单的 ID 生成器
func generateID() string {
	return time.Now().Format("20060102150405") // 示例 ID
}
