package menu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gmha/internal/app"
	machinedomain "gmha/internal/domain/machine"
	machineusecase "gmha/internal/usecase/machine"
)

// MachineMenu 是机器管理的交互式菜单，负责展示机器信息并提供纳管、修改、删除、采集等操作入口。
type MachineMenu struct {
	core *app.App
}

// NewMachineMenu 创建一个新的 MachineMenu 实例。
func NewMachineMenu(core *app.App) *MachineMenu {
	return &MachineMenu{core: core}
}

// Run 运行机器管理菜单的主循环，显示菜单选项并处理用户选择。
func (m *MachineMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== 机器管理 ====")
		fmt.Println("1. 纳管机器")
		fmt.Println("2. 查看已纳管机器")
		fmt.Println("3. 修改机器")
		fmt.Println("4. 删除机器")
		fmt.Println("5. 分配机器到集群")
		fmt.Println("6. 采集机器静态信息")
		fmt.Println("7. 查看机器静态信息")
		fmt.Println("8. 查看机器动态数据")
		fmt.Println("9. 查看 MySQL 动态数据")
		fmt.Println("10. 查看 MySQL 静态数据")
		fmt.Println("11. 创建 SSH 凭证")
		fmt.Println("12. 查看 SSH 凭证")
		fmt.Println("13. 删除 SSH 凭证")
		fmt.Println("0. 返回上级")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		switch trim(line) {
		case "1":
			if err := m.Onboard(reader); err != nil {
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
			if err := m.AssignCluster(reader); err != nil {
				printError(err)
			}
		case "6":
			if err := m.CollectStatic(reader); err != nil {
				printError(err)
			}
		case "7":
			if err := m.ShowStatic(reader); err != nil {
				printError(err)
			}
		case "8":
			if err := m.ShowDynamic(reader); err != nil {
				printError(err)
			}
		case "9":
			if err := m.ShowMySQLDynamic(reader); err != nil {
				printError(err)
			}
		case "10":
			if err := m.ShowMySQLStatic(reader); err != nil {
				printError(err)
			}
		case "11":
			if err := m.CreateCredential(reader); err != nil {
				printError(err)
			}
		case "12":
			if err := m.ListCredentials(); err != nil {
				printError(err)
			}
		case "13":
			if err := m.DeleteCredential(reader); err != nil {
				printError(err)
			}
		case "0":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// CollectStatic 让用户选择机器并触发静态信息采集。
func (m *MachineMenu) CollectStatic(reader *bufio.Reader) error {
	machines, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	for _, machine := range machines {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		item, err := m.core.MachineService.RefreshStaticInfo(ctx, machine.IP)
		cancel()
		if err != nil {
			return err
		}
		if len(machines) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", machine.Name, machine.IP)
		}
		if err := printJSON(item); err != nil {
			return err
		}
	}
	return nil
}

// ShowStatic 让用户选择机器并显示其静态信息。
func (m *MachineMenu) ShowStatic(reader *bufio.Reader) error {
	machines, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	for _, machine := range machines {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		item, err := m.core.MachineService.RefreshStaticInfo(ctx, machine.IP)
		cancel()
		if err != nil {
			return err
		}
		if len(machines) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", machine.Name, machine.IP)
		}
		if err := printJSON(item); err != nil {
			return err
		}
	}
	return nil
}

// ShowDynamic 让用户选择机器并显示其动态指标数据。
func (m *MachineMenu) ShowDynamic(reader *bufio.Reader) error {
	machines, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	for _, machine := range machines {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		item, err := m.core.MachineService.GetMachineDynamicMetrics(ctx, machine.IP)
		cancel()
		if err != nil {
			return err
		}
		if len(machines) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", machine.Name, machine.IP)
		}
		printDynamicMetrics(item, "机器动态数据")
	}
	return nil
}

// ShowMySQLDynamic 让用户选择 MySQL 实例并显示其动态指标数据。
func (m *MachineMenu) ShowMySQLDynamic(reader *bufio.Reader) error {
	instances, err := selectMySQLInstances(m.core, reader, "选择 MySQL 实例序号/名称/IP:Port，多个用逗号分隔")
	if err != nil {
		return err
	}
	for _, instance := range instances {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		item, err := m.core.MachineService.GetMySQLDynamicMetrics(ctx, instance.Endpoint())
		cancel()
		if err != nil {
			return err
		}
		if len(instances) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", instance.Name, instance.Endpoint())
		}
		printDynamicMetrics(item, "MySQL 动态数据")
	}
	return nil
}

// ShowMySQLStatic 让用户选择机器并显示其 MySQL 静态信息。
func (m *MachineMenu) ShowMySQLStatic(reader *bufio.Reader) error {
	machines, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	for _, machine := range machines {
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		item, err := m.core.MachineService.RefreshStaticInfo(ctx, machine.IP)
		cancel()
		if err != nil {
			return err
		}
		if len(machines) > 1 {
			fmt.Printf("\n===== %s (%s) =====\n", machine.Name, machine.IP)
		}
		printMySQLStaticInfo(machine.Name, item.MySQL)
	}
	return nil
}

// Onboard 引导用户输入机器信息并执行纳管操作。
func (m *MachineMenu) Onboard(reader *bufio.Reader) error {
	name, err := prompt(reader, "机器名")
	if err != nil {
		return err
	}
	ip, err := prompt(reader, "机器 IP")
	if err != nil {
		return err
	}
	sshPortText, err := promptWithDefault(reader, "SSH 端口", "22")
	if err != nil {
		return err
	}
	sshPort, err := strconv.Atoi(sshPortText)
	if err != nil {
		return err
	}
	var sshPassword string
	var sshUser string
	var credential string
	cred, hasCredential, err := m.selectCredential(reader)
	if err != nil {
		return err
	}
	if hasCredential {
		credential = cred.Name
		sshUser = cred.SSHUser
		fmt.Printf("已选择凭证: %s，SSH 用户: %s\n", cred.Name, cred.SSHUser)
	} else {
		sshUser, err = prompt(reader, "SSH 用户")
		if err != nil {
			return err
		}
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 15*time.Second)
	ok, err := m.core.MachineService.CheckSSHTrust(checkCtx, ip, sshPort, sshUser)
	checkCancel()
	if err != nil {
		return err
	}
	if ok {
		fmt.Println("检测到已完成 SSH 互信，跳过密码输入。")
	} else if hasCredential {
		fmt.Println("未检测到 SSH 互信，将使用所选凭证初始化免密。")
	} else {
		fmt.Println("未检测到 SSH 互信，请输入 SSH 密码继续纳管。")
		sshPassword, err = prompt(reader, "SSH 密码")
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := m.core.MachineService.Onboard(ctx, machineusecase.OnboardMachineRequest{
		Name:           name,
		IP:             ip,
		SSHPort:        sshPort,
		SSHUser:        sshUser,
		SSHPassword:    sshPassword,
		CredentialName: credential,
	})
	if err != nil {
		return err
	}
	return printJSON(resp)
}

// List 显示所有已纳管机器列表。
func (m *MachineMenu) List() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListMachines(ctx)
	if err != nil {
		return err
	}
	printMachineTable(items)
	return nil
}

// Update 引导用户选择机器并修改其信息。
func (m *MachineMenu) Update(reader *bufio.Reader) error {
	selected, err := m.selectManagedMachineByIP(reader)
	if err != nil {
		return err
	}
	name, err := promptWithDefault(reader, "机器名", selected.Name)
	if err != nil {
		return err
	}
	ip, err := promptWithDefault(reader, "机器 IP", selected.IP)
	if err != nil {
		return err
	}
	sshPortText, err := promptWithDefault(reader, "SSH 端口", strconv.Itoa(selected.SSHPort))
	if err != nil {
		return err
	}
	sshPort, err := strconv.Atoi(sshPortText)
	if err != nil {
		return err
	}
	sshUser, err := promptWithDefault(reader, "SSH 用户", selected.SSHUser)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.core.MachineService.UpdateMachine(ctx, selected.ID, name, ip, sshPort, sshUser); err != nil {
		return err
	}
	return printJSON(map[string]string{"ip": ip})
}

// Delete 引导用户选择机器并执行删除操作。
func (m *MachineMenu) Delete(reader *bufio.Reader) error {
	items, err := m.selectManagedMachines(reader, "选择要删除的机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, fmt.Sprintf("%s(%s)", item.Name, item.IP))
	}
	ok, err := confirmYES(reader, fmt.Sprintf("确认删除机器 %s", strings.Join(labels, ", ")))
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
	for _, selected := range items {
		if err := m.core.MachineService.DeleteMachine(ctx, selected.ID); err != nil {
			return err
		}
	}
	return printJSON(map[string]string{"deleted": strings.Join(labels, ", ")})
}

// AssignCluster 引导用户选择机器和集群，将机器分配到指定集群。
func (m *MachineMenu) AssignCluster(reader *bufio.Reader) error {
	selected, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	clusterName, err := selectClusterName(m.core, reader, "选择集群序号/名称")
	if err != nil {
		return err
	}
	for _, item := range selected {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		err := m.core.MachineService.AssignMachineCluster(ctx, item.ID, clusterName)
		cancel()
		if err != nil {
			return err
		}
	}
	return printJSON(map[string]string{"machines": strconv.Itoa(len(selected)), "cluster": clusterName})
}

// CreateCredential 引导用户输入 SSH 凭证信息并创建凭证。
func (m *MachineMenu) CreateCredential(reader *bufio.Reader) error {
	name, err := prompt(reader, "凭证名称")
	if err != nil {
		return err
	}
	sshUser, err := prompt(reader, "SSH 用户")
	if err != nil {
		return err
	}
	sshPassword, err := prompt(reader, "SSH 密码")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, err := m.core.MachineService.CreateSSHCredential(ctx, name, sshUser, "password", sshPassword, "", "")
	if err != nil {
		return err
	}
	return printJSON(item)
}

// ListCredentials 显示所有 SSH 凭证列表。
func (m *MachineMenu) ListCredentials() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListSSHCredentials(ctx)
	if err != nil {
		return err
	}
	printCredentialTable(items)
	return nil
}

// DeleteCredential 引导用户选择 SSH 凭证并执行删除操作。
func (m *MachineMenu) DeleteCredential(reader *bufio.Reader) error {
	credential, err := prompt(reader, "凭证名称或 ID")
	if err != nil {
		return err
	}
	ok, err := confirmYES(reader, fmt.Sprintf("确认删除 SSH 凭证 %s", credential))
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
	if err := m.core.MachineService.DeleteSSHCredential(ctx, credential); err != nil {
		return err
	}
	return printJSON(map[string]string{"credential": credential})
}

// prompt 显示输入提示并读取用户输入。
func prompt(reader *bufio.Reader, label string) (string, error) {
	fmt.Printf("%s: ", label)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptWithDefault 显示带默认值的输入提示，用户直接回车则使用默认值。
func promptWithDefault(reader *bufio.Reader, label, def string) (string, error) {
	if def == "" {
		fmt.Printf("%s: ", label)
	} else {
		fmt.Printf("%s [%s]: ", label, def)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return def, nil
	}
	return value, nil
}

// trim 去除字符串首尾空白字符。
func trim(s string) string {
	return strings.TrimSpace(s)
}

// selectCredential 展示可用 SSH 凭证列表并引导用户选择一个凭证。
func (m *MachineMenu) selectCredential(reader *bufio.Reader) (app.SSHCredentialView, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListSSHCredentials(ctx)
	if err != nil {
		return app.SSHCredentialView{}, false, err
	}
	if len(items) == 0 {
		return app.SSHCredentialView{}, false, nil
	}
	fmt.Println("可用 SSH 凭证：")
	for i, item := range items {
		fmt.Printf("%d. %s | 用户=%s | ID=%s\n", i+1, item.Name, item.SSHUser, item.ID)
	}
	choiceText, err := prompt(reader, "选择凭证序号/名称/ID，留空表示不使用凭证")
	if err != nil {
		return app.SSHCredentialView{}, false, err
	}
	choiceText = strings.TrimSpace(choiceText)
	if choiceText == "" {
		return app.SSHCredentialView{}, false, nil
	}
	choice, err := strconv.Atoi(choiceText)
	if err == nil {
		if choice < 1 || choice > len(items) {
			return app.SSHCredentialView{}, false, fmt.Errorf("无效序号")
		}
		return items[choice-1], true, nil
	}
	for _, item := range items {
		if item.ID == choiceText || item.Name == choiceText {
			return item, true, nil
		}
	}
	return app.SSHCredentialView{}, false, fmt.Errorf("未找到 SSH 凭证 %s", choiceText)
}

// selectManagedMachineByIP 引导用户选择一台已纳管机器并返回。
func (m *MachineMenu) selectManagedMachineByIP(reader *bufio.Reader) (machineView, error) {
	items, err := m.selectManagedMachines(reader, "选择机器序号/名称/IP")
	if err != nil {
		return machineView{}, err
	}
	if len(items) == 0 {
		return machineView{}, fmt.Errorf("未选择机器")
	}
	return items[0], nil
}

// selectManagedMachines 展示已纳管机器列表并引导用户选择多台机器。
func (m *MachineMenu) selectManagedMachines(reader *bufio.Reader, label string) ([]machineView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.MachineService.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("暂无已纳管机器")
	}
	views := make([]machineView, 0, len(items))
	fmt.Println("已纳管机器：")
	for i, item := range items {
		view := machineView{
			ID:      item.ID,
			Name:    item.Name,
			IP:      item.IP,
			SSHPort: item.SSHPort,
			SSHUser: item.SSHUser,
			Cluster: item.Cluster,
			Status:  string(item.Status),
		}
		views = append(views, view)
		fmt.Printf("%d. %s | %s:%d | 用户=%s | 集群=%s | 状态=%s\n", i+1, view.Name, view.IP, view.SSHPort, view.SSHUser, emptyAsDash(view.Cluster), view.Status)
	}
	text, err := prompt(reader, label)
	if err != nil {
		return nil, err
	}
	tokens := splitCommaInput(text)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("未选择机器")
	}
	selected := make([]machineView, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		matches := make([]machineView, 0, 1)
		if idx, err := strconv.Atoi(token); err == nil {
			if idx < 1 || idx > len(views) {
				return nil, fmt.Errorf("无效机器序号 %s", token)
			}
			matches = append(matches, views[idx-1])
		} else {
			for _, item := range views {
				if item.IP == token || item.Name == token || item.ID == token {
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
		if seen[matches[0].ID] {
			continue
		}
		seen[matches[0].ID] = true
		selected = append(selected, matches[0])
	}
	return selected, nil
}

// machineView 是机器在菜单中的简化视图，包含展示所需的基本信息。
type machineView struct {
	ID      string
	Name    string
	IP      string
	SSHPort int
	SSHUser string
	Cluster string
	Status  string
}

// emptyAsDash 将空字符串转换为短横线显示。
func emptyAsDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// printError 打印错误信息，对过长的错误进行截断摘要显示。
func printError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	short := summarizeError(msg)
	if short == "-" {
		short = msg
	}
	if short != msg {
		fmt.Println("错误:", short)
		fmt.Println("提示: 完整错误已保存在状态信息中，可通过“查看安装错误”或“查看某台机器 Agent 状态”查看。")
		return
	}
	fmt.Println("错误:", msg)
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

// printMachineTable 以表格形式打印机器列表。
func printMachineTable(items []machinedomain.Machine) {
	if len(items) == 0 {
		fmt.Println("暂无已纳管机器。")
		return
	}
	headers := []string{"ID", "名称", "IP", "SSH端口", "SSH用户", "凭证", "集群", "状态"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ID,
			item.Name,
			item.IP,
			strconv.Itoa(item.SSHPort),
			item.SSHUser,
			emptyAsDash(item.CredentialID),
			emptyAsDash(item.Cluster),
			string(item.Status),
		})
	}
	printAlignedTable(headers, rows)
}

// printCredentialTable 以表格形式打印 SSH 凭证列表。
func printCredentialTable(items []app.SSHCredentialView) {
	if len(items) == 0 {
		fmt.Println("暂无 SSH 凭证。")
		return
	}
	headers := []string{"ID", "名称", "SSH用户", "创建时间", "更新时间"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ID,
			item.Name,
			item.SSHUser,
			item.CreatedAt,
			item.UpdatedAt,
		})
	}
	printAlignedTable(headers, rows)
}

// printDynamicMetrics 以表格形式打印动态指标数据。
func printDynamicMetrics(item app.DynamicMetricsView, title string) {
	fmt.Printf("%s：%s (%s)  心跳=%s  最近心跳=%s\n", title, item.MachineName, item.MachineIP, emptyAsDash(item.HeartbeatState), emptyAsDash(item.LastHeartbeatAt))
	if len(item.Metrics) == 0 {
		fmt.Println("暂无动态数据。")
		return
	}
	headers := []string{"名称", "分类", "成功", "类型", "值", "采集时间", "耗时(ms)", "错误"}
	rows := make([][]string, 0, len(item.Metrics))
	skipped := 0
	for _, metric := range item.Metrics {
		if isSkippedMetric(metric.Value) {
			skipped++
			continue
		}
		rows = append(rows, []string{
			metric.Name,
			metric.Category,
			strconv.FormatBool(metric.Success),
			metric.ValueType,
			metricValueString(metric.Value),
			metric.CollectedAt.Local().Format("2006-01-02 15:04:05"),
			strconv.FormatInt(metric.DurationMS, 10),
			summarizeError(metric.Error),
		})
	}
	if len(rows) == 0 {
		fmt.Println("暂无可展示动态数据。")
		return
	}
	printAlignedTableWithGaps(headers, rows, []int{2, 2, 2, 2, 6, 2, 2})
}

// metricValueString 将指标值转换为字符串表示，过长时截断。
func metricValueString(v any) string {
	if v == nil {
		return "-"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	text := string(data)
	if len(text) > 160 {
		return text[:160] + "..."
	}
	return text
}

// isSkippedMetric 判断指标值是否为跳过状态。
func isSkippedMetric(v any) bool {
	item, ok := v.(map[string]any)
	if !ok {
		return false
	}
	skipped, _ := item["skipped"].(bool)
	return skipped
}
