package wizard

import (
	"GMHA-MySQL/internal/intercore"
	"GMHA-MySQL/internal/terminal_utils"
	"fmt"

	"gorm.io/gorm"
)

func Run(mds *gorm.DB) {
	prompter := terminal_utils.NewPrompter()
	prompter.PrintHeader("Manager")
	// mds := store.GetDB() // 如果这里需要使用 DB，直接获取即可，不需要 Close

	for {
		fmt.Println("1.集群信息查看")
		fmt.Println("2.集群基本信息管理")
		fmt.Println("3.机器纳管")
		fmt.Println("4.VIP管理")
		fmt.Println("5.一键部署数据库")
		fmt.Println("6.数据库主从切换")
		fmt.Println("7.数据库备份")
		fmt.Println("8.数据库主从修复")
		fmt.Println("9.一键巡检")
		fmt.Println("10.告警对接")
		fmt.Println("q.退出")

		choice := prompter.Ask("请输入您的选择", "q")

		switch choice {
		case "q", "quit", "exit":
			fmt.Println("退出成功")
			return
		case "1":
			intercore.ShowClusterInfo(mds)
		default:
			fmt.Println("无效输入")
		}
	}
}
