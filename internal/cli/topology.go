package cli

import (
	"fmt"

	"GMHA-MySQL/internal/store"
)

// PrintClusterList 打印集群列表（添加集群后立即显示）
func PrintClusterList(s *store.Store) {
	list, err := s.ListClusters()
	if err != nil {
		fmt.Println("  [错误] 读取集群列表失败:", err)
		return
	}
	if len(list) == 0 {
		fmt.Println("  [拓扑] 当前无集群")
		return
	}
	fmt.Println("  ---------- 集群列表 ----------")
	for _, c := range list {
		fmt.Printf("  集群ID: %s   Worker: %s  创建: %s\n", c.ID, c.WorkerAddr, c.CreatedAt)
	}
	fmt.Println("  -----------------------------")
}

// PrintClusterTopology 打印某集群的拓扑（主机 + 实例，添加主机/实例后立即显示）
func PrintClusterTopology(s *store.Store, clusterID string) {
	fmt.Printf("  ---------- 集群 %s 拓扑 ----------\n", clusterID)
	hosts, err := s.ListHosts(clusterID)
	if err != nil {
		fmt.Println("  [错误] 读取主机列表失败:", err)
		return
	}
	if len(hosts) > 0 {
		fmt.Println("  主机:")
		for _, h := range hosts {
			fmt.Printf("    - %s  (SSH %s@%s:%d)\n", h.IP, h.SSHUser, h.IP, h.SSHPort)
		}
	}
	instances, err := s.ListInstances(clusterID)
	if err != nil {
		fmt.Println("  [错误] 读取实例列表失败:", err)
		return
	}
	if len(instances) > 0 {
		fmt.Println("  实例:")
		for _, i := range instances {
			addr := fmt.Sprintf("%s:%d", i.Host, i.Port)
			if i.Role == "master" {
				fmt.Printf("    master: %s\n", addr)
			} else {
				fmt.Printf("    slave:  %s  -> master %s\n", addr, i.MasterAddr)
			}
		}
	}
	if len(hosts) == 0 && len(instances) == 0 {
		fmt.Println("  (暂无主机与实例)")
	}
	fmt.Println("  ------------------------------------")
}
