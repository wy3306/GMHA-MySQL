package collect

import (
	"context"
	"strconv"
	"strings"
)

func collectCPUCores(ctx context.Context) (int, error) {
	out, err := runCommand(ctx, "nproc")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}
