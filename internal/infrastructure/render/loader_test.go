package render

import (
	"strings"
	"testing"
)

func TestLoaderUsesEmbeddedTemplatesOutsideProjectDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	data, err := NewLoader("configs").LoadTemplate("mysql", "my.cnf.tmpl")
	if err != nil {
		t.Fatalf("LoadTemplate() error = %v", err)
	}
	if !strings.Contains(string(data), "[mysqld]") {
		t.Fatalf("embedded template does not contain [mysqld]")
	}
}

func TestLoaderDoesNotMaskMissingCustomRoot(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := NewLoader("custom-configs").LoadTemplate("mysql", "my.cnf.tmpl"); err == nil {
		t.Fatal("LoadTemplate() expected an error for a missing custom root")
	}
}
