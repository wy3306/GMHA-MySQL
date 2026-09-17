package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// OpenLog uses bounded, compressed retention, independent of journald/logrotate.
// Keep the bootstrap log beside agent.yaml, even if configuration is invalid.
func OpenLog(dir string) (*lumberjack.Logger, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "agent.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open agent log: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &lumberjack.Logger{Filename: path, MaxSize: 10, MaxBackups: 5, MaxAge: 7, Compress: true}, nil
}
