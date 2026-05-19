package v1

import "errors"

// CreateClusterRequest 表示创建集群的请求参数，包含集群名称。
type CreateClusterRequest struct {
	Name string `json:"name"`
}

// Validate 对创建集群请求参数进行校验，确保集群名称不为空。
func (r CreateClusterRequest) Validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

// ListClustersResponse 表示集群列表的响应结果，包含集群名称列表。
type ListClustersResponse struct {
	Items []string `json:"items"`
}
