package cli

import (
	"GMHA-MySQL/internal/store"
)

// RunOneLiner 处理单行命令模式
func RunOneLiner(s *store.Store, args []string) {
	cmd, clusterID, flags := ParseArgs(args)
	if cmd == "help" || cmd == "" {
		// 这里可以调用更详细的 One-Line 帮助
		printHelp()
		return
	}

	if cmd == "exit" || cmd == "quit" {
		return
	}

	// 复用 Execute 逻辑
	// 注意：Execute 内部如果返回 true 表示 exit，这里对于 One-Liner 来说执行完就是退出了
	Execute(s, cmd, clusterID, flags)

	// 如果需要处理 Execute 的错误返回，可以在 Execute 中增强返回类型，目前保持简单
}
