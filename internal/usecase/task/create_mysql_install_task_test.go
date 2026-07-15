package task

import "testing"

func TestBuildMySQLInstallStepsOptionalPTTools(t *testing.T) {
	withoutPT := buildMySQLInstallSteps("task-without-pt", false)
	withPT := buildMySQLInstallSteps("task-with-pt", true)
	if len(withPT) != len(withoutPT)+1 {
		t.Fatalf("expected one optional PT step, got without=%d with=%d", len(withoutPT), len(withPT))
	}
	for _, step := range withoutPT {
		if step.StepName == "install_pt_tools" {
			t.Fatal("PT step must not be present unless explicitly enabled")
		}
	}
	for index, step := range withPT {
		if step.StepName != "install_pt_tools" {
			continue
		}
		if index == 0 || withPT[index-1].StepName != "verify_mysql" {
			t.Fatalf("PT step must run after MySQL verification, previous=%q", withPT[index-1].StepName)
		}
		if step.Message != "安装 PT 工具（Percona Toolkit）" {
			t.Fatalf("unexpected PT step message: %q", step.Message)
		}
		return
	}
	t.Fatal("PT step was not created")
}
