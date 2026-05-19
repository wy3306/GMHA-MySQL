package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"gmha/internal/app"
	machineusecase "gmha/internal/usecase/machine"
)

// MachineCommand 是机器管理的 CLI 命令处理器，负责处理 machine 相关的子命令。
type MachineCommand struct {
	core *app.App
}

// NewMachineCommand 创建一个新的 MachineCommand 实例。
func NewMachineCommand(core *app.App) *MachineCommand {
	return &MachineCommand{core: core}
}

// Run 解析并执行机器管理子命令，支持 onboard、credential-create、credential-list、credential-delete、
// list、update、delete、assign-cluster、collect、collect-static、static-info、dynamic-info、mysql-dynamic-info 等操作。
func (c *MachineCommand) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage())
	}
	switch args[0] {
	case "onboard":
		fs := flag.NewFlagSet("machine onboard", flag.ContinueOnError)
		name := fs.String("name", "", "机器名")
		ip := fs.String("ip", "", "机器 IP")
		sshPort := fs.Int("ssh-port", 22, "SSH 端口")
		sshUser := fs.String("ssh-user", "", "SSH 用户；使用 --credential 时可省略")
		sshPassword := fs.String("ssh-password", "", "SSH 密码；如已完成互信可省略")
		credential := fs.String("credential", "", "SSH 凭证名称或 ID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		resp, err := c.core.MachineService.Onboard(ctx, machineusecase.OnboardMachineRequest{
			Name:           *name,
			IP:             *ip,
			SSHPort:        *sshPort,
			SSHUser:        *sshUser,
			SSHPassword:    *sshPassword,
			CredentialName: *credential,
		})
		if err != nil {
			return err
		}
		return printJSON(resp)
	case "credential-create":
		fs := flag.NewFlagSet("machine credential-create", flag.ContinueOnError)
		name := fs.String("name", "", "凭证名称")
		sshUser := fs.String("ssh-user", "", "SSH 用户")
		sshPassword := fs.String("ssh-password", "", "SSH 密码")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.MachineService.CreateSSHCredential(ctx, *name, *sshUser, *sshPassword)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "credential-list":
		fs := flag.NewFlagSet("machine credential-list", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.MachineService.ListSSHCredentials(ctx)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "credential-delete":
		fs := flag.NewFlagSet("machine credential-delete", flag.ContinueOnError)
		credential := fs.String("credential", "", "SSH 凭证名称或 ID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.core.MachineService.DeleteSSHCredential(ctx, *credential); err != nil {
			return err
		}
		return printJSON(map[string]string{"credential": *credential})
	case "list":
		fs := flag.NewFlagSet("machine list", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.MachineService.ListMachines(ctx)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "update":
		fs := flag.NewFlagSet("machine update", flag.ContinueOnError)
		target := fs.String("target", "", "机器名称或 IP")
		id := fs.String("id", "", "机器 ID；兼容旧命令，建议使用 --target")
		name := fs.String("name", "", "机器名")
		ip := fs.String("ip", "", "机器 IP")
		sshPort := fs.Int("ssh-port", 22, "SSH 端口")
		sshUser := fs.String("ssh-user", "", "SSH 用户")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		machineID, currentIP, err := c.resolveMachineID(ctx, *target, *id)
		if err != nil {
			return err
		}
		if err := c.core.MachineService.UpdateMachine(ctx, machineID, *name, *ip, *sshPort, *sshUser); err != nil {
			return err
		}
		outIP := *ip
		if strings.TrimSpace(outIP) == "" {
			outIP = currentIP
		}
		return printJSON(map[string]string{"ip": outIP})
	case "delete":
		fs := flag.NewFlagSet("machine delete", flag.ContinueOnError)
		target := fs.String("ip", "", "机器 IP 或机器名")
		id := fs.String("id", "", "机器 ID；兼容旧命令，建议使用 --ip")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		machineID, ip, err := c.resolveMachineID(ctx, *target, *id)
		if err != nil {
			return err
		}
		if err := c.core.MachineService.DeleteMachine(ctx, machineID); err != nil {
			return err
		}
		return printJSON(map[string]string{"ip": ip})
	case "assign-cluster":
		fs := flag.NewFlagSet("machine assign-cluster", flag.ContinueOnError)
		target := fs.String("ip", "", "机器 IP 或机器名")
		id := fs.String("id", "", "机器 ID；兼容旧命令，建议使用 --ip")
		cluster := fs.String("cluster", "", "集群名称")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		machineID, ip, err := c.resolveMachineID(ctx, *target, *id)
		if err != nil {
			return err
		}
		if err := c.core.MachineService.AssignMachineCluster(ctx, machineID, *cluster); err != nil {
			return err
		}
		return printJSON(map[string]string{"ip": ip, "cluster": *cluster})
	case "collect":
		fs := flag.NewFlagSet("machine collect", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名称或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		item, err := c.core.MachineService.RefreshMachineInfo(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "collect-static":
		fs := flag.NewFlagSet("machine collect-static", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名称或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()
		item, err := c.core.MachineService.RefreshStaticInfo(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "static-info":
		fs := flag.NewFlagSet("machine static-info", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名称或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.MachineService.GetStaticInfo(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "dynamic-info":
		fs := flag.NewFlagSet("machine dynamic-info", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名称或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.MachineService.GetMachineDynamicMetrics(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "mysql-dynamic-info":
		fs := flag.NewFlagSet("machine mysql-dynamic-info", flag.ContinueOnError)
		machine := fs.String("machine", "", "机器名称或 IP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.MachineService.GetMySQLDynamicMetrics(ctx, *machine)
		if err != nil {
			return err
		}
		return printJSON(item)
	default:
		return fmt.Errorf("%s", usage())
	}
}

// resolveMachineID 根据目标名称或 IP 解析机器 ID，返回机器 ID 和 IP。
func (c *MachineCommand) resolveMachineID(ctx context.Context, target, fallbackID string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		target = strings.TrimSpace(fallbackID)
	}
	if target == "" {
		return "", "", fmt.Errorf("machine ip/name is required")
	}
	items, err := c.core.MachineService.ListMachines(ctx)
	if err != nil {
		return "", "", err
	}
	for _, item := range items {
		if item.IP == target || strings.EqualFold(item.Name, target) || item.ID == target {
			return item.ID, item.IP, nil
		}
	}
	return "", "", fmt.Errorf("machine %s not found", target)
}

// runClusterCreate 执行创建集群操作。
func runClusterCreate(core *app.App, name, description string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := core.MachineService.CreateCluster(ctx, name, description); err != nil {
		return err
	}
	return printJSON(map[string]string{"name": name, "description": description})
}

// runClusterList 执行列出所有集群操作。
func runClusterList(core *app.App) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := core.MachineService.ListClusters(ctx)
	if err != nil {
		return err
	}
	return printJSON(items)
}

// runClusterShow 执行查看指定集群详情操作。
func runClusterShow(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := core.MachineService.ListClusters(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Name == cluster {
			return printJSON(item)
		}
	}
	return fmt.Errorf("cluster %s not found", cluster)
}

// runClusterUpdate 执行更新集群信息操作。
func runClusterUpdate(core *app.App, oldName, newName, description string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := core.MachineService.UpdateCluster(ctx, oldName, newName, description); err != nil {
		return err
	}
	return printJSON(map[string]string{"old_name": oldName, "new_name": newName, "description": description})
}

// runClusterDelete 执行删除集群操作。
func runClusterDelete(core *app.App, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := core.MachineService.DeleteCluster(ctx, name); err != nil {
		return err
	}
	return printJSON(map[string]string{"name": name})
}

// runClusterCleanup 执行一键清理集群操作，会卸载集群机器上的 MySQL 和 Agent。
func runClusterCleanup(core *app.App, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	result, err := core.MachineService.CleanupCluster(ctx, name)
	if err != nil {
		if printErr := printJSON(result); printErr != nil {
			return printErr
		}
		return err
	}
	return printJSON(result)
}

// runVIPScan 执行扫描集群 VIP 操作。
func runVIPScan(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := core.HAService.VIP().Scan(ctx, cluster)
	if err != nil {
		return err
	}
	return printJSON(items)
}

// runVIPAdopt 执行采纳集群 VIP 操作。
func runVIPAdopt(core *app.App, cluster, vip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	item, err := core.HAService.VIP().Adopt(ctx, cluster, vip)
	if err != nil {
		return err
	}
	return printJSON(item)
}

// runVIPValidate 执行验证集群 VIP 配置操作。
func runVIPValidate(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := core.HAService.VIP().Validate(ctx, cluster)
	if err != nil {
		return err
	}
	return printJSON(items)
}

// runVIPStatus 执行查看集群 VIP 状态操作。
func runVIPStatus(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := core.HAService.VIP().Status(ctx, cluster)
	if err != nil {
		return err
	}
	return printJSON(items)
}

// runFailoverPlan 执行生成故障切换计划操作。
func runFailoverPlan(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, err := core.HAService.PlanFailover(ctx, cluster)
	if err != nil {
		return err
	}
	return printJSON(item)
}

// runFailoverStart 执行启动故障切换操作。
func runFailoverStart(core *app.App, cluster string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	item, err := core.HAService.StartFailover(ctx, cluster)
	if err != nil {
		return err
	}
	return printJSON(item)
}

// runFailoverStatus 执行查看故障切换状态操作。
func runFailoverStatus(core *app.App, cluster, failoverID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, ok, err := core.HAService.GetFailover(ctx, cluster, failoverID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("failover %s not found in cluster %s", failoverID, cluster)
	}
	return printJSON(item)
}

// printJSON 将任意值格式化为 JSON 并输出到标准输出。
func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
