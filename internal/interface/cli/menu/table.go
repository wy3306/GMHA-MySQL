// Package menu 提供 GMHA CLI 的交互式菜单界面，负责通过终端菜单引导用户完成各项管理操作，包括终端表格对齐和显示宽度计算等工具函数。
package menu

import (
	"fmt"
	"strings"
	"unicode"
)

// printAlignedTable 打印对齐的表格，自动计算列宽。
func printAlignedTable(headers []string, rows [][]string) {
	printAlignedTableWithGaps(headers, rows, nil)
}

// printAlignedTableWithGaps 打印带自定义列间距的对齐表格。
func printAlignedTableWithGaps(headers []string, rows [][]string, gaps []int) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = displayWidth(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if w := displayWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}

	for i, header := range headers {
		fmt.Print(padDisplay(header, widths[i]))
		if i < len(headers)-1 {
			fmt.Print(strings.Repeat(" ", tableGap(gaps, i)))
		}
	}
	fmt.Println()

	for _, row := range rows {
		for i, cell := range row {
			fmt.Print(padDisplay(cell, widths[i]))
			if i < len(row)-1 {
				fmt.Print(strings.Repeat(" ", tableGap(gaps, i)))
			}
		}
		fmt.Println()
	}
}

// tableGap 返回指定列的间距值，默认为 2。
func tableGap(gaps []int, column int) int {
	if column >= 0 && column < len(gaps) && gaps[column] > 0 {
		return gaps[column]
	}
	return 2
}

// padDisplay 将字符串填充到指定显示宽度，不足部分用空格补齐。
func padDisplay(s string, width int) string {
	pad := width - displayWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// displayWidth 计算字符串的终端显示宽度，中文字符占 2 列。
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeDisplayWidth(r)
	}
	return width
}

// runeDisplayWidth 返回单个 Unicode 字符的终端显示宽度，CJK 字符占 2 列，ASCII 字符占 1 列。
func runeDisplayWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Han, r), unicode.In(r, unicode.Hangul, unicode.Hiragana, unicode.Katakana):
		return 2
	case r < 128:
		return 1
	default:
		return 1
	}
}
