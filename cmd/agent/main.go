// Package main 是 GMHA Agent 端的入口程序。
// Agent (agentd) 是部署在每台被纳管机器上的守护进程，负责：
//   - 通过 gRPC 双向流与 Manager 保持心跳连接
//   - 接收并执行 Manager 下发的任务（命令执行、信息采集、MySQL 安装/卸载等）
//   - 采集主机和 MySQL 动态指标并上报给 Manager
//   - 执行 Agent 自检确保自身健康运行
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"gmha/internal/agent"
)

// main 是 Agent 程序的入口函数。
// 启动流程：
//   1. 解析命令行参数，获取配置文件路径（默认 /home/gmha/agent/agent.yaml）
//   2. 加载 Agent 配置（包含 agent_id、machine_id、Manager 地址等）
//   3. 创建可被信号中断的 Context（SIGINT/SIGTERM）
//   4. 调用 agent.Run 启动 Agent 主循环
func main() {
	configPath := flag.String("config", "/home/gmha/agent/agent.yaml", "agent config path")
	flag.Parse()

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}
