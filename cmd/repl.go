package cmd

import (
	"GMHA-MySQL/internal/cli"
	"GMHA-MySQL/internal/store"
	"bufio"
	"fmt"
	"os"
	"strings"
)

const prompt = "GMHA> "

// RunInteractive 进入交互循环：读命令 -> 解析 -> 执行 -> 添加类命令后立即显示拓扑 -> 循环直到 exit
func RunInteractive(s *store.Store) {

	// 初始化交互信息
	fmt.Println("Golang Master High Availability Manager and tools for MySQL")
	fmt.Println("输入 help 查看命令，exit 退出。")
	fmt.Println()
	// 读取输入
	scanner := bufio.NewScanner(os.Stdin)

	// 进入交互循环
	for {
		fmt.Println("1. 显示所有集群")
		fmt.Println("2. 管理集群")
		fmt.Println("3. 监控纳管")
		fmt.Println("4. 备份管理")
		fmt.Println("quit,exit 退出")

		//打印提示符，等待用户输入
		fmt.Print(prompt)

		//读取用户输入
		if !scanner.Scan() {
			break
		}
		//解析用户输入的命令
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "1":
			cli.PrintClusterList(s)
		case "2":
			ManagerCluster(s)
		case "3":
			fmt.Println("监控纳管")
		case "4":
			fmt.Println("备份管理")
		case "quit", "exit":
			fmt.Println("退出程序")
			return
		default:
			fmt.Println("未知命令")
		}
	}
	// 处理可能的读取错误
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "读取输入错误:", err)
	}
}

func ManagerCluster(s *store.Store) {
	scanner := bufio.NewScanner(os.Stdin)
	clusetprompt := "集群管理> "

	for {

		fmt.Print(clusetprompt)
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())

		switch line {
		case "1":
			fmt.Println("显示所有集群")
		case "2":
			fmt.Println("创建集群")
			cmd, clusterID, flags := cli.ParseLine(line)
			if cli.Execute(s, cmd, clusterID, flags) {
				fmt.Println("命令执行完成")
			}
		case "3":
			fmt.Println("删除集群")
		case "4":
			fmt.Println("修改集群")
		case "5":
			fmt.Println("查看集群信息")
		case "6":
			ControlCluster(s)
		case "7":
			fmt.Println("根据配置文件建立新集群")
		case "8":
			fmt.Println("查看默认数据库版本")
		case "back":
			fmt.Println("返回主菜单")
			return
		case "exit", "quit":
			fmt.Println("退出程序")
			os.Exit(0)
		default:
			fmt.Println("未知命令")
		}
	}
}
func ControlCluster(s *store.Store) {
	scanner := bufio.NewScanner(os.Stdin)
	controlprompt := "集群控制管理> "
	for {
		fmt.Print(controlprompt)
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())

		switch line {
		case "1":
			fmt.Println("启动集群")
		case "2":
			fmt.Println("停止集群")
		case "3":
			fmt.Println("重启集群")
		case "4":
			fmt.Println("修改VIP地址")
		case "5":
			fmt.Println("集群监控管理")
		case "6":
			fmt.Println("集群添加机器")
		case "7":
			fmt.Println("集群删除机器")
		case "10":
			fmt.Println("切换主从")
		case "back":
			fmt.Println("返回主菜单")
			return
		case "exit", "quit":
			fmt.Println("退出程序")
			os.Exit(0)
		default:
			fmt.Println("未知命令")
		}
	}
}
