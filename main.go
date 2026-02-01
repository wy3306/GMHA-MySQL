package main

import (
	"GMHA-MySQL/cmd"
	"GMHA-MySQL/internal/cli"
	"GMHA-MySQL/internal/store"
	"GMHA-MySQL/logger"
	"os"
)

func main() {
	if err := logger.Init(logger.Config{
		ClusterID:  "cli",
		LogDir:     "log",
		LogFile:    "app.log",
		Level:      "info",
		Console:    true,
		MaxSize:    100,
		MaxAge:     28,
		MaxBackups: 10,
		Compress:   true,
	}); err != nil {
		panic(err)
	}

	storePath := "data/gmha.db"
	s, err := store.New(storePath)
	if err != nil {
		panic(err)
	}
	defer s.Close()

	// 简单的参数判断：如果有参数，则进入 One-Liner 模式；否则进入交互模式
	if len(os.Args) > 1 {
		// os.Args[0] 是程序名，os.Args[1:] 是真正的参数
		cli.RunOneLiner(s, os.Args[1:])
	} else {
		cmd.RunInteractive(s)
	}
}
