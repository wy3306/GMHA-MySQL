package menu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"time"

	"gmha/internal/app"
)

// ManagerMenu 是 Manager 控制台的交互式菜单，负责 Manager 服务的启动、停止、重启和配置管理。
type ManagerMenu struct {
	core *app.App
}

// NewManagerMenu 创建一个新的 ManagerMenu 实例。
func NewManagerMenu(core *app.App) *ManagerMenu {
	return &ManagerMenu{core: core}
}

// Run 运行 Manager 控制台菜单的主循环，显示菜单选项并处理用户选择。
func (m *ManagerMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== Manager 控制台 ====")
		fmt.Println("1. 查看运行状态")
		fmt.Println("2. 启动 Manager 服务")
		fmt.Println("3. 重启 Manager 服务")
		fmt.Println("4. 停止 Manager 服务")
		fmt.Println("5. 修改启动参数")
		fmt.Println("0. 返回")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch trim(line) {
		case "1":
			if err := m.ShowStatus(); err != nil {
				printError(err)
			}
		case "2":
			if err := m.Start(reader); err != nil {
				printError(err)
			}
		case "3":
			if err := m.Restart(reader); err != nil {
				printError(err)
			}
		case "4":
			if err := m.Stop(reader); err != nil {
				printError(err)
			}
		case "5":
			if err := m.UpdateConfig(reader); err != nil {
				printError(err)
			}
		case "0", "esc", "ESC":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// ShowStatus 显示 Manager 服务的当前运行状态。
func (m *ManagerMenu) ShowStatus() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := m.core.ManagerRuntime.DescribeStatus(ctx)
	if err != nil {
		return err
	}
	headers := []string{"项目", "值"}
	rows := make([][]string, 0, len(status))
	order := []string{"运行状态", "PID", "HTTP监听", "gRPC监听", "HTTP地址", "gRPC地址", "数据库", "Agent二进制", "日志文件"}
	for _, key := range order {
		rows = append(rows, []string{key, status[key]})
	}
	printAlignedTable(headers, rows)
	return nil
}

// Start 引导用户配置启动参数并启动 Manager 服务。
func (m *ManagerMenu) Start(reader *bufio.Reader) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, err := m.core.ManagerRuntime.GetStatus(ctx)
	if err != nil {
		return err
	}
	cfg, err := m.promptConfig(reader, current.Config)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	status, err := m.core.ManagerRuntime.Start(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Manager 已启动，PID=%d\n", status.PID)
	return m.ShowStatus()
}

// Stop 确认后停止 Manager 服务。
func (m *ManagerMenu) Stop(reader *bufio.Reader) error {
	confirm, err := confirmYES(reader, "确认停止 Manager")
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("已取消停止。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.core.ManagerRuntime.Stop(ctx); err != nil {
		return err
	}
	fmt.Println("Manager 已停止。")
	return nil
}

// Restart 确认后重启 Manager 服务。
func (m *ManagerMenu) Restart(reader *bufio.Reader) error {
	confirm, err := confirmYES(reader, "确认重启 Manager")
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("已取消重启。")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	current, err := m.core.ManagerRuntime.GetStatus(ctx)
	if err != nil {
		return err
	}
	cfg := current.Config
	if cfg == (app.ManagerRuntimeConfig{}) {
		cfg = app.ManagerRuntimeConfig{}
	}
	status, err := m.core.ManagerRuntime.Restart(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Manager 已重启，PID=%d\n", status.PID)
	return m.ShowStatus()
}

// UpdateConfig 引导用户修改 Manager 启动参数并保存配置。
func (m *ManagerMenu) UpdateConfig(reader *bufio.Reader) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, err := m.core.ManagerRuntime.GetStatus(ctx)
	if err != nil {
		return err
	}
	cfg, err := m.promptConfig(reader, current.Config)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if err := m.core.ManagerRuntime.SaveConfig(ctx, cfg); err != nil {
		return err
	}
	fmt.Println("Manager 启动参数已保存。")
	return m.ShowStatus()
}

// promptConfig 引导用户逐项输入 Manager 启动配置参数。
func (m *ManagerMenu) promptConfig(reader *bufio.Reader, cfg app.ManagerRuntimeConfig) (app.ManagerRuntimeConfig, error) {
	var err error
	host, httpPort := app.SplitManagerHTTPAddr(cfg.ManagerHTTPAddr)
	_, grpcPort := app.SplitManagerGRPCAddr(cfg.ManagerGRPCAddr)
	if cfg.ListenHTTP, err = promptMenuWithDefault(reader, "HTTP 监听地址", cfg.ListenHTTP); err != nil {
		return cfg, err
	}
	if cfg.ListenGRPC, err = promptMenuWithDefault(reader, "gRPC 监听地址", cfg.ListenGRPC); err != nil {
		return cfg, err
	}
	if host, err = promptMenuWithDefault(reader, "Manager 主机 IP", host); err != nil {
		return cfg, err
	}
	if httpPort, err = promptMenuWithDefault(reader, "Manager HTTP 端口", httpPort); err != nil {
		return cfg, err
	}
	if grpcPort, err = promptMenuWithDefault(reader, "Manager gRPC 端口", grpcPort); err != nil {
		return cfg, err
	}
	if cfg.DBPath, err = promptMenuWithDefault(reader, "数据库路径", cfg.DBPath); err != nil {
		return cfg, err
	}
	if cfg.AgentBinaryPath, err = promptMenuWithDefault(reader, "Agent 二进制路径", cfg.AgentBinaryPath); err != nil {
		return cfg, err
	}
	if cfg.ManagerPublicKey, err = promptMenuWithDefault(reader, "Manager SSH 公钥路径", cfg.ManagerPublicKey); err != nil {
		return cfg, err
	}
	cfg.ManagerHTTPAddr = app.BuildManagerHTTPAddr(host, httpPort)
	cfg.ManagerGRPCAddr = app.BuildManagerGRPCAddr(host, grpcPort)
	return cfg, nil
}
