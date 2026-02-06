package cmdline

import (
	"fmt"
)

func Run(args []string) {
	if len(args) < 1 {
		printUsage()
		return
	}

	command := args[0]
	switch command {
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
	}
}

func printUsage() {
	fmt.Println("Usage: gmha [command] [flags]")
	fmt.Println("Commands:")
	fmt.Println("  (no commands available)")
}
