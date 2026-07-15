// Package configassets provides the built-in Manager profiles and templates.
// Filesystem files may override these assets during development, while the
// embedded copy keeps a deployed gmha binary independent of its working directory.
package configassets

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

//go:embed profiles templates
var assets embed.FS

// ReadFile reads an embedded path relative to the configs directory.
func ReadFile(name string) ([]byte, error) {
	name = strings.TrimPrefix(path.Clean(strings.ReplaceAll(name, "\\", "/")), "configs/")
	return fs.ReadFile(assets, name)
}
