package cli

import (
	"fmt"
	"strconv"
	"time"

	"GMHA-MySQL/internal/store"
)

// Execute 执行解析后的命令，执行添加类命令后会立即打印拓扑
func Execute(s *store.Store, cmd, clusterID string, flags map[string]string) (exit bool) {
	switch cmd {
	case "":
		return false
	case "exit":
		return true
	case "help":
		printHelp()
		return false
	case "cluster_help", "unknown", "unknown_sub":
		printHelp()
		return false
	case "cluster_add":
		return runClusterAdd(s, flags)
	case "cluster_list":
		list, err := s.ListClusters()
		if err != nil {
			fmt.Println("  [错误]", err)
			return false
		}
		if len(list) == 0 {
			fmt.Println("  [拓扑] 当前无集群")
			return false
		}
		PrintClusterList(s)
		return false
	case "cluster_show", "cluster_show_need_id":
		if clusterID == "" {
			fmt.Println("  用法: cluster show <cluster_id>")
			return false
		}
		c, err := s.GetCluster(clusterID)
		if err != nil || c == nil {
			fmt.Println("  [错误] 集群不存在:", clusterID)
			return false
		}
		PrintClusterTopology(s, clusterID)
		return false
	case "host_add":
		return runHostAdd(s, clusterID, flags)
	case "host_list":
		c, err := s.GetCluster(clusterID)
		if err != nil || c == nil {
			fmt.Println("  [错误] 集群不存在:", clusterID)
			return false
		}
		PrintClusterTopology(s, clusterID)
		return false
	case "instance_add":
		return runInstanceAdd(s, clusterID, flags)
	case "instance_list":
		c, err := s.GetCluster(clusterID)
		if err != nil || c == nil {
			fmt.Println("  [错误] 集群不存在:", clusterID)
			return false
		}
		PrintClusterTopology(s, clusterID)
		return false
	case "host_need_add_list", "instance_need_add_list", "cluster_sub_need_cmd":
		printHelp()
		return false
	default:
		fmt.Println("  未知命令，输入 help 查看帮助")
		return false
	}
}

func runClusterAdd(s *store.Store, flags map[string]string) (exit bool) {
	id := flags["id"]
	if id == "" {
		fmt.Println("  用法: cluster add --id=<cluster_id> [--listen=127.0.0.1:9001]")
		return false
	}
	addr := flags["listen"]
	if addr == "" {
		addr = "127.0.0.1:9001"
	}
	c := store.Cluster{
		ID:         id,
		WorkerAddr: addr,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := s.AddCluster(c); err != nil {
		fmt.Println("  [错误] 添加集群失败:", err)
		return false
	}
	fmt.Println("  [成功] 集群已添加:", id)
	PrintClusterList(s)
	return false
}

func runHostAdd(s *store.Store, clusterID string, flags map[string]string) (exit bool) {
	if c, err := s.GetCluster(clusterID); err != nil || c == nil {
		fmt.Println("  [错误] 集群不存在:", clusterID)
		return false
	}
	ip := flags["ip"]
	if ip == "" {
		fmt.Println("  用法: cluster <cluster_id> host add --ip=<ip> [--ssh-user=root] [--ssh-port=22]")
		return false
	}
	user := flags["ssh-user"]
	if user == "" {
		user = "root"
	}
	port := 22
	if p := flags["ssh-port"]; p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	h := store.Host{IP: ip, SSHUser: user, SSHPort: port}
	if err := s.AddHost(clusterID, h); err != nil {
		fmt.Println("  [错误] 添加主机失败:", err)
		return false
	}
	fmt.Println("  [成功] 主机已添加:", ip, "到集群", clusterID)
	PrintClusterTopology(s, clusterID)
	return false
}

func runInstanceAdd(s *store.Store, clusterID string, flags map[string]string) (exit bool) {
	if c, err := s.GetCluster(clusterID); err != nil || c == nil {
		fmt.Println("  [错误] 集群不存在:", clusterID)
		return false
	}
	host := flags["host"]
	portStr := flags["port"]
	if host == "" || portStr == "" {
		fmt.Println("  用法: cluster <cluster_id> instance add --host=<ip> --port=<3306> [--role=master|slave] [--master=<ip:port>]")
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		fmt.Println("  [错误] port 必须为正整数")
		return false
	}
	role := flags["role"]
	if role == "" {
		role = "slave"
	}
	master := flags["master"]
	inst := store.Instance{Host: host, Port: port, Role: role, MasterAddr: master}
	if err := s.AddInstance(clusterID, inst); err != nil {
		fmt.Println("  [错误] 添加实例失败:", err)
		return false
	}
	fmt.Println("  [成功] 实例已添加:", host+":"+portStr, "角色", role, "到集群", clusterID)
	PrintClusterTopology(s, clusterID)
	return false
}

func printHelp() {
	fmt.Println(`
  命令说明（集群与数据纳管）:
    cluster add --id=<集群ID> [--listen=127.0.0.1:9001]  添加集群（添加后立即显示集群列表）
    cluster list                                           列出所有集群
    cluster show <集群ID>                                   显示某集群拓扑（主机+实例）

    cluster <集群ID> host add --ip=<IP> [--ssh-user=root] [--ssh-port=22]  添加主机（添加后立即显示该集群拓扑）
    cluster <集群ID> host list                              列出该集群主机（同拓扑）

    cluster <集群ID> instance add --host=<IP> --port=<3306> [--role=master|slave] [--master=<ip:port>]  添加实例（添加后立即显示拓扑）
    cluster <集群ID> instance list                         列出该集群实例（同拓扑）

    help    显示本帮助
    exit    退出交互
`)
}
