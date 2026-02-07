package wizard

import (
	"fmt"
	"os"

	"gorm.io/gorm"
)

func Enter(db *gorm.DB) {
	state := StateMainMenu
	for {
		switch state {
		case StateMainMenu:
			state = showMainMenu() // 显示主菜单
		case StateShowClusterInfo:
			state = showClusterInfo(db) // 显示集群信息
		case StateClusterInfoManage:
			state = manageClusterInfo() // 管理集群信息
		case StateMachineManage:
			state = manageMachines() // 管理机器
		case StateVIPManage:
			state = manageVIPs() // 管理VIP
		case StateDeployDB:
			state = deployDB() // 一键部署数据库
		case StateSwitchDB:
			state = switchDB() // 数据库主从切换
		case StateBackupDB:
			state = backupDB() // 数据库备份
		case StateFixDB:
			state = fixDB() // 数据库主从修复
		case StateInspectDB:
			state = inspectDB() // 一键巡检
		case StateAlertDock:
			state = alertDock() // 告警对接
		case StateExit:
			os.Exit(0)
			fmt.Println("退出成功")
			return
		default:
			fmt.Println("无效输入")
			state = StateMainMenu
		}
	}

}
