package wizard

// AppState 定义应用程序的状态枚举
type AppState int

const (
	// 主菜单
	StateMainMenu AppState = iota
	// 显示集群信息
	StateShowClusterInfo = 1
	// 集群基本信息管理
	StateClusterInfoManage = 2
	// 机器纳管
	StateMachineManage = 3
	// VIP管理
	StateVIPManage = 4
	// 一键部署数据库
	StateDeployDB = 5
	// 数据库主从切换
	StateSwitchDB = 6
	// 数据库备份
	StateBackupDB = 7
	// 数据库主从修复
	StateFixDB = 8
	// 一键巡检
	StateInspectDB = 9
	// 告警对接
	StateAlertDock = 10
	// 退出
	StateExit = 11
)
