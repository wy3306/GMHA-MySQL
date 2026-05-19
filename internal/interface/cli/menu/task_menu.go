package menu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gmha/internal/app"
	taskdomain "gmha/internal/domain/task"
	"golang.org/x/term"
)

// TaskMenu 是任务管理的交互式菜单，负责展示任务列表并提供创建、查看、监控等操作入口。
type TaskMenu struct {
	core *app.App
}

// NewTaskMenu 创建一个新的 TaskMenu 实例。
func NewTaskMenu(core *app.App) *TaskMenu {
	return &TaskMenu{core: core}
}

// Run 运行任务管理菜单的主循环，显示菜单选项并处理用户选择。
func (m *TaskMenu) Run(reader *bufio.Reader) error {
	for {
		fmt.Println()
		fmt.Println("==== 任务管理 ====")
		fmt.Println("1. 创建 exec 任务")
		fmt.Println("2. 查看任务列表")
		fmt.Println("3. 查看子任务列表")
		fmt.Println("4. 查看正在运行任务")
		fmt.Println("5. 查看任务详情")
		fmt.Println("0. 返回上级")
		fmt.Print("请选择: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		switch trim(line) {
		case "1":
			if err := m.CreateExec(reader); err != nil {
				printError(err)
			}
		case "2":
			if err := m.List(); err != nil {
				printError(err)
			}
		case "3":
			if err := m.ListSteps(reader); err != nil {
				printError(err)
			}
		case "4":
			if err := m.ListRunning(); err != nil {
				printError(err)
			}
		case "5":
			if err := m.Detail(reader); err != nil {
				printError(err)
			}
		case "0":
			return nil
		default:
			fmt.Println("无效选项")
		}
	}
}

// CreateExec 引导用户选择机器和命令并创建 exec 类型任务。
func (m *TaskMenu) CreateExec(reader *bufio.Reader) error {
	machines, err := NewMachineMenu(m.core).selectManagedMachines(reader, "选择机器序号/名称/IP，多个用逗号分隔")
	if err != nil {
		return err
	}
	command, err := promptMenu(reader, "执行命令")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	taskIDs := make([]string, 0, len(machines))
	for _, machine := range machines {
		item, err := m.core.TaskService.CreateExecTask(ctx, machine.IP, command)
		if err != nil {
			return err
		}
		fmt.Printf("任务已创建：%s (%s)\n", machine.Name, machine.IP)
		if len(machines) == 1 {
			if err := printTaskDetail(item); err != nil {
				return err
			}
		}
		taskIDs = append(taskIDs, item.Task.ID)
	}
	if len(taskIDs) > 1 {
		return watchTaskGroup(m.core, reader, "exec 批量任务进度", taskIDs)
	}
	return nil
}

// List 显示最近的任务列表。
func (m *TaskMenu) List() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.TaskService.ListTasks(ctx, 50)
	if err != nil {
		return err
	}
	printTaskTable(m.core, items)
	return nil
}

// ListRunning 显示当前正在运行的任务列表。
func (m *TaskMenu) ListRunning() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.TaskService.ListTasks(ctx, 100)
	if err != nil {
		return err
	}
	running := make([]taskdomain.Task, 0)
	for _, item := range items {
		if item.Status == taskdomain.StatusPending || item.Status == taskdomain.StatusSent || item.Status == taskdomain.StatusRunning {
			running = append(running, item)
		}
	}
	printTaskTable(m.core, running)
	return nil
}

// Detail 引导用户选择任务并显示其详细信息，包括步骤和事件。
func (m *TaskMenu) Detail(reader *bufio.Reader) error {
	taskID, err := m.selectTaskID(reader, "选择任务序号/ID")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, err := m.core.TaskService.GetTaskDetail(ctx, taskID)
	if err != nil {
		return err
	}
	return printTaskDetail(item)
}

// ListSteps 引导用户选择任务并显示其子任务步骤列表。
func (m *TaskMenu) ListSteps(reader *bufio.Reader) error {
	taskID, err := m.selectTaskID(reader, "选择任务序号/ID")
	if err != nil {
		if errors.Is(err, ErrBackToMenu) {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item, err := m.core.TaskService.GetTaskDetail(ctx, taskID)
	if err != nil {
		return err
	}
	printTaskStepTable(item.Steps)
	return nil
}

// selectTaskID 展示任务列表并引导用户通过序号或 ID 选择一个任务。
func (m *TaskMenu) selectTaskID(reader *bufio.Reader, label string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items, err := m.core.TaskService.ListTasks(ctx, 50)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", fmt.Errorf("暂无任务")
	}
	printTaskTable(m.core, items)
	text, err := promptMenu(reader, label)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if idx, err := strconv.Atoi(text); err == nil {
		if idx < 1 || idx > len(items) {
			return "", fmt.Errorf("无效任务序号")
		}
		return items[idx-1].ID, nil
	}
	for _, item := range items {
		if item.ID == text {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("未找到任务 %s", text)
}

// printTaskTable 以表格形式打印任务列表。
func printTaskTable(core *app.App, items []taskdomain.Task) {
	if len(items) == 0 {
		fmt.Println("暂无任务。")
		return
	}
	machineLabels := taskMachineLabels(core, items)
	headers := []string{"任务ID", "类型", "机器", "AgentID", "状态", "进度", "当前步骤", "创建时间"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.ID,
			string(item.Type),
			machineLabels[item.MachineID],
			item.AgentID,
			string(item.Status),
			fmt.Sprintf("%d%%", item.ProgressPercent),
			emptyAsDash(item.CurrentStep),
			item.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	printAlignedTable(headers, rows)
}

// taskMachineLabels 构建机器 ID 到显示标签（名称+IP）的映射。
func taskMachineLabels(core *app.App, items []taskdomain.Task) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.MachineID] = "-"
	}
	if core == nil || core.MachineService == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	machines, err := core.MachineService.ListMachines(ctx)
	if err != nil {
		return out
	}
	for _, machine := range machines {
		if _, ok := out[machine.ID]; ok {
			out[machine.ID] = machineLabel(machine.Name, machine.IP)
		}
	}
	return out
}

// taskDetailMachineLabel 返回任务详情中机器的显示标签。
func taskDetailMachineLabel(item app.TaskDetail) string {
	if strings.TrimSpace(item.MachineIP) != "" {
		return machineLabel(item.MachineName, item.MachineIP)
	}
	return "-"
}

// machineLabel 生成机器的显示标签，格式为 "名称(IP)"。
func machineLabel(name, ip string) string {
	name = strings.TrimSpace(name)
	ip = strings.TrimSpace(ip)
	if name == "" {
		return emptyAsDash(ip)
	}
	if ip == "" {
		return name
	}
	return fmt.Sprintf("%s(%s)", name, ip)
}

// printTaskStepTable 以表格形式打印任务步骤列表。
func printTaskStepTable(steps []taskdomain.Step) {
	if len(steps) == 0 {
		fmt.Println("暂无子任务。")
		return
	}
	headers := []string{"序号", "子任务ID", "子任务名", "状态", "消息", "开始时间", "结束时间"}
	rows := make([][]string, 0, len(steps))
	for _, step := range steps {
		rows = append(rows, []string{
			fmt.Sprintf("%d", step.StepNo),
			step.ID,
			step.StepName,
			string(step.Status),
			emptyAsDash(singleLineText(step.Message)),
			formatTime(step.StartedAt),
			formatTime(step.FinishedAt),
		})
	}
	printAlignedTable(headers, rows)
}

// printTaskDetail 打印任务的完整详情，包括概览、步骤和事件。
func printTaskDetail(item app.TaskDetail) error {
	fmt.Println()
	fmt.Println("任务概览：")
	overviewHeaders := []string{"任务ID", "类型", "机器", "AgentID", "状态", "进度", "当前步骤", "创建时间", "开始时间", "结束时间"}
	overviewRows := [][]string{{
		item.Task.ID,
		string(item.Task.Type),
		taskDetailMachineLabel(item),
		item.Task.AgentID,
		string(item.Task.Status),
		fmt.Sprintf("%d%%", item.Task.ProgressPercent),
		emptyAsDash(item.Task.CurrentStep),
		item.Task.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		formatTime(item.Task.StartedAt),
		formatTime(item.Task.FinishedAt),
	}}
	printAlignedTable(overviewHeaders, overviewRows)

	fmt.Println()
	fmt.Println("步骤：")
	if len(item.Steps) == 0 {
		fmt.Println("暂无步骤。")
	} else {
		stepHeaders := []string{"序号", "步骤ID", "步骤名", "状态", "消息", "开始时间", "结束时间"}
		stepRows := make([][]string, 0, len(item.Steps))
		for _, step := range item.Steps {
			stepRows = append(stepRows, []string{
				fmt.Sprintf("%d", step.StepNo),
				step.ID,
				step.StepName,
				string(step.Status),
				emptyAsDash(summarizeTaskText(step.Message)),
				formatTime(step.StartedAt),
				formatTime(step.FinishedAt),
			})
		}
		printAlignedTable(stepHeaders, stepRows)
	}

	fmt.Println()
	fmt.Println("事件：")
	if len(item.Events) == 0 {
		fmt.Println("暂无事件。")
		return nil
	}
	eventHeaders := []string{"时间", "类型", "步骤ID", "内容"}
	eventRows := make([][]string, 0, len(item.Events))
	for _, event := range item.Events {
		eventRows = append(eventRows, []string{
			event.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			string(event.EventType),
			emptyAsDash(event.StepID),
			emptyAsDash(summarizeTaskText(event.Content)),
		})
	}
	printAlignedTable(eventHeaders, eventRows)
	return nil
}

// summarizeTaskText 将任务文本截断为指定长度的摘要。
func summarizeTaskText(s string) string {
	s = singleLineText(s)
	const limit = 96
	if displayWidth(s) <= limit {
		return s
	}
	out := make([]rune, 0, limit)
	width := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if width+rw > limit-3 {
			break
		}
		out = append(out, r)
		width += rw
	}
	return string(out) + "..."
}

// watchTask 根据终端类型选择实时或轮询方式监控单个任务进度。
func watchTask(core *app.App, reader *bufio.Reader, taskID string) error {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return watchTaskLive(core, taskID)
	}
	return watchTaskPrompt(core, reader, taskID)
}

// watchTaskGroup 根据终端类型选择实时或轮询方式批量监控多个任务进度。
func watchTaskGroup(core *app.App, reader *bufio.Reader, title string, taskIDs []string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return watchTaskGroupLive(core, title, taskIDs)
	}
	return watchTaskGroupPrompt(core, reader, title, taskIDs)
}

// watchTaskGroupLive 在终端备用屏幕中实时刷新批量任务进度。
func watchTaskGroupLive(core *app.App, title string, taskIDs []string) error {
	fd := int(os.Stdin.Fd())
	enterAlternateScreen()
	defer exitAlternateScreen()
	hideCursor()
	defer showCursor()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		items, err := loadTaskDetails(core, taskIDs)
		if err != nil {
			return err
		}
		clearScreen()
		printTaskGroupWatchView(title, items, time.Now())
		if allTasksFinished(items) {
			fmt.Print("\n所有任务已结束，按 0 或 Esc 返回。")
		} else {
			fmt.Print("\n自动刷新中，按 0 或 Esc 返回。")
		}
		input, err := waitWatchInput(fd, ticker.C)
		if err != nil {
			return err
		}
		if input == "back" {
			fmt.Print("\n")
			return nil
		}
	}
}

// watchTaskGroupPrompt 通过用户手动回车刷新来监控批量任务进度。
func watchTaskGroupPrompt(core *app.App, reader *bufio.Reader, title string, taskIDs []string) error {
	fmt.Println("正在观察批量任务进度。直接回车刷新，输入 0 或 esc 返回。")
	for {
		items, err := loadTaskDetails(core, taskIDs)
		if err != nil {
			return err
		}
		fmt.Printf("\n===== %s %s =====\n", title, time.Now().Format("15:04:05"))
		printTaskGroupWatchView(title, items, time.Now())
		if allTasksFinished(items) {
			fmt.Print("所有任务已结束 [回车返回，0 或 esc 返回]: ")
			_, err := reader.ReadString('\n')
			return err
		}
		fmt.Print("继续观察 [回车刷新，0 或 esc 返回]: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if isBackInput(strings.TrimSpace(line)) {
			fmt.Println("已退出观察，任务仍在后台执行。")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

// watchTaskLive 在终端备用屏幕中实时刷新单个任务进度。
func watchTaskLive(core *app.App, taskID string) error {
	fd := int(os.Stdin.Fd())
	enterAlternateScreen()
	defer exitAlternateScreen()
	hideCursor()
	defer showCursor()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		item, err := loadTaskDetail(core, taskID)
		if err != nil {
			return err
		}
		clearScreen()
		printTaskWatchView(item, time.Now())
		if item.Task.Status == taskdomain.StatusSuccess || item.Task.Status == taskdomain.StatusFailed {
			fmt.Print("\n任务已结束，按 0 或 Esc 返回。")
		} else {
			fmt.Print("\n自动刷新中，按 0 或 Esc 返回。")
		}

		input, err := waitWatchInput(fd, ticker.C)
		if err != nil {
			return err
		}
		if input == "back" {
			fmt.Print("\n")
			return nil
		}
	}
}

// watchTaskPrompt 通过用户手动回车刷新来监控单个任务进度。
func watchTaskPrompt(core *app.App, reader *bufio.Reader, taskID string) error {
	fmt.Println("正在观察任务进度。直接回车刷新完整详情，输入 0 或 esc 返回。")
	for {
		item, err := loadTaskDetail(core, taskID)
		if err != nil {
			return err
		}
		fmt.Printf("\n===== 任务进度刷新 %s =====\n", time.Now().Format("15:04:05"))
		printTaskWatchView(item, time.Now())
		if item.Task.Status == taskdomain.StatusSuccess || item.Task.Status == taskdomain.StatusFailed {
			fmt.Print("任务已结束 [回车返回，0 或 esc 返回]: ")
			_, err := reader.ReadString('\n')
			return err
		}
		fmt.Print("继续观察 [回车刷新完整详情，0 或 esc 返回]: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if isBackInput(strings.TrimSpace(line)) {
			fmt.Println("已退出观察，任务仍在后台执行。")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

// loadTaskDetail 加载任务详情，遇到 SQLite 锁时自动重试。
func loadTaskDetail(core *app.App, taskID string) (app.TaskDetail, error) {
	var lastErr error
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		item, err := core.TaskService.GetTaskDetail(ctx, taskID)
		cancel()
		if err == nil {
			return item, nil
		}
		lastErr = err
		if !isSQLiteBusy(err) {
			return app.TaskDetail{}, err
		}
		time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
	}
	return app.TaskDetail{}, lastErr
}

// loadTaskDetails 批量加载多个任务的详情。
func loadTaskDetails(core *app.App, taskIDs []string) ([]app.TaskDetail, error) {
	items := make([]app.TaskDetail, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		item, err := loadTaskDetail(core, taskID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// isSQLiteBusy 判断错误是否为 SQLite 数据库忙/锁定状态。
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

// printTaskGroupWatchView 打印批量任务的实时监控视图。
func printTaskGroupWatchView(title string, items []app.TaskDetail, now time.Time) {
	fmt.Printf("%s  刷新: %s\n", title, now.Format("15:04:05"))
	fmt.Println()
	headers := []string{"任务ID", "机器", "状态", "进度", "当前步骤", "步骤状态", "消息", "开始", "结束"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		stepStatus := "-"
		message := "-"
		startedAt := formatClock(item.Task.StartedAt)
		finishedAt := formatClock(item.Task.FinishedAt)
		if step := currentTaskStep(item); step != nil {
			stepStatus = string(step.Status)
			message = emptyAsDash(summarizeTaskWatchText(step.Message, 36))
			startedAt = formatClock(step.StartedAt)
			finishedAt = formatClock(step.FinishedAt)
		}
		rows = append(rows, []string{
			shortTaskID(item.Task.ID),
			taskDetailMachineLabel(item),
			string(item.Task.Status),
			fmt.Sprintf("%d%%", item.Task.ProgressPercent),
			emptyAsDash(item.Task.CurrentStep),
			stepStatus,
			message,
			startedAt,
			finishedAt,
		})
	}
	printAlignedTable(headers, rows)

	fmt.Println()
	fmt.Println("失败/最近事件：")
	eventHeaders := []string{"任务ID", "时间", "类型", "内容"}
	eventRows := make([][]string, 0)
	for _, item := range items {
		for _, event := range latestTaskEvents(item.Events, 3) {
			if event.EventType != taskdomain.EventError && item.Task.Status != taskdomain.StatusFailed {
				continue
			}
			eventRows = append(eventRows, []string{
				shortTaskID(item.Task.ID),
				event.CreatedAt.Local().Format("15:04:05"),
				string(event.EventType),
				emptyAsDash(summarizeTaskWatchText(event.Content, 72)),
			})
		}
	}
	if len(eventRows) == 0 {
		fmt.Println("暂无错误事件。")
		return
	}
	printAlignedTable(eventHeaders, eventRows)
}

// currentTaskStep 获取任务当前正在执行的步骤。
func currentTaskStep(item app.TaskDetail) *taskdomain.Step {
	for i := range item.Steps {
		if item.Steps[i].StepName == item.Task.CurrentStep {
			return &item.Steps[i]
		}
	}
	for i := range item.Steps {
		if item.Steps[i].Status == taskdomain.StepRunning {
			return &item.Steps[i]
		}
	}
	return nil
}

// allTasksFinished 判断所有任务是否都已结束（成功或失败）。
func allTasksFinished(items []app.TaskDetail) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		if item.Task.Status != taskdomain.StatusSuccess && item.Task.Status != taskdomain.StatusFailed {
			return false
		}
	}
	return true
}

// printTaskWatchView 打印单个任务的实时监控视图。
func printTaskWatchView(item app.TaskDetail, now time.Time) {
	fmt.Printf("任务ID: %s\n", item.Task.ID)
	fmt.Printf("类型: %s  状态: %s  进度: %d%%  当前步骤: %s  刷新: %s\n",
		item.Task.Type,
		item.Task.Status,
		item.Task.ProgressPercent,
		emptyAsDash(item.Task.CurrentStep),
		now.Format("15:04:05"),
	)
	fmt.Printf("机器: %s  AgentID: %s\n", taskDetailMachineLabel(item), item.Task.AgentID)
	fmt.Printf("创建: %s  开始: %s  结束: %s\n",
		item.Task.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		formatTime(item.Task.StartedAt),
		formatTime(item.Task.FinishedAt),
	)
	fmt.Println()
	fmt.Println("步骤：")
	stepHeaders := []string{"序号", "步骤名", "状态", "消息", "开始", "结束"}
	stepRows := make([][]string, 0, len(item.Steps))
	for _, step := range item.Steps {
		stepRows = append(stepRows, []string{
			fmt.Sprintf("%d", step.StepNo),
			step.StepName,
			string(step.Status),
			emptyAsDash(summarizeTaskWatchText(step.Message, 42)),
			formatClock(step.StartedAt),
			formatClock(step.FinishedAt),
		})
	}
	printAlignedTable(stepHeaders, stepRows)
	fmt.Println()
	fmt.Println("最近事件：")
	events := latestTaskEvents(item.Events, 8)
	if len(events) == 0 {
		fmt.Println("暂无事件。")
		return
	}
	eventHeaders := []string{"时间", "类型", "步骤", "内容"}
	eventRows := make([][]string, 0, len(events))
	for _, event := range events {
		eventRows = append(eventRows, []string{
			event.CreatedAt.Local().Format("15:04:05"),
			string(event.EventType),
			shortStepID(event.StepID),
			emptyAsDash(summarizeTaskWatchText(event.Content, 72)),
		})
	}
	printAlignedTable(eventHeaders, eventRows)
}

// summarizeTaskWatchText 将任务监控文本截断为指定显示宽度的摘要。
func summarizeTaskWatchText(s string, limit int) string {
	s = stripMySQLPasswordWarnings(singleLineText(s))
	if displayWidth(s) <= limit {
		return s
	}
	out := make([]rune, 0, limit)
	width := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if width+rw > limit-3 {
			break
		}
		out = append(out, r)
		width += rw
	}
	return string(out) + "..."
}

// latestTaskEvents 获取最近指定数量的任务事件。
func latestTaskEvents(events []taskdomain.Event, limit int) []taskdomain.Event {
	if len(events) <= limit {
		return events
	}
	return events[len(events)-limit:]
}

// shortStepID 返回步骤 ID 的短格式（最后一个横杠后的部分）。
func shortStepID(id string) string {
	if id == "" {
		return "-"
	}
	idx := strings.LastIndex(id, "-")
	if idx >= 0 && idx < len(id)-1 {
		return id[idx+1:]
	}
	return id
}

// shortTaskID 返回任务 ID 的短格式（去除 task- 前缀并截断）。
func shortTaskID(id string) string {
	if id == "" {
		return "-"
	}
	const prefix = "task-"
	if strings.HasPrefix(id, prefix) && len(id) > len(prefix)+8 {
		return id[len(prefix):]
	}
	return id
}

// formatClock 将时间格式化为时:分:秒格式，nil 或零值返回短横线。
func formatClock(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("15:04:05")
}

// waitWatchInput 在终端原始模式下等待用户按键或定时器触发，用于实时任务监控。
func waitWatchInput(fd int, tick <-chan time.Time) (string, error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)
	if err := syscall.SetNonblock(fd, true); err != nil {
		return "", err
	}
	defer syscall.SetNonblock(fd, false)

	buf := make([]byte, 16)
	for {
		n, err := os.Stdin.Read(buf)
		if err == nil && n > 0 {
			text := strings.TrimSpace(strings.ToLower(string(buf[:n])))
			if strings.Contains(text, "0") || strings.Contains(text, "esc") || buf[0] == 27 {
				return "back", nil
			}
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return "", err
		}
		select {
		case <-tick:
			return "refresh", nil
		default:
			time.Sleep(80 * time.Millisecond)
		}
	}
}

// clearScreen 清除终端屏幕并将光标移到左上角。
func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

// enterAlternateScreen 切换到终端备用屏幕缓冲区。
func enterAlternateScreen() {
	fmt.Print("\033[?1049h")
}

// exitAlternateScreen 退出终端备用屏幕缓冲区。
func exitAlternateScreen() {
	fmt.Print("\033[?1049l")
}

// hideCursor 隐藏终端光标。
func hideCursor() {
	fmt.Print("\033[?25l")
}

// showCursor 恢复显示终端光标。
func showCursor() {
	fmt.Print("\033[?25h")
}

// formatTime 将时间格式化为日期时间格式，nil 或零值返回短横线。
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
