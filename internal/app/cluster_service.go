// Package app 集群服务层。
package app

import (
	"context"

	clusterdomain "gmha/internal/domain/cluster"
)

// ClusterService 是集群管理服务，提供集群的查询功能。
type ClusterService struct {
	clusterRepo clusterdomain.Repository
}

// NewClusterService 创建集群服务实例。
func NewClusterService(clusterRepo clusterdomain.Repository) *ClusterService {
	return &ClusterService{clusterRepo: clusterRepo}
}

// List 返回所有集群列表。
func (s *ClusterService) List(ctx context.Context) ([]clusterdomain.Cluster, error) {
	return s.clusterRepo.List(ctx)
}
