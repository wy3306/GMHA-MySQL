package menu

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBackToMenu 是用户选择返回上级菜单时返回的哨兵错误。
var ErrBackToMenu = errors.New("back to menu")

// isBackInput 判断用户输入是否为返回操作（"0" 或 "esc"）。
func isBackInput(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v == "0" || v == "esc"
}

// promptMenu 显示带返回选项的输入提示，要求用户输入非空值。
func promptMenu(reader *bufio.Reader, label string) (string, error) {
	for {
		fmt.Printf("%s [输入 0 或 esc 返回]: ", label)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(line)
		if isBackInput(value) {
			return "", ErrBackToMenu
		}
		if value == "" {
			fmt.Printf("%s不能为空，请重新输入。\n", label)
			continue
		}
		return value, nil
	}
}

// promptMenuWithDefault 显示带默认值和返回选项的输入提示，用户直接回车则使用默认值。
func promptMenuWithDefault(reader *bufio.Reader, label, def string) (string, error) {
	if def == "" {
		fmt.Printf("%s [输入 0 或 esc 返回]: ", label)
	} else {
		fmt.Printf("%s [%s，输入 0 或 esc 返回]: ", label, def)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(line)
	if isBackInput(value) {
		return "", ErrBackToMenu
	}
	if value == "" {
		return def, nil
	}
	return value, nil
}

// promptMenuIntWithDefault 显示带整数默认值的输入提示，用户直接回车则使用默认值。
func promptMenuIntWithDefault(reader *bufio.Reader, label string, def int) (int, error) {
	text, err := promptMenuWithDefault(reader, label, strconv.Itoa(def))
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(strings.ToLower(text))
	switch value {
	case "yes", "y":
		// 用户在默认值场景里顺手输入 yes 时，按默认值处理，避免中断流程。
		return def, nil
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s 无效，应输入数字，直接回车可使用默认值 %d", label, def)
	}
	return number, nil
}

// splitCommaInput 将逗号分隔的用户输入拆分为去重后的字符串切片。
func splitCommaInput(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// confirmYES 显示确认提示，要求用户输入 yes 确认操作。
func confirmYES(reader *bufio.Reader, prompt string) (bool, error) {
	value, err := promptMenu(reader, prompt+"，输入 yes 确认")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "yes", "y":
		return true, nil
	default:
		return false, nil
	}
}

// confirmYesDefault 显示带默认值的确认提示，用户直接回车则使用默认值。
func confirmYesDefault(reader *bufio.Reader, prompt string, def bool) (bool, error) {
	defaultText := "no"
	if def {
		defaultText = "yes"
	}
	value, err := promptMenuWithDefault(reader, prompt+"，输入 yes 确认", defaultText)
	if err != nil {
		return false, err
	}
	return isMenuYes(value), nil
}
