package render

import (
	"os"
	"path/filepath"
)

// Loader 负责从文件系统加载模板文件和配置档案。
type Loader struct {
	baseDir string
}

func NewLoader(baseDir string) *Loader {
	return &Loader{baseDir: baseDir}
}

func (l *Loader) LoadTemplate(group, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(l.baseDir, "templates", group, name))
}

func (l *Loader) LoadProfile(group, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(l.baseDir, "profiles", group, name))
}
