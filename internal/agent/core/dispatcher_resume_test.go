package core

import (
	"testing"

	taskdomain "gmha/internal/domain/task"
)

func TestFirstIncompleteStepSkipsPreviouslySuccessfulSteps(t *testing.T) {
	steps := []taskdomain.DispatchStep{
		{ID: "step-1", StepNo: 1, StepName: "verify_mysql", Status: taskdomain.StepSuccess},
		{ID: "step-2", StepNo: 2, StepName: "install_pt_tools", Status: taskdomain.StepPending},
		{ID: "step-3", StepNo: 3, StepName: "init_accounts", Status: taskdomain.StepPending},
	}
	if got := firstIncompleteStep(steps); got.ID != "step-2" {
		t.Fatalf("firstIncompleteStep() = %+v, want step-2", got)
	}
}
