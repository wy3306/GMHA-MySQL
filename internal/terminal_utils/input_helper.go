package terminal_utils

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Prompter struct {
	reader *bufio.Reader
}

func NewPrompter() *Prompter {
	return &Prompter{
		reader: bufio.NewReader(os.Stdin),
	}
}

// Ask 询问文本输入
func (p *Prompter) Ask(label string, defaultValue string) string {
	fmt.Printf("%s", label)
	if defaultValue != "" {
		fmt.Printf(" [%s]", defaultValue)
	}
	fmt.Print(": ")

	input, _ := p.reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return defaultValue
	}
	return input
}

// AskBool 询问布尔值 (y/n)
func (p *Prompter) AskBool(label string, defaultValue bool) bool {
	defaultStr := "y"
	if !defaultValue {
		defaultStr = "n"
	}

	fmt.Printf("%s [%s]: ", label, defaultStr)
	input, _ := p.reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "" {
		return defaultValue
	}

	return input == "y" || input == "yes"
}

// PrintHeader 打印标题
func (p *Prompter) PrintHeader(title string) {
	fmt.Println("\n" + strings.Repeat("=", 40))
	fmt.Println(" " + title)
	fmt.Println(strings.Repeat("=", 40))
}

// PrintInfo 打印信息
func (p *Prompter) PrintInfo(format string, a ...interface{}) {
	fmt.Printf("[INFO] "+format+"\n", a...)
}
