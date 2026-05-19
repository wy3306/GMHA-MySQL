package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func collectMemoryGB(ctx context.Context) (int, error) {
	out, err := runCommand(ctx, "free", "-g")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Mem:" {
			return strconv.Atoi(fields[1])
		}
	}
	return 0, fmt.Errorf("failed to parse free -g output")
}
