package mysqldynamic

import (
	"path/filepath"
	"testing"
)

func TestBuildCollectEnvsDoesNotInventDefaultInstance(t *testing.T) {
	t.Setenv("GMHA_AGENT_INSTALL_DIR", t.TempDir())
	envs, err := BuildCollectEnvs(filepath.Join(t.TempDir(), "mysql-heartbeat.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 0 {
		t.Fatalf("missing mysql config produced phantom instances: %+v", envs)
	}
}
