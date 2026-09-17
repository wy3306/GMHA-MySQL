package ssh

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryPreservesActivationFailure(t *testing.T) {
	for _, action := range []string{"start", "restart"} {
		for _, fail := range []string{"daemon-reload", "reset-failed", "enable", action, "is-active", "none"} {
			t.Run(action+"/"+fail, func(t *testing.T) {
				dir := t.TempDir()
				script := "#!/bin/sh\necho \"$*\" >> \"$CALLS\"\nif [ \"$1\" = \"$FAIL\" ]; then exit 23; fi\n"
				if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\necho startup-diagnostic\n"), 0700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("sh", "-c", recoveryActivationCommand(action))
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAIL="+fail, "CALLS="+filepath.Join(dir, "calls"))
				out, err := cmd.CombinedOutput()
				if fail == "none" && err != nil {
					t.Fatal(err)
				}
				if fail != "none" && (err == nil || cmd.ProcessState.ExitCode() != 23) {
					t.Fatalf("failure swallowed: %v %s", err, out)
				}
				if !strings.Contains(string(out), "startup-diagnostic") {
					t.Fatalf("missing diagnostics: %s", out)
				}
				calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
				if fail == "none" && !strings.Contains(string(calls), "enable gmha-agent.service") {
					t.Fatal("boot activation missing")
				}
			})
		}
	}
}
