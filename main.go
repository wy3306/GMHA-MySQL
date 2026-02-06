package main

import (
	"GMHA-MySQL/internal/interaction/cmdline"
	"GMHA-MySQL/internal/interaction/web"
	"GMHA-MySQL/internal/interaction/wizard"
	"GMHA-MySQL/internal/model"
	"GMHA-MySQL/internal/store"
	"fmt"
	"os"
)

func main() {
	// 1. 初始化基础设施
	if err := store.InitSQLite("data/gmha.db"); err != nil {
		fmt.Printf("Error initializing DB: %v\n", err)
		os.Exit(1)
	}
	db := store.GetDB()

	// 程序退出时关闭数据库连接
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	if err := model.AutoMigrate(db); err != nil {
		fmt.Printf("Error migrating DB: %v\n", err)
		os.Exit(1)
	}

	// 4. 根据参数选择交互模式
	if len(os.Args) < 2 {
		// 默认模式：引导式交互 (Wizard)
		wizard.Run(db)
		return
	}

	command := os.Args[1]
	switch command {
	case "web":
		// Web 模式: gmha web [port]
		port := 8080 // 默认端口
		web.Run(port)
	case "interactive":
		// 显式调用引导模式
		wizard.Run(db)
	case "help", "--help", "-h":
		printHelp()
	default:
		// 单行命令模式 (CLI): gmha create-cluster ...
		cmdline.Run(os.Args[1:])
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
	fmt.Println("  (no commands available)")
	fmt.Println()
}
