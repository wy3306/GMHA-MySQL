// Package command 提供 GMHA CLI 子命令的实现，负责将命令行参数解析并路由到对应的应用服务。
package command

import (
	"context"
	"flag"
	"fmt"
	"time"

	"gmha/internal/app"
	agentusecase "gmha/internal/usecase/agent"
)

// AgentCommand 是 Agent 管理的 CLI 命令处理器，负责处理 agent 相关的子命令。
type AgentCommand struct {
	core *app.App
}

// NewAgentCommand 创建一个新的 AgentCommand 实例。
func NewAgentCommand(core *app.App) *AgentCommand {
	return &AgentCommand{core: core}
}

// Run 解析并执行 Agent 子命令，支持 list、pending、retry-install、uninstall、upgrade、recovery-list、recover 等操作。
func (c *AgentCommand) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage())
	}
	switch args[0] {
	case "list":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.AgentService.ListViews(ctx)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "pending":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.AgentService.ListInstallCandidates(ctx)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "retry-install":
		fs := flag.NewFlagSet("agent retry-install", flag.ContinueOnError)
		ip := fs.String("ip", "", "machine ip")
		installDir := fs.String("install-dir", "", "agent install dir")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resp, err := c.core.AgentService.RetryInstallByIP(ctx, agentusecase.InstallAgentRequest{
			IP:         *ip,
			InstallDir: *installDir,
		})
		if err != nil {
			return err
		}
		return printJSON(resp)
	case "uninstall":
		fs := flag.NewFlagSet("agent uninstall", flag.ContinueOnError)
		ip := fs.String("ip", "", "machine ip")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resp, err := c.core.AgentService.UninstallByIP(ctx, *ip)
		if err != nil {
			return err
		}
		return printJSON(resp)
	case "upgrade":
		fs := flag.NewFlagSet("agent upgrade", flag.ContinueOnError)
		ip := fs.String("ip", "", "machine ip")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		resp, err := c.core.AgentService.UpgradeByIP(ctx, *ip)
		if err != nil {
			return err
		}
		return printJSON(resp)
	case "recovery-list":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.RecoveryService.ListRecent(ctx, 20)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "recover":
		fs := flag.NewFlagSet("agent recover", flag.ContinueOnError)
		ip := fs.String("ip", "", "machine ip")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		resp, err := c.core.RecoveryService.TriggerManualRecoverByIP(ctx, *ip)
		if err != nil {
			return err
		}
		return printJSON(resp)
	default:
		return fmt.Errorf("%s", usage())
	}
}
