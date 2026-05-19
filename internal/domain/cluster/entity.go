// Package cluster 定义了集群实体的领域模型和仓储接口。
// 集群是逻辑分组，用于将多台机器组织在一起进行统一管理。
package cluster

import (
	"context"
	"time"
)

// Cluster 是集群实体的领域模型，表示一组关联的机器。
type Cluster struct {
	Name        string
	Description string
	CreatedAt   time.Time
}

// Repository 定义了集群实体的仓储接口，用于持久化集群数据。
type Repository interface {
	Exists(ctx context.Context, name string) (bool, error)
	Create(ctx context.Context, cluster Cluster) error
	Get(ctx context.Context, name string) (Cluster, bool, error)
	List(ctx context.Context) ([]Cluster, error)
	Update(ctx context.Context, oldName string, cluster Cluster) error
	Delete(ctx context.Context, name string) error
}
