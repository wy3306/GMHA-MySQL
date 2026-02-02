package cmdline

import (
	"GMHA-MySQL/internal/service"
	"context"
	"flag"
	"fmt"
	"os"
)

// Run 启动单行命令模式
// args 是除程序名以外的参数
func Run(svc service.ClusterService, args []string) {
	if len(args) < 1 {
		printUsage()
		return
	}

	command := args[0]
	switch command {
	case "create-cluster":
		handleCreateCluster(svc, args[1:])
	case "list-clusters":
		handleListClusters(svc, args[1:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
	}
}

func printUsage() {
	fmt.Println("Usage: gmha [command] [flags]")
	fmt.Println("Commands:")
	fmt.Println("  create-cluster  Create a new cluster")
	fmt.Println("  list-clusters   List all clusters")
}

func handleCreateCluster(svc service.ClusterService, args []string) {
	fs := flag.NewFlagSet("create-cluster", flag.ExitOnError)
	name := fs.String("name", "", "Cluster Name (required)")
	vip := fs.String("vip", "", "Virtual IP")
	desc := fs.String("desc", "", "Description")

	if err := fs.Parse(args); err != nil {
		fmt.Println(err)
		return
	}

	if *name == "" {
		fmt.Println("Error: -name is required")
		fs.Usage()
		return
	}

	input := service.CreateClusterInput{
		Name:        *name,
		Description: *desc,
		VIP:         *vip,
		// 简化起见，CLI 模式暂不支持通过 flag 极其复杂的机器列表输入
		// 实际项目中通常使用 JSON 配置文件或多次调用
	}

	cluster, err := svc.CreateCluster(context.Background(), input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cluster created successfully: %s (ID: %s)\n", cluster.Name, cluster.ID)
}

func handleListClusters(svc service.ClusterService, args []string) {
	clusters, err := svc.ListClusters(context.Background())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	for _, c := range clusters {
		fmt.Printf("%s\t%s\t%s\n", c.ID, c.Name, c.VIP)
	}
}
