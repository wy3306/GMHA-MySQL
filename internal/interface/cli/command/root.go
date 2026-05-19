package command

import (
	"errors"
	"flag"
	"fmt"

	"gmha/internal/app"
)

// Root 是 CLI 根命令处理器，负责将顶层子命令路由到对应的命令处理器。
type Root struct {
	core *app.App
}

// NewRoot 创建一个新的 Root 命令处理器实例。
func NewRoot(core *app.App) *Root {
	return &Root{core: core}
}

// Run 解析并执行顶层子命令，包括 machine、cluster、vip、failover、mysql、agent、task 等。
func (r *Root) Run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage())
	}

	switch args[0] {
	case "machine":
		return NewMachineCommand(r.core).Run(args[1:])
	case "cluster":
		return r.runCluster(args[1:])
	case "vip":
		return r.runVIP(args[1:])
	case "failover":
		return r.runFailover(args[1:])
	case "mysql":
		return NewMySQLCommand(r.core).Run(args[1:])
	case "agent":
		return NewAgentCommand(r.core).Run(args[1:])
	case "task":
		return NewTaskCommand(r.core).Run(args[1:])
	default:
		return fmt.Errorf("%s", usage())
	}
}

// runCluster 处理 cluster 子命令，支持 create、list、show、update、delete、cleanup 等操作。
func (r *Root) runCluster(args []string) error {
	if len(args) == 0 {
		return errors.New(usage())
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("cluster create", flag.ContinueOnError)
		name := fs.String("name", "", "cluster name")
		description := fs.String("description", "", "cluster description")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterCreate(r.core, *name, *description)
	case "list":
		fs := flag.NewFlagSet("cluster list", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterList(r.core)
	case "show":
		fs := flag.NewFlagSet("cluster show", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterShow(r.core, *cluster)
	case "update":
		fs := flag.NewFlagSet("cluster update", flag.ContinueOnError)
		oldName := fs.String("old-name", "", "原集群名称")
		newName := fs.String("new-name", "", "新集群名称")
		description := fs.String("description", "", "集群描述")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterUpdate(r.core, *oldName, *newName, *description)
	case "delete":
		fs := flag.NewFlagSet("cluster delete", flag.ContinueOnError)
		name := fs.String("name", "", "集群名称")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterDelete(r.core, *name)
	case "cleanup":
		fs := flag.NewFlagSet("cluster cleanup", flag.ContinueOnError)
		name := fs.String("name", "", "集群名称")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runClusterCleanup(r.core, *name)
	default:
		return fmt.Errorf("%s", usage())
	}
}

// runVIP 处理 vip 子命令，支持 scan、adopt、validate、status 等操作。
func (r *Root) runVIP(args []string) error {
	if len(args) == 0 {
		return errors.New(usage())
	}
	switch args[0] {
	case "scan":
		fs := flag.NewFlagSet("vip scan", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runVIPScan(r.core, *cluster)
	case "adopt":
		fs := flag.NewFlagSet("vip adopt", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		vip := fs.String("vip", "", "VIP address")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runVIPAdopt(r.core, *cluster, *vip)
	case "validate":
		fs := flag.NewFlagSet("vip validate", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runVIPValidate(r.core, *cluster)
	case "status":
		fs := flag.NewFlagSet("vip status", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runVIPStatus(r.core, *cluster)
	default:
		return fmt.Errorf("%s", usage())
	}
}

// runFailover 处理 failover 子命令，支持 plan、start、status 等操作。
func (r *Root) runFailover(args []string) error {
	if len(args) == 0 {
		return errors.New(usage())
	}
	switch args[0] {
	case "plan":
		fs := flag.NewFlagSet("failover plan", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runFailoverPlan(r.core, *cluster)
	case "start":
		fs := flag.NewFlagSet("failover start", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runFailoverStart(r.core, *cluster)
	case "status":
		fs := flag.NewFlagSet("failover status", flag.ContinueOnError)
		cluster := fs.String("cluster", "", "cluster id/name")
		failoverID := fs.String("failover-id", "", "failover id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return runFailoverStatus(r.core, *cluster, *failoverID)
	default:
		return fmt.Errorf("%s", usage())
	}
}

// usage 返回 CLI 命令的使用说明文本。
func usage() string {
	return `用法:
  gmha
  gmha start cli
  gmha start web --listen :8080 --db ./data/manager.db
  gmha machine credential-create --name root-cred --ssh-user root --ssh-password secret
  gmha machine credential-list
  gmha machine credential-delete --credential root-cred
  gmha machine onboard --name db-01 --ip 10.0.0.11 --ssh-port 22 --credential root-cred
  gmha machine onboard --name db-01 --ip 10.0.0.11 --ssh-port 22 --ssh-user root [--ssh-password secret]
  gmha machine list
  gmha machine update --target 10.0.0.11 --name db-01 --ip 10.0.0.11 --ssh-port 22 --ssh-user root
  gmha machine delete --ip 10.0.0.11
  gmha machine assign-cluster --ip 10.0.0.11 --cluster prod-a
  gmha machine collect --machine db-01
  gmha machine collect-static --machine db-01
  gmha machine static-info --machine db-01
  gmha machine dynamic-info --machine db-01
  gmha machine mysql-dynamic-info --machine db-01
  gmha mysql install --machine db-01 --port 3306 --root-password secret --profile prod
  gmha mysql install-cluster --cluster prod-a --port 3306 --root-password secret --profile prod
  gmha mysql uninstall --machine db-01 --port 3306
  gmha mysql forget --machine db-01 --port 3306
  gmha mysql list
  gmha mysql static-info --machine db-01
  gmha mysql dynamic-info --machine db-01
  gmha cluster create --name prod-a [--description "业务集群"]
  gmha cluster list
  gmha cluster show --cluster prod-a
  gmha cluster update --old-name prod-a --new-name prod-b [--description "新描述"]
  gmha cluster delete --name prod-a
  gmha cluster cleanup --name prod-a
  gmha vip scan --cluster prod-a
  gmha vip adopt --cluster prod-a --vip 10.0.0.100
  gmha vip validate --cluster prod-a
  gmha vip status --cluster prod-a
  gmha failover plan --cluster prod-a
  gmha failover start --cluster prod-a
  gmha failover status --cluster prod-a --failover-id fo-xxx
  gmha agent list
  gmha agent pending
  gmha agent retry-install --ip 10.0.0.11 [--install-dir /home/gmha/agent]
  gmha agent upgrade --ip 10.0.0.11
  gmha agent uninstall --ip 10.0.0.11
  gmha agent recovery-list
  gmha agent recover --ip 10.0.0.11
  gmha task exec --machine db-01 --command "hostname"
  gmha task list
  gmha task get --id task-xxx
  gmha serve --listen :8080 --db ./data/manager.db`
}
