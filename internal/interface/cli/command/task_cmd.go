package command

import (
	"context"
	"flag"
	"fmt"
	"time"

	"gmha/internal/app"
)

// TaskCommand 是任务管理的 CLI 命令处理器，负责处理 task 相关的子命令。
type TaskCommand struct {
	core *app.App
}

// NewTaskCommand 创建一个新的 TaskCommand 实例。
func NewTaskCommand(core *app.App) *TaskCommand {
	return &TaskCommand{core: core}
}

// Run 解析并执行任务管理子命令，支持 exec、list、get 等操作。
func (c *TaskCommand) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage())
	}
	switch args[0] {
	case "exec":
		fs := flag.NewFlagSet("task exec", flag.ContinueOnError)
		machine := fs.String("machine", "", "machine name or ip")
		command := fs.String("command", "", "command to execute")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.TaskService.CreateExecTask(ctx, *machine, *command)
		if err != nil {
			return err
		}
		return printJSON(item)
	case "list":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items, err := c.core.TaskService.ListTasks(ctx, 50)
		if err != nil {
			return err
		}
		return printJSON(items)
	case "get":
		fs := flag.NewFlagSet("task get", flag.ContinueOnError)
		id := fs.String("id", "", "task id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		item, err := c.core.TaskService.GetTaskDetail(ctx, *id)
		if err != nil {
			return err
		}
		return printJSON(item)
	default:
		return fmt.Errorf("%s", usage())
	}
}
