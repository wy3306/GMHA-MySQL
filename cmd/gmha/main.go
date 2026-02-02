package main

import (
	"GMHA-MySQL/internal/interaction/cmdline"
	"GMHA-MySQL/internal/interaction/web"
	"GMHA-MySQL/internal/interaction/wizard"
	"GMHA-MySQL/internal/model"
	"GMHA-MySQL/internal/service"
	"GMHA-MySQL/internal/store"
	"fmt"
	"os"
)

func main() {
	// 1. 初始化基础设施
	if err := store.InitSQLite("gmha.db"); err != nil {
		fmt.Printf("Error initializing DB: %v\n", err)
		os.Exit(1)
	}
	db := store.GetDB()

	// 2. 自动迁移 (确保表存在)
	if err := model.AutoMigrate(db); err != nil {
		fmt.Printf("Error migrating DB: %v\n", err)
		os.Exit(1)
	}

	// 3. 初始化内核 (Kernel/Service)
	clusterRepo := model.NewClusterRepository(db)
	machineRepo := model.NewMachineRepository(db)
	svc := service.NewClusterService(clusterRepo, machineRepo, db)

	// 4. 根据参数选择交互模式
	if len(os.Args) < 2 {
		// 默认模式：引导式交互 (Wizard)
		wizard.Run(svc)
		return
	}

	command := os.Args[1]
	switch command {
	case "web":
		// Web 模式: gmha web [port]
		port := 8080 // 默认端口
		// 简单解析一下端口参数，如果有的话
		// 实际项目中建议使用 flag 解析
		web.Run(svc, port)
	case "interactive":
		// 显式调用引导模式
		wizard.Run(svc)
	case "help", "--help", "-h":
		printHelp()
	default:
		// 单行命令模式 (CLI): gmha create-cluster ...
		// 将除去程序名的所有参数传给 cmdline 处理器
		// os.Args[1:] 包含了 command 和它的 flags
		cmdline.Run(svc, os.Args[1:])
	}
}

func printHelp() {
	fmt.Println("GMHA-MySQL Management Tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gmha [command] [flags]")
	fmt.Println()
	fmt.Println("Modes:")
	fmt.Println("  (no args)      Run in Interactive Wizard mode (Default)")
	fmt.Println("  interactive    Run in Interactive Wizard mode")
	fmt.Println("  web            Start Web API Server")
	fmt.Println()
	fmt.Println("Commands (Single Line Mode):")
	fmt.Println("  create-cluster -name <name> ...")
	fmt.Println("  list-clusters")
	fmt.Println()
}
