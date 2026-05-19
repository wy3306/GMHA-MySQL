package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func collectDiskFreeGB(ctx context.Context) (int, error) {
	paths := []string{"/data", "/"}
	for _, path := range paths {
		out, err := runCommand(ctx, "df", "-BG", path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 2 {
			continue
		}
		fields := strings.Fields(lines[len(lines)-1])
		if len(fields) < 4 {
			continue
		}
		return parseSizeGB(fields[3])
	}
	return 0, fmt.Errorf("failed to collect disk usage")
}

func parseSizeGB(v string) (int, error) {
	v = strings.TrimSpace(strings.TrimSuffix(v, "G"))
	return strconv.Atoi(v)
}
