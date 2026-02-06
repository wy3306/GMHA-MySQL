package intercore

import (
	"GMHA-MySQL/internal/model"
	"fmt"

	"gorm.io/gorm"
)

// GetClusterInfo 获取集群信息
func GetClusterInfo(db *gorm.DB) (*model.Cluster, error) {
	var cluster model.Cluster
	// 使用 Preload 可以预加载关联的 Machines 数据
	if err := db.Preload("Machines").First(&cluster).Error; err != nil {
		return nil, err
	}
	return &cluster, nil
}

// AddCluster 添加集群信息
func AddCluster(db *gorm.DB, cluster *model.Cluster) error {
	return db.Create(cluster).Error
}

// ShowClusterInfo 显示集群信息
func ShowClusterInfo(db *gorm.DB) {
	var cluster model.Cluster
	if err := db.First(&cluster).Error; err != nil {
		fmt.Println("获取集群信息失败:", err)
		return
	}
	fmt.Printf("集群信息:\n")
	fmt.Printf("  ID: %d\n", cluster.ID)
	fmt.Printf("  名称: %s\n", cluster.Name)
	fmt.Printf("  状态: %s\n", cluster.Status)
	fmt.Printf("  描述: %s\n", cluster.Description)
	fmt.Printf("  集群IP: %s\n", cluster.ClusterIP)
	fmt.Printf("  VIP: %s\n", cluster.VIP)
	fmt.Printf("  VIP状态: %s\n", cluster.VIPStatus)
	fmt.Printf("  SSH信任: %s\n", cluster.SSHTrust)
	fmt.Printf("  是否有Layer3交换机: %s\n", cluster.HasLayer3Switch)
	fmt.Printf("  管理端口: %s\n", cluster.ManagerPort)
}
