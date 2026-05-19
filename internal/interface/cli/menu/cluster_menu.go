package menu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
)

// ClusterMenu 是集群管理的交互式菜单，负责展示集群信息并提供创建、修改、删除、清理等操作入口。
type ClusterMenu struct {
	core *app.App
}

// NewClusterMenu 创建一个新的 ClusterMenu 实例。
func NewClusterMenu(core *app.App) *ClusterMenu {
	return &ClusterMenu{core: core}
}

// Run 运行集群管理菜单的主循环，显示菜单选项并处理用户选择。
func (m *ClusterMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== 集群管理 ====")
		fmt.Println("1. 创建集群")
		fmt.Println("2. 查看集群")
		fmt.Println("3. 修改集群")
		fmt.Println("4. 删除集群")
		fmt.Println("5. 一键集群清理")
		fmt.Println("0. 返回上级")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		switch trim(line) {
		case "1":
			if err := m.Create(reader); err != nil {
				printError(err)
			}
		case "2":
			if err := m.List(); err != nil {
				printError(err)
			}
		case "3":
			if err := m.Update(reader); err != nil {
				printError(err)
			}
		case "4":
			if err := m.Delete(reader); err != nil {
				printError(err)
			}
		case "5":
			if err := m.Cleanup(reader); err != nil {
				printError(err)
			}
		case "0":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// Create 引导用户输入集群信息并创建新集群。
func (m *ClusterMenu) Create(reader *bufio.Reader) error {
	name, err := prompt(reader, "集群名称")
	if err != nil {
		return err
	}
	description, err := promptWithDefault(reader, "集群描述", "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.core.MachineService.CreateCluster(ctx, name, description); err != nil {
		return err
	}
	return printJSON(map[string]string{"name": name, "description": description})
}

// List 显示所有集群列表。
func (m *ClusterMenu) List() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListClusters(ctx)
	if err != nil {
		return err
	}
	printClusterTable(items)
	return nil
}

// Update 引导用户选择集群并修改其信息。
func (m *ClusterMenu) Update(reader *bufio.Reader) error {
	oldName, err := selectClusterName(m.core, reader, "选择原集群序号/名称")
	if err != nil {
		return err
	}
	newName, err := prompt(reader, "新集群名称")
	if err != nil {
		return err
	}
	description, err := promptWithDefault(reader, "集群描述", "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.core.MachineService.UpdateCluster(ctx, oldName, newName, description); err != nil {
		return err
	}
	return printJSON(map[string]string{"old_name": oldName, "new_name": newName, "description": description})
}

// Delete 引导用户选择集群并执行删除操作。
func (m *ClusterMenu) Delete(reader *bufio.Reader) error {
	name, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return err
	}
	ok, err := confirmYES(reader, fmt.Sprintf("确认删除集群 %s", name))
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !ok {
		fmt.Println("已取消删除。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.core.MachineService.DeleteCluster(ctx, name); err != nil {
		return err
	}
	return printJSON(map[string]string{"name": name})
}

// Cleanup 引导用户选择集群并执行一键清理操作，会卸载集群机器上的 MySQL 和 Agent。
func (m *ClusterMenu) Cleanup(reader *bufio.Reader) error {
	name, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return err
	}
	ok, err := confirmYES(reader, fmt.Sprintf("确认一键清理集群 %s 吗？此操作会卸载集群机器上的 MySQL 和 Agent，并清理本地集群、MySQL、Agent 相关记录", name))
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !ok {
		fmt.Println("已取消清理。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	result, err := m.core.MachineService.CleanupCluster(ctx, name)
	printClusterCleanupResult(result)
	return err
}

// printClusterTable 以表格形式打印集群视图列表。
func printClusterTable(items []app.ClusterView) {
	if len(items) == 0 {
		fmt.Println("暂无集群。")
		return
	}
	headers := []string{"名称", "描述", "纳管机器", "创建时间"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.Name,
			emptyAsDash(item.Description),
			joinOrDash(item.Machines),
			item.CreatedAt,
		})
	}
	printAlignedTable(headers, rows)
}

// selectClusterName 展示可用集群列表并引导用户通过序号或名称选择一个集群。
func selectClusterName(core *app.App, reader *bufio.Reader, label string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := core.MachineService.ListClusters(ctx)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", fmt.Errorf("暂无集群")
	}
	fmt.Println("可用集群：")
	for i, item := range items {
		fmt.Printf("%d. %s | 描述=%s | 机器=%s\n", i+1, item.Name, emptyAsDash(item.Description), joinOrDash(item.Machines))
	}
	text, err := prompt(reader, label)
	if err != nil {
		return "", err
	}
	if idx, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		if idx < 1 || idx > len(items) {
			return "", fmt.Errorf("无效集群序号")
		}
		return items[idx-1].Name, nil
	}
	for _, item := range items {
		if item.Name == strings.TrimSpace(text) {
			return item.Name, nil
		}
	}
	return "", fmt.Errorf("未找到集群 %s", text)
}

// printClusterCleanupResult 以表格形式打印集群清理结果。
func printClusterCleanupResult(result app.ClusterCleanupResult) {
	if strings.TrimSpace(result.Cluster) == "" {
		return
	}
	fmt.Printf("集群 %s 清理结果：机器 %d，失败 %d\n", result.Cluster, len(result.Items), result.Failed)
	headers := []string{"机器名", "IP", "MySQL端口", "Agent卸载", "本地清理", "错误"}
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		portTexts := make([]string, 0, len(item.MySQLPorts))
		for _, port := range item.MySQLPorts {
			portTexts = append(portTexts, fmt.Sprintf("%d", port))
		}
		rows = append(rows, []string{
			emptyAsDash(item.Name),
			emptyAsDash(item.IP),
			emptyAsDash(strings.Join(portTexts, ",")),
			strconv.FormatBool(item.AgentUninstalled),
			strconv.FormatBool(item.LocalCleaned),
			emptyAsDash(item.Error),
		})
	}
	printAlignedTable(headers, rows)
}

// joinOrDash 将字符串切片用逗号连接，如果为空则返回短横线。
func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
