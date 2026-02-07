package wizard

import (
	"GMHA-MySQL/internal/model"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"gorm.io/gorm"
)

// 主菜单
func showMainMenu() AppState {
	//定义主菜单列表
	menuItems := []MenuItem{
		{Label: "1.集群信息查看", Value: "1", Hotkey: "1"},
		{Label: "2.集群基本信息管理", Value: "2", Hotkey: "2"},
		{Label: "3.机器纳管", Value: "3", Hotkey: "3"},
		{Label: "4.VIP管理", Value: "4", Hotkey: "4"},
		{Label: "5.一键部署数据库", Value: "5", Hotkey: "5"},
		{Label: "6.数据库主从切换", Value: "6", Hotkey: "6"},
		{Label: "7.数据库备份", Value: "7", Hotkey: "7"},
		{Label: "8.数据库主从修复", Value: "8", Hotkey: "8"},
		{Label: "9.一键巡检", Value: "9", Hotkey: "9"},
		{Label: "10.告警对接", Value: "10", Hotkey: "10"},
	}
	// 初始化菜单模型
	menuModel := MenuModel{
		Title:  "主菜单",
		Items:  menuItems,
		Cursor: 0,
	}

	// 运行
	p := tea.NewProgram(menuModel)
	m, err := p.Run()
	if err != nil {
		fmt.Printf("Error running menu: %v", err) // 打印错误
		return StateExit                          // 出错则退出
	}

	//获取用户选择并返回
	if finalModel, ok := m.(MenuModel); ok {
		if finalModel.Selected == "1" {
			return StateShowClusterInfo
		} else if finalModel.Selected == "2" {
			return StateClusterInfoManage
		} else if finalModel.Selected == "3" {
			return StateMachineManage
		} else if finalModel.Selected == "4" {
			return StateVIPManage
		} else if finalModel.Selected == "5" {
			return StateDeployDB
		} else if finalModel.Selected == "6" {
			return StateSwitchDB
		} else if finalModel.Selected == "7" {
			return StateBackupDB
		} else if finalModel.Selected == "8" {
			return StateFixDB
		} else if finalModel.Selected == "9" {
			return StateInspectDB
		} else if finalModel.Selected == "10" {
			return StateAlertDock
		} else if finalModel.Selected == "q" {
			return StateExit
		} else {
			fmt.Println("无效选择，请重新输入") // 默认退出
		}
	}
	return StateExit // 默认退出

}

// 集群信息查看
func showClusterInfo(db *gorm.DB) AppState {
	var cluster model.Cluster
	if err := db.First(&cluster).Error; err != nil {
		fmt.Println("获取集群信息失败:", err)
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
	return StateMainMenu
}

// 集群基本信息管理
func manageClusterInfo() AppState {
	fmt.Println("集群基本信息管理")
	return StateExit
}

// 机器纳管
func manageMachines() AppState {
	fmt.Println("机器纳管")
	return StateExit
}

// VIP管理
func manageVIPs() AppState {
	fmt.Println("VIP管理")
	return StateExit
}

// 一键部署数据库
func deployDB() AppState {
	fmt.Println("一键部署数据库")
	return StateExit
}

// 数据库主从切换
func switchDB() AppState {
	fmt.Println("数据库主从切换")
	return StateExit
}

// 数据库备份
func backupDB() AppState {
	fmt.Println("数据库备份")
	return StateExit
}

// 数据库主从修复
func fixDB() AppState {
	fmt.Println("数据库主从修复")
	return StateExit
}

// 一键巡检
func inspectDB() AppState {
	fmt.Println("一键巡检")
	return StateExit
}

// 告警对接
func alertDock() AppState {
	fmt.Println("告警对接")
	return StateExit
}
