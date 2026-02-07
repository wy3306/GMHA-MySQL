package wizard

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// MenuItem 定义菜单中的每一项
type MenuItem struct {
	Label  string // 显示的标签文本
	Value  string // 选中后返回的值
	Hotkey string // 快捷键 (如 "1", "q")
}

// MenuModel 定义 Bubble Tea 模型，用于控制菜单逻辑
type MenuModel struct {
	Title    string     // 菜单标题
	Items    []MenuItem // 菜单项列表
	Cursor   int        // 当前光标位置 (索引)
	Selected string     // 用户选中的值
	Quitting bool       // 是否正在退出菜单
}

// Init 初始化模型，这里不需要执行任何命令
func (m MenuModel) Init() tea.Cmd {
	return nil
}

// Update 处理消息并更新模型状态
func (m MenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg: // 处理按键消息
		switch msg.String() {
		case "ctrl+c": // Ctrl+C 强制退出
			m.Selected = ""    // 清空选择
			m.Quitting = true  // 标记退出
			return m, tea.Quit // 发送退出命令
		case "up", "k": // 上箭头或 k 键向上移动光标
			if m.Cursor > 0 {
				m.Cursor-- // 光标减一
			}
		case "down", "j": // 下箭头或 j 键向下移动光标
			if m.Cursor < len(m.Items)-1 {
				m.Cursor++ // 光标加一
			}
		case "enter": // 回车键确认选择
			m.Selected = m.Items[m.Cursor].Value // 记录当前光标处的选项值
			m.Quitting = true                    // 标记退出
			return m, tea.Quit                   // 发送退出命令
		default:
			// 检查快捷键 (1-9, q 等)
			for _, item := range m.Items {
				if item.Hotkey == msg.String() { // 如果按键匹配快捷键
					m.Selected = item.Value // 记录对应的选项值
					m.Quitting = true       // 标记退出
					return m, tea.Quit      // 发送退出命令
				}
			}
		}
	}
	return m, nil // 返回更新后的模型
}

// View 渲染菜单的 UI
func (m MenuModel) View() string {
	if m.Quitting { // 如果正在退出，不渲染任何内容
		return ""
	}

	// 渲染标题
	s := fmt.Sprintf("\n  %s\n\n", m.Title)

	// 遍历并渲染每个菜单项
	for i, item := range m.Items {
		cursor := " " // 默认光标为空格
		if m.Cursor == i {
			cursor = "> " // 如果是当前选中项，显示 >
		}

		// 拼接菜单项文本
		s += fmt.Sprintf("  %s %s\n", cursor, item.Label)
	}

	// 添加底部的帮助提示信息
	s += "\n  (↑/↓: 选择 • Enter: 确认 • 1-9/q: 快捷键)\n"
	return s
}
