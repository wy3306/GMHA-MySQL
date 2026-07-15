package render

import (
	"errors"
	"os"
	"path/filepath"

	configassets "gmha/configs"
)

// Loader 负责从文件系统加载模板文件和配置档案。
type Loader struct {
	baseDir string
}

func NewLoader(baseDir string) *Loader {
	return &Loader{baseDir: baseDir}
}

func (l *Loader) LoadTemplate(group, name string) ([]byte, error) {
	return l.read("templates", group, name)
}

func (l *Loader) LoadProfile(group, name string) ([]byte, error) {
	return l.read("profiles", group, name)
}

func (l *Loader) read(parts ...string) ([]byte, error) {
	filePath := filepath.Join(append([]string{l.baseDir}, parts...)...)
	data, err := os.ReadFile(filePath)
	if err == nil {
		return data, nil
	}
	// Only the conventional configs root falls back to built-in assets. A custom
	// root is an explicit override and should continue reporting its own errors.
	if !errors.Is(err, os.ErrNotExist) || filepath.Clean(l.baseDir) != "configs" {
		return nil, err
	}
	return configassets.ReadFile(filepath.Join(parts...))
}
