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
	agentusecase "gmha/internal/usecase/agent"
)

// AgentMenu 是 Agent 管理的交互式菜单，负责展示 Agent 状态并提供安装、卸载、升级、恢复等操作入口。
type AgentMenu struct {
	core *app.App
}

// NewAgentMenu 创建一个新的 AgentMenu 实例。
func NewAgentMenu(core *app.App) *AgentMenu {
	return &AgentMenu{core: core}
}

// Run 运行 Agent 管理菜单的主循环，显示菜单选项并处理用户选择。
func (m *AgentMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== Agent 状态 ====")
		fmt.Println("1. 查看所有 Agent 状态")
		fmt.Println("2. 查看未安装 Agent 的集群机器")
		fmt.Println("3. 查看安装错误")
		fmt.Println("4. 重试安装 Agent")
		fmt.Println("5. 查看某台机器 Agent 状态")
		fmt.Println("6. 卸载 Agent")
		fmt.Println("7. 重启 Agent")
		fmt.Println("8. 查看恢复任务")
		fmt.Println("9. 手动拉起 Agent")
		fmt.Println("10. 升级 Agent")
		fmt.Println("11. 修复 MySQL 采集配置 (mysql-heartbeat.json)")
		fmt.Println("0. 返回")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch trim(line) {
		case "1":
			if err := m.List(); err != nil {
				printError(err)
			}
		case "2":
			if err := m.ListPending(); err != nil {
				printError(err)
			}
		case "3":
			if err := m.ListErrors(); err != nil {
				printError(err)
			}
		case "4":
			if err := m.Retry(reader); err != nil {
				printError(err)
			}
		case "5":
			if err := m.GetByMachine(reader); err != nil {
				printError(err)
			}
		case "6":
			if err := m.Uninstall(reader); err != nil {
				printError(err)
			}
		case "7":
			fmt.Println("重启 Agent 将调用同一套 AgentService，当前为骨架占位。")
		case "8":
			if err := m.ListRecoveryTasks(); err != nil {
				printError(err)
			}
		case "9":
			if err := m.TriggerRecovery(reader); err != nil {
				printError(err)
			}
		case "10":
			if err := m.Upgrade(reader); err != nil {
				printError(err)
			}
		case "11":
			if err := m.RepairMySQLConfig(reader); err != nil {
				printError(err)
			}
		case "0":
			return nil
		case "esc", "ESC":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// List 显示所有 Agent 的状态列表。
func (m *AgentMenu) List() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.AgentService.ListViews(ctx)
	if err != nil {
		return err
	}
	printAgentTable(items, "暂无 Agent 信息。", true)
	return nil
}

// ListPending 显示已纳入集群但未安装或未在线的 Agent 机器列表。
func (m *AgentMenu) ListPending() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.AgentService.ListInstallCandidates(ctx)
	if err != nil {
		return err
	}
	printAgentTable(items, "暂无已纳入集群但未安装或未在线的 Agent 机器。", true)
	return nil
}

// GetByMachine 根据用户选择的机器查看该机器的 Agent 状态和最近恢复任务。
func (m *AgentMenu) GetByMachine(reader *bufio.Reader) error {
	selected, err := NewMachineMenu(m.core).selectManagedMachineByIP(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	ip := selected.IP
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, ok, err := m.core.AgentService.GetViewByIP(ctx, ip)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("未找到 Agent")
		return nil
	}
	fmt.Println()
	fmt.Println("当前 Agent 状态：")
	if err := printJSON(item); err != nil {
		return err
	}
	if m.core.RecoveryService == nil {
		return nil
	}

	recoveryItems, err := m.core.RecoveryService.ListRecent(ctx, 20)
	if err != nil {
		return err
	}
	filtered := make([]app.RecoveryView, 0)
	for _, recovery := range recoveryItems {
		if recovery.MachineIP == ip {
			filtered = append(filtered, recovery)
		}
	}
	if len(filtered) == 0 {
		fmt.Println()
		fmt.Println("最近恢复任务：暂无")
		return nil
	}

	fmt.Println()
	fmt.Println("最近恢复任务：")
	headers := []string{"状态", "触发方式", "动作", "次数", "时间", "错误"}
	rows := make([][]string, 0, len(filtered))
	for _, recovery := range filtered {
		rows = append(rows, []string{
			recovery.Status,
			recovery.Trigger,
			recovery.Action,
			strconv.Itoa(recovery.Attempt),
			recovery.CreatedAt,
			summarizeError(recovery.LastError),
		})
	}
	printAlignedTable(headers, rows)

	latest := filtered[0]
	if trim(latest.LastSSHOutput) != "" {
		fmt.Println()
		fmt.Println("最近一次恢复 SSH 输出：")
		fmt.Println(singleLineText(latest.LastSSHOutput))
	}
	return nil
}

// ListErrors 显示安装失败的 Agent 列表。
func (m *AgentMenu) ListErrors() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.AgentService.ListInstallCandidates(ctx)
	if err != nil {
		return err
	}
	failed := make([]app.AgentView, 0)
	for _, item := range items {
		if item.LastError != "" {
			failed = append(failed, item)
		}
	}
	printAgentTable(failed, "暂无 Agent 安装错误。", false)
	return nil
}

// Retry 让用户选择安装失败的机器并重试 Agent 安装。
func (m *AgentMenu) Retry(reader *bufio.Reader) error {
	items, err := m.selectRetryCandidates(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	for _, selected := range items {
		installDir, err := promptMenuWithDefault(reader, fmt.Sprintf("%s(%s) 安装目录", selected.Name, selected.IP), selected.InstallDir)
		if err != nil {
			if errors.Is(err, ErrBackToMenu) {
				return nil
			}
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		resp, err := m.core.AgentService.RetryInstallByIP(ctx, agentusecase.InstallAgentRequest{
			IP:         selected.IP,
			InstallDir: installDir,
		})
		cancel()
		if err != nil {
			return err
		}
		if err := printJSON(resp); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall 让用户选择已安装的 Agent 并执行卸载操作。
func (m *AgentMenu) Uninstall(reader *bufio.Reader) error {
	items, err := m.selectInstalledAgents(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	confirm, err := confirmYES(reader, fmt.Sprintf("确认卸载 %s", strings.Join(labels, ", ")))
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("已取消卸载。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, selected := range items {
		resp, err := m.core.AgentService.UninstallByIP(ctx, selected.IP)
		if err != nil {
			return err
		}
		if err := printJSON(resp); err != nil {
			return err
		}
	}
	return nil
}

// ListRecoveryTasks 显示最近的 Agent 恢复任务列表。
func (m *AgentMenu) ListRecoveryTasks() error {
	if m.core.RecoveryService == nil {
		return fmt.Errorf("恢复服务未启用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.RecoveryService.ListRecent(ctx, 20)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("暂无恢复任务。")
		return nil
	}
	headers := []string{"机器IP", "状态", "触发方式", "动作", "次数", "时间", "错误"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.MachineIP,
			item.Status,
			item.Trigger,
			item.Action,
			strconv.Itoa(item.Attempt),
			item.CreatedAt,
			summarizeError(item.LastError),
		})
	}
	printAlignedTable(headers, rows)
	return nil
}

// TriggerRecovery 让用户选择机器并手动触发 Agent 恢复操作。
func (m *AgentMenu) TriggerRecovery(reader *bufio.Reader) error {
	if m.core.RecoveryService == nil {
		return fmt.Errorf("恢复服务未启用")
	}
	items, err := m.selectInstalledAgents(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	confirm, err := confirmYES(reader, fmt.Sprintf("确认对 %s 执行手动拉起", strings.Join(labels, ", ")))
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("已取消。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, selected := range items {
		resp, err := m.core.RecoveryService.TriggerManualRecoverByIP(ctx, selected.IP)
		if err != nil {
			return err
		}
		if err := printJSON(resp); err != nil {
			return err
		}
	}
	return nil
}

// Upgrade 让用户选择已安装的 Agent 并执行升级操作。
func (m *AgentMenu) Upgrade(reader *bufio.Reader) error {
	items, err := m.selectInstalledAgents(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	confirm, err := confirmYES(reader, fmt.Sprintf("确认升级 %s 上的 Agent", strings.Join(labels, ", ")))
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	if !confirm {
		fmt.Println("已取消升级。")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, selected := range items {
		resp, err := m.core.AgentService.UpgradeByIP(ctx, selected.IP)
		if err != nil {
			return err
		}
		if err := printJSON(resp); err != nil {
			return err
		}
	}
	return nil
}

// RepairMySQLConfig 让用户选择机器并修复/重置其 MySQL 采集配置。
func (m *AgentMenu) RepairMySQLConfig(reader *bufio.Reader) error {
	selected, err := NewMachineMenu(m.core).selectManagedMachineByIP(reader)
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ip := selected.IP
	confirm, err := confirmYES(reader, fmt.Sprintf("确认对机器 %s 修复/重置 MySQL 采集配置吗？", ip))
	if err != nil {
		return err
	}
	if !confirm {
		fmt.Println("已取消。")
		return nil
	}

	taskID, err := m.core.AgentService.RepairMySQLConfigByIP(ctx, ip)
	if err != nil {
		return err
	}

	fmt.Printf("修复任务已创建 (ID: %s)，Agent 将在下次采集周期自动加载新配置。\n", taskID)
	return nil
}

// selectRetryCandidates 获取并展示可重试安装 Agent 的机器列表供用户选择。
func (m *AgentMenu) selectRetryCandidates(reader *bufio.Reader) ([]app.AgentView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.AgentService.ListInstallCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("当前没有可重试安装 Agent 的机器")
	}

	fmt.Println("以下是已纳入集群但 Agent 未在线的机器：")
	printAgentTable(items, "", true)
	return selectAgentViews(reader, items, "选择机器序号/名称/IP，多个用逗号分隔")
}

// selectInstalledAgents 获取并展示已安装 Agent 的机器列表供用户选择。
func (m *AgentMenu) selectInstalledAgents(reader *bufio.Reader) ([]app.AgentView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.AgentService.ListUninstallCandidates(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("当前没有可卸载的 Agent 或残留")
	}

	fmt.Println("以下是当前可卸载 Agent 或清理残留的机器：")
	printAgentTable(items, "", true)
	return selectAgentViews(reader, items, "选择机器序号/名称/IP，多个用逗号分隔")
}

// selectAgentViews 根据用户输入的序号、名称或 IP 从 Agent 列表中选择多个 Agent。
func selectAgentViews(reader *bufio.Reader, items []app.AgentView, label string) ([]app.AgentView, error) {
	text, err := promptMenu(reader, label)
	if err != nil {
		return nil, err
	}
	tokens := splitCommaInput(text)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("未选择机器")
	}
	selected := make([]app.AgentView, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		var matches []app.AgentView
		if idx, err := strconv.Atoi(token); err == nil {
			if idx < 1 || idx > len(items) {
				return nil, fmt.Errorf("无效机器序号 %s", token)
			}
			matches = append(matches, items[idx-1])
		} else {
			for _, item := range items {
				if item.IP == token || item.Name == token {
					matches = append(matches, item)
				}
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("未找到机器 %s", token)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("机器 %s 匹配到多条记录，请改用序号或 IP", token)
		}
		key := matches[0].IP
		if seen[key] {
			continue
		}
		seen[key] = true
		selected = append(selected, matches[0])
	}
	return selected, nil
}

// printAgentTable 以表格形式打印 Agent 视图列表。
func printAgentTable(items []app.AgentView, emptyMessage string, compact bool) {
	if len(items) == 0 {
		if emptyMessage != "" {
			fmt.Println(emptyMessage)
		}
		return
	}
	headers := []string{"名称", "IP", "集群", "机器状态", "安装态", "心跳态", "健康度", "检查结果", "恢复状态", "抑制到", "最近心跳", "安装目录", "错误"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.Name,
			item.IP,
			emptyAsDash(item.Cluster),
			item.MachineStatus,
			item.InstallState,
			item.HeartbeatState,
			emptyAsDash(item.OverallHealth),
			displayAgentError(item.CheckSummary, compact),
			emptyAsDash(item.RecoveryState),
			emptyAsDash(item.SuppressedUntil),
			emptyAsDash(item.LastHeartbeatAt),
			emptyAsDash(item.InstallDir),
			displayAgentError(item.LastError, compact),
		})
	}
	printAlignedTable(headers, rows)
}

// displayAgentError 根据紧凑模式决定是否截断显示 Agent 错误信息。
func displayAgentError(err string, compact bool) string {
	if compact {
		return summarizeError(err)
	}
	return emptyAsDash(err)
}

// summarizeError 将错误信息截断为单行摘要，超过限制长度时以省略号结尾。
func summarizeError(err string) string {
	err = trim(err)
	if err == "" {
		return "-"
	}
	err = trim(stripMySQLPasswordWarnings(singleLineText(err)))
	const limit = 72
	if displayWidth(err) <= limit {
		return err
	}
	runes := []rune(err)
	if len(runes) > limit-3 {
		runes = runes[:limit-3]
	}
	return string(runes) + "..."
}

// singleLineText 将多行文本转换为单行，用分号替换换行符。
func singleLineText(s string) string {
	s = trim(s)
	s = strings.ReplaceAll(s, "\n", " ; ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = trimReplaceSpaces(s)
	return s
}

// trimReplace 替换字符串中的指定内容并清理空白。
func trimReplace(s, old, new string) string {
	return trimReplaceSpaces(strings.ReplaceAll(s, old, new))
}

// trimReplaceSpaces 将连续空白字符合并为单个空格并去除首尾空白。
func trimReplaceSpaces(s string) string {
	return trim(strings.Join(strings.Fields(s), " "))
}

// stripMySQLPasswordWarnings 移除 MySQL 命令行工具输出中的密码警告信息。
func stripMySQLPasswordWarnings(s string) string {
	warnings := []string{
		"mysql: [Warning] Using a password on the command line interface can be insecure.",
		"mysqladmin: [Warning] Using a password on the command line interface can be insecure.",
	}
	for _, warning := range warnings {
		s = strings.ReplaceAll(s, warning, "")
	}
	s = strings.ReplaceAll(s, " ;  ; ", " ; ")
	s = strings.Trim(s, " ;")
	return trimReplaceSpaces(s)
}
