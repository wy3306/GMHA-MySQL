package agent

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogRotationCompressionAndRetention(t *testing.T) {
	dir := t.TempDir()
	writer, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.MaxSize = 1
	writer.MaxBackups = 2
	payload := strings.Repeat("startup diagnostic\n", 32768)
	for i := 0; i < 5; i++ {
		if _, err := writer.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond) // backup names have millisecond precision
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		files, _ := filepath.Glob(filepath.Join(dir, "agent-*.gz"))
		backups, _ := filepath.Glob(filepath.Join(dir, "agent-*"))
		if len(files) == 2 && len(backups) == 2 {
			for _, path := range files {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				r, err := gzip.NewReader(f)
				if err != nil {
					f.Close()
					t.Fatal(err)
				}
				data, err := io.ReadAll(r)
				r.Close()
				f.Close()
				if err != nil || string(data) != payload {
					t.Fatalf("invalid compressed diagnostic: %v", err)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retention not enforced: %v", backups)
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, err := os.Stat(filepath.Join(dir, "agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 1024*1024 || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe active log: size=%d mode=%v", info.Size(), info.Mode())
	}
}

func TestHeartbeatRetrySurvivesOutageAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := retryHeartbeat(ctx, time.Millisecond, func(context.Context) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return io.EOF
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestInvalidConfigStillHasBootstrapLog(t *testing.T) {
	dir := t.TempDir()
	writer, err := OpenLog(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	_, loadErr := LoadConfig(filepath.Join(dir, "missing.yaml"))
	if loadErr == nil {
		t.Fatal("expected missing config")
	}
	if _, err = writer.Write([]byte(loadErr.Error())); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	data, err := os.ReadFile(filepath.Join(dir, "logs", "agent.log"))
	if err != nil || !strings.Contains(string(data), "missing.yaml") {
		t.Fatalf("bootstrap error missing: %v", err)
	}
}
