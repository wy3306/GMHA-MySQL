package app

import (
	"context"
	"strings"
	"testing"

	aidomain "gmha/internal/domain/ai"
)

func fuzzyAIContext() map[string]any {
	return map[string]any{
		"clusters": []map[string]any{
			{"id": "alpha", "name": "Alpha 生产集群"},
			{"id": "beta", "name": "Beta 灾备集群"},
		},
		"machines": []map[string]any{
			{"id": "alpha-01", "name": "alpha-db-new", "ip": "10.0.0.11", "cluster": "alpha"},
			{"id": "alpha-02", "name": "alpha-db-old", "ip": "10.0.0.12", "cluster": "alpha"},
			{"id": "beta-01", "name": "beta-db-1", "ip": "10.0.1.11", "cluster": "beta"},
			{"id": "beta-02", "name": "beta-db-2", "ip": "10.0.1.12", "cluster": "beta"},
		},
	}
}

func TestAIFuzzyDeleteUnderstandsOrdinalClusterAndMachine(t *testing.T) {
	for _, prompt := range []string{
		"把第一个集群的第一个机器删除",
		"删掉首个集群里头一台主机",
		"移除 1号集群 中的 1号节点",
		"请删除集群列表排第一的集群内机器列表排第一的服务器",
	} {
		t.Run(prompt, func(t *testing.T) {
			proposals, changed, evidence := resolveAIFuzzyConversationProposals(
				nil, nil, prompt, nil, fuzzyAIContext(),
			)
			if !changed || len(proposals) != 2 {
				t.Fatalf("fuzzy delete did not become a two-step workflow: %#v, evidence=%#v", proposals, evidence)
			}
			remove, deletePlan := proposals[0], proposals[1]
			if remove.Action != "remove_cluster_members" || remove.TargetID != "alpha" ||
				aiContextString(remove.Parameters["machine_ids"]) != "alpha-01" {
				t.Fatalf("wrong prerequisite target for %q: %#v", prompt, remove)
			}
			if deletePlan.Action != "delete_machine" || deletePlan.TargetID != "alpha-01" ||
				aiContextString(deletePlan.Parameters["detach_only"]) != "true" {
				t.Fatalf("wrong machine delete target or scope for %q: %#v", prompt, deletePlan)
			}
			if deletePlan.WorkflowKey == "" || deletePlan.WorkflowKey != remove.WorkflowKey ||
				len(deletePlan.DependsOn) != 1 || deletePlan.DependsOn[0] != remove.OperationID {
				t.Fatalf("cluster removal dependency was not preserved: remove=%#v delete=%#v", remove, deletePlan)
			}
			joined := strings.Join(evidence, "\n")
			if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "alpha-db-new") {
				t.Fatalf("resolution evidence did not expose the exact objects: %s", joined)
			}
		})
	}
}

func TestAIFuzzyDeleteSupportsLastAndReverseOrdinals(t *testing.T) {
	for _, prompt := range []string{
		"把最后一个集群的最后一台服务器删掉",
		"删除倒数第一个集群内倒数第一个节点",
	} {
		proposals, changed, _ := resolveAIFuzzyConversationProposals(nil, nil, prompt, nil, fuzzyAIContext())
		if !changed || len(proposals) != 2 || proposals[1].TargetID != "beta-02" || proposals[0].TargetID != "beta" {
			t.Fatalf("reverse ordinal was resolved incorrectly for %q: %#v", prompt, proposals)
		}
	}
}

func TestAIFuzzyResolverGroundsModelSelectorsForEveryMachineAction(t *testing.T) {
	for _, action := range []string{"diagnose_machine", "restart_agent", "restart_mysql", "stop_mysql", "reboot_host", "delete_machine"} {
		proposals, changed, _ := resolveAIFuzzyConversationProposals([]aiModelProposal{{
			Action: action, TargetID: "第一个集群中的第二台机器",
		}}, nil, "处理第一个集群中的第二台机器", nil, fuzzyAIContext())
		if !changed {
			t.Fatalf("%s selector was not grounded", action)
		}
		found := false
		for _, proposal := range proposals {
			if proposal.Action == action && proposal.TargetID == "alpha-02" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s did not resolve to alpha-02: %#v", action, proposals)
		}
	}
}

func TestAIFuzzyResolverGroundsEveryExistingClusterAction(t *testing.T) {
	for _, action := range AIActionCatalog() {
		if action.TargetKind != "cluster" || action.ID == "create_cluster" {
			continue
		}
		proposal := aiModelProposal{
			Action: action.ID, TargetID: "第一个集群", Parameters: map[string]any{},
		}
		switch action.ID {
		case "register_cluster_members", "remove_cluster_members":
			proposal.Parameters["machine_ids"] = "第二台机器"
		case "configure_cluster_architecture":
			proposal.Parameters["machine_ids"] = []string{"第一台机器", "第二台机器"}
		case "configure_cluster_vip":
			proposal.Parameters["target_machine_id"] = "第一台机器"
		}
		proposals, changed, _ := resolveAIFuzzyConversationProposals(
			[]aiModelProposal{proposal}, nil, "处理第一个集群，涉及的机器按上述序号选择", nil, fuzzyAIContext(),
		)
		if !changed || len(proposals) != 1 || proposals[0].TargetID != "alpha" {
			t.Fatalf("cluster action %s was not grounded: %#v", action.ID, proposals)
		}
		switch action.ID {
		case "register_cluster_members", "remove_cluster_members":
			if aiContextString(proposals[0].Parameters["machine_ids"]) != "alpha-02" {
				t.Fatalf("%s machine_ids were not grounded: %#v", action.ID, proposals[0].Parameters)
			}
		case "configure_cluster_vip":
			if aiContextString(proposals[0].Parameters["target_machine_id"]) != "alpha-01" {
				t.Fatalf("%s target machine was not grounded: %#v", action.ID, proposals[0].Parameters)
			}
		}
	}
}

func TestAIFuzzyResolverCanRecoverWhenModelReturnsNoPlan(t *testing.T) {
	for _, test := range []struct {
		prompt string
		action string
		target string
	}{
		{"检查第一个集群的第二台机器", "diagnose_machine", "alpha-02"},
		{"重启第一个集群第一台机器上的 agent", "restart_agent", "alpha-01"},
		{"重启第二个集群第一台主机的 MySQL", "restart_mysql", "beta-01"},
		{"停止第二个集群第二个节点的 MySQL", "stop_mysql", "beta-02"},
		{"重启第一个集群的第二台主机", "reboot_host", "alpha-02"},
	} {
		proposals, changed, _ := resolveAIFuzzyConversationProposals(nil, nil, test.prompt, nil, fuzzyAIContext())
		if !changed || len(proposals) != 1 || proposals[0].Action != test.action || proposals[0].TargetID != test.target {
			t.Fatalf("fallback intent failed for %q: %#v", test.prompt, proposals)
		}
	}
}

func TestAIFuzzyDeleteDoesNotExpandRemoteCleanupWithoutExplicitWords(t *testing.T) {
	plain, _, _ := resolveAIFuzzyConversationProposals(nil, nil, "删除 alpha 集群的 alpha-db-new", nil, fuzzyAIContext())
	if len(plain) != 2 {
		t.Fatalf("plain delete did not create workflow: %#v", plain)
	}
	deletePlan := plain[1]
	if aiContextString(deletePlan.Parameters["detach_only"]) != "true" ||
		aiContextString(deletePlan.Parameters["delete_mysql"]) != "false" ||
		aiContextString(deletePlan.Parameters["delete_agent"]) != "false" {
		t.Fatalf("plain delete unexpectedly expanded destructive scope: %#v", deletePlan.Parameters)
	}

	full, _, _ := resolveAIFuzzyConversationProposals(nil, nil,
		"彻底删除 alpha 集群的 alpha-db-new，连同 MySQL 和 Agent 一起清理", nil, fuzzyAIContext())
	if len(full) != 2 {
		t.Fatalf("full cleanup did not create workflow: %#v", full)
	}
	deletePlan = full[1]
	if aiContextString(deletePlan.Parameters["detach_only"]) != "false" ||
		aiContextString(deletePlan.Parameters["delete_mysql"]) != "true" ||
		aiContextString(deletePlan.Parameters["delete_agent"]) != "true" {
		t.Fatalf("explicit cleanup scope was lost: %#v", deletePlan.Parameters)
	}
}

func TestAIFuzzyPronounUsesValidatedPreviousMachineIntent(t *testing.T) {
	previous := &aidomain.Plan{Action: "diagnose_machine", TargetID: "beta-01", TargetName: "beta-db-1"}
	proposals, changed, _ := resolveAIFuzzyConversationProposals(nil, nil, "把它删掉，只解除纳管", previous, fuzzyAIContext())
	if !changed || len(proposals) != 2 || proposals[1].Action != "delete_machine" || proposals[1].TargetID != "beta-01" {
		t.Fatalf("validated previous target was not reused for pronoun follow-up: %#v", proposals)
	}
}

func TestAIFuzzyResolverRefusesOutOfRangeAndAmbiguousTargets(t *testing.T) {
	for _, prompt := range []string{
		"删除第九个集群的第一台机器",
		"删除机器",
		"删除第一台机器上的 MySQL 实例",
		"删除 alpha-db-new 上的 Agent 服务",
	} {
		proposals, _, _ := resolveAIFuzzyConversationProposals(nil, nil, prompt, nil, fuzzyAIContext())
		if len(proposals) != 0 {
			t.Fatalf("unsafe target %q produced a plan: %#v", prompt, proposals)
		}
	}
}

func TestAIDeleteMachineActionIsCriticalAndDefaultsToDetachOnly(t *testing.T) {
	action, ok := lookupAIAction("delete_machine")
	if !ok || action.Risk != "critical" || action.APIPath != "/api/v1/machines/{machine_id}" {
		t.Fatalf("delete machine action is not correctly protected: %#v", action)
	}
	plans := (&AIService{}).proposalsToPlans([]aiModelProposal{{
		Action: "delete_machine", TargetID: "alpha-01", TargetName: "alpha-db-new",
	}}, "default", "")
	if len(plans) != 1 || plans[0].Status != "approval_required" || plans[0].ConfirmationPhrase == "" {
		t.Fatalf("machine deletion skipped typed critical approval: %#v", plans)
	}
	guarded, err := applyMachineDeletionSafety(plans[0], aiMachineDeletionImpact{
		Found: true, MachineID: "alpha-01", MachineName: "alpha-db-new", MachineIP: "10.0.0.11",
		DetachOnly: true,
	})
	if err != nil || guarded.Parameters["detach_only"] != "true" || !strings.Contains(guarded.Summary, "远端 MySQL 与 Agent 保持不变") {
		t.Fatalf("safe detach-only semantics were not preserved: %#v, %v", guarded, err)
	}
}

func TestAIDeleteMachineRequiresClusterRemovalOrDependency(t *testing.T) {
	plan := aidomain.Plan{
		Action: "delete_machine", TargetID: "alpha-01", Risk: "critical",
		Parameters: map[string]string{"detach_only": "true"},
	}
	impact := aiMachineDeletionImpact{
		Found: true, MachineID: "alpha-01", MachineName: "alpha-db-new",
		MachineIP: "10.0.0.11", ClusterName: "alpha", DetachOnly: true,
		Blockers: []string{"机器仍属于集群 alpha；必须先安全移出集群"},
	}
	if guarded, err := applyMachineDeletionSafety(plan, impact); err == nil || guarded.Status != "blocked" {
		t.Fatalf("direct deletion of a cluster member was not blocked: %#v, %v", guarded, err)
	}
	plan.DependsOn = []string{"remove-from-cluster"}
	plan.Parameters["expected_cluster_removal"] = "alpha"
	impact.HasClusterRemovalStage = true
	impact.Blockers = nil
	if guarded, err := applyMachineDeletionSafety(plan, impact); err != nil || guarded.Status == "blocked" {
		t.Fatalf("dependency-protected deletion was not approvable: %#v, %v", guarded, err)
	}
}

func TestAIDetachOnlyRuntimeDoesNotRequireOnlineAgent(t *testing.T) {
	contextValue := map[string]any{
		"machines": []map[string]any{{
			"id": "alpha-01", "agent_management_ready": false,
			"agent_management_reason": "Agent 离线",
		}},
		"active_tasks": []map[string]any{},
	}
	if err := validateAIPlanRuntimeContext(contextValue, aidomain.Plan{
		Action: "delete_machine", TargetID: "alpha-01",
		Parameters: map[string]string{"detach_only": "true"},
	}); err != nil {
		t.Fatalf("Manager-only detach incorrectly required an online Agent: %v", err)
	}
}

func TestAIDeleteMachineMigratesIntoPreviousDefaultPolicy(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{
			AnalysisIntervalMinutes: 15,
			AllowedActions:          defaultAIAllowedActionsExcept("delete_machine"),
		},
	}}
	service := newTestAIService(t, repo)
	_ = service
	state, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range state.Settings.AllowedActions {
		if action == "delete_machine" {
			return
		}
	}
	t.Fatalf("existing default policy did not receive delete_machine: %#v", state.Settings.AllowedActions)
}
