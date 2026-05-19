// Package machine 是机器管理的应用服务层，负责机器的接入和集群分配等用例的编排。
package machine

import "context"

// AssignClusterRequest 是分配集群的请求参数。
type AssignClusterRequest struct {
	MachineID  string
	Cluster    string
	InstallDir string
}

// AssignClusterResponse 是分配集群的响应结果。
type AssignClusterResponse struct {
	MachineID string
	Cluster   string
}

// AssignClusterUsecase 是分配集群的用例，负责将机器分配到指定集群。
type AssignClusterUsecase struct{}

// NewAssignClusterUsecase 创建一个新的分配集群用例实例。
func NewAssignClusterUsecase() *AssignClusterUsecase {
	return &AssignClusterUsecase{}
}

// Execute 执行集群分配操作。
func (u *AssignClusterUsecase) Execute(ctx context.Context, req AssignClusterRequest) (AssignClusterResponse, error) {
	_ = ctx
	return AssignClusterResponse{MachineID: req.MachineID, Cluster: req.Cluster}, nil
}
