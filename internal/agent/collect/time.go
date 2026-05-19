package collect

import (
	"context"
	"os/exec"
	"sort"
	"strings"
)

func collectSwapEnabled(ctx context.Context) (bool, error) {
	out, err := runCommand(ctx, "swapon", "--show")
	if err != nil {
		return false, nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return len(lines) > 1, nil
}

func collectNTPEnabled(ctx context.Context) (bool, error) {
	out, err := runCommand(ctx, "timedatectl", "status")
	if err != nil {
		return false, nil
	}
	text := strings.ToLower(out)
	if strings.Contains(text, "ntp service: active") || strings.Contains(text, "system clock synchronized: yes") {
		return true, nil
	}
	return false, nil
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
