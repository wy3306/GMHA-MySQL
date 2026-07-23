package core

import (
	"context"
	"strings"
	"testing"
)

func TestRunShellWithOutputStreamsCompleteLinesAndKeepsCombinedOutput(t *testing.T) {
	runner := &CommandRunner{preferSystemd: false}
	var lines []string
	output, err := runner.RunShellWithOutput(context.Background(), "task-stream", "copy", "printf 'copy 10%%\\ncopy 20%%\\n'; printf 'warning\\n' >&2; printf 'done'", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"copy 10%", "copy 20%", "warning", "done"} {
		found := false
		for _, line := range lines {
			if line == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stream callback missing %q: %#v", want, lines)
		}
		if !strings.Contains(output, want) {
			t.Fatalf("combined output missing %q: %s", want, output)
		}
	}
}
