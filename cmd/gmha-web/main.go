// Command gmha-web starts the bootstrap page used to launch the full Manager.
package main

import (
	"flag"
	"fmt"
	"os"

	"gmha/internal/launcher"
)

func main() {
	cfg := launcher.Config{}
	flag.StringVar(&cfg.Listen, "listen", "127.0.0.1:8079", "启动页监听地址")
	flag.StringVar(&cfg.ManagerURL, "manager-url", "auto", "浏览器访问 Manager 的地址；auto 表示沿用当前主机名和 Manager 端口")
	flag.StringVar(&cfg.ManagerListen, "manager-listen", ":8080", "Manager HTTP 监听地址")
	flag.StringVar(&cfg.ManagerGRPCListen, "manager-grpc-listen", ":9100", "Manager gRPC 监听地址")
	flag.StringVar(&cfg.ManagerPublicKey, "manager-pubkey", "", "Manager SSH 公钥路径；对应私钥将用于验证现有互信")
	flag.StringVar(&cfg.ManagerBinary, "manager-binary", "", "gmha 程序路径，默认使用启动器同目录的 gmha")
	flag.StringVar(&cfg.DataPath, "db", "", "SQLite 数据库路径，默认使用启动器同目录的 data/manager.db")
	flag.StringVar(&cfg.AgentBinary, "agent-binary", "", "Agent 程序路径，默认使用启动器同目录的 bin/agentd")
	flag.StringVar(&cfg.LogPath, "log", "", "Manager 日志路径，默认使用启动器同目录的 logs/manager.log")
	flag.BoolVar(&cfg.OpenBrowser, "open-browser", true, "启动后自动打开浏览器")
	flag.Parse()

	controller, err := launcher.NewController(cfg)
	if err == nil {
		err = controller.Serve()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
