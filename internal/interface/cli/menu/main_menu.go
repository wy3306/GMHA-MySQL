package menu

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gmha/internal/app"
)

// MainMenu 是 GMHA CLI 的主菜单，负责展示顶层功能入口并路由到各子菜单。
type MainMenu struct {
	core *app.App
}

// NewMainMenu 创建一个新的 MainMenu 实例。
func NewMainMenu(core *app.App) *MainMenu {
	return &MainMenu{core: core}
}

// Run 运行主菜单的主循环，显示 Manager 控制台、机器管理、集群管理、Agent 状态、任务管理、MySQL 管理、架构搭建等入口。
func (m *MainMenu) Run() error {
	reader := bufio.NewReader(os.Stdin)
	managerMenu := NewManagerMenu(m.core)
	machineMenu := NewMachineMenu(m.core)
	clusterMenu := NewClusterMenu(m.core)
	agentMenu := NewAgentMenu(m.core)
	taskMenu := NewTaskMenu(m.core)
	mysqlMenu := NewMySQLMenu(m.core)
	topologyMenu := NewTopologyMenu(m.core)
	for {
		fmt.Println()
		fmt.Println("==== GMHA 主菜单 ====")
		fmt.Println("1. Manager 控制台")
		fmt.Println("2. 机器管理")
		fmt.Println("3. 集群管理")
		fmt.Println("4. Agent 状态")
		fmt.Println("5. 任务管理")
		fmt.Println("6. MySQL 管理")
		fmt.Println("7. 架构搭建")
		fmt.Println("0. 退出")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		switch strings.TrimSpace(line) {
		case "1":
			if err := managerMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "2":
			if err := machineMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "3":
			if err := clusterMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "4":
			if err := agentMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "5":
			if err := taskMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "6":
			if err := mysqlMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "7":
			if err := topologyMenu.Run(reader); err != nil {
				fmt.Println("错误:", err)
			}
		case "0":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}
