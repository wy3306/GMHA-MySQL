package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	aidomain "gmha/internal/domain/ai"
	taskdomain "gmha/internal/domain/task"
)

type memoryAIRepository struct {
	mu    sync.Mutex
	state aidomain.State
}

func (r *memoryAIRepository) Migrate() error { return nil }
func (r *memoryAIRepository) Load(context.Context) (aidomain.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := json.Marshal(r.state)
	var clone aidomain.State
	_ = json.Unmarshal(raw, &clone)
	return clone, nil
}
func (r *memoryAIRepository) Save(_ context.Context, state aidomain.State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := json.Marshal(state)
	return json.Unmarshal(raw, &r.state)
}

func newTestAIService(t *testing.T, repo aidomain.Repository) *AIService {
	t.Helper()
	service, err := NewAIService(repo, nil, nil, nil, filepath.Join(t.TempDir(), "ai-secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service
}

func TestAIProviderSecretIsEncryptedAndRedacted(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	saved, err := service.SaveProvider(context.Background(), aidomain.Provider{
		Name: "测试模型", Type: "openai", BaseURL: "https://api.example.com/v1",
		Model: "ops-model", APIKey: "secret-value", Enabled: true, IsDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.APIKey != maskedAIKey || !saved.HasAPIKey {
		t.Fatalf("provider secret was not redacted: %#v", saved)
	}
	state, _ := repo.Load(context.Background())
	if state.Providers[0].Secret == "" || state.Providers[0].Secret == "secret-value" {
		t.Fatalf("provider secret was not encrypted at rest: %#v", state.Providers[0])
	}
	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Providers[0].Secret != "" || overview.Providers[0].APIKey != maskedAIKey {
		t.Fatalf("overview leaked provider secret: %#v", overview.Providers[0])
	}
}

func TestAIProviderConnectionUsesCompatibleChatEndpoint(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		gotAuthorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": `{"answer":"连接成功","plans":[]}`}}},
		})
	}))
	defer server.Close()
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	provider, err := service.SaveProvider(context.Background(), aidomain.Provider{
		Name: "本地测试", Type: "custom", BaseURL: server.URL + "/v1",
		Model: "test-model", APIKey: "test-token", Enabled: true, IsDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.TestProvider(context.Background(), provider.ID); err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Bearer test-token" {
		t.Fatalf("unexpected authorization header %q", gotAuthorization)
	}
}

func TestAIHighRiskPlanCannotBypassConfirmation(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	state, _ := repo.Load(context.Background())
	state.Settings.Enabled = true
	state.Settings.AllowedActions = []string{"restart_mysql"}
	state.Plans = []aidomain.Plan{{
		ID: "plan-high", Title: "重启 MySQL", Action: "restart_mysql", ActionLabel: "重启 MySQL",
		Risk: "high", TargetID: "machine-01", TargetName: "db-01", Status: "approval_required",
		ConfirmationPhrase: "确认重启 MySQL db-01", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}}
	if err := repo.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	plan, err := service.ExecutePlan(context.Background(), "plan-high", "确认重启 MySQL db-02", true)
	if err == nil {
		t.Fatal("high-risk execution accepted a mismatched confirmation phrase")
	}
	if plan.Status != "approval_required" {
		t.Fatalf("plan state changed despite rejected confirmation: %s", plan.Status)
	}
}

func TestAIExecutePlanRejectsEmptyPlanIDClearly(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	_, err := service.ExecutePlan(context.Background(), "  ", "", false)
	if err == nil || !strings.Contains(err.Error(), "执行计划 ID 为空") {
		t.Fatalf("expected a clear empty plan ID error, got %v", err)
	}
}

func TestAIExpiredUnsubmittedPlanCanBeRejected(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{AnalysisIntervalMinutes: 15},
		Plans: []aidomain.Plan{{
			ID: "plan-expired", Action: "restart_mysql", Risk: "high",
			Status: "expired", CreatedAt: time.Now().UTC().Add(-time.Hour),
			ExpiresAt: time.Now().UTC().Add(-30 * time.Minute),
		}},
	}}
	service := newTestAIService(t, repo)
	rejected, err := service.RejectPlan(context.Background(), "plan-expired")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("expired unsubmitted plan was not rejected: %#v", rejected)
	}
}

func TestAIExpiredPlanRecordsThatNothingWasSubmitted(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{AnalysisIntervalMinutes: 15},
		Plans: []aidomain.Plan{{
			ID: "plan-expiring", Action: "restart_mysql", Risk: "high",
			Status: "approval_required", ConfirmationPhrase: "确认重启 MySQL DB-01",
			CreatedAt: time.Now().UTC().Add(-time.Hour),
			ExpiresAt: time.Now().UTC().Add(-time.Minute),
		}},
	}}
	service := newTestAIService(t, repo)
	plan, err := service.ExecutePlan(context.Background(), "plan-expiring", "确认重启 MySQL DB-01", true)
	if err == nil || !strings.Contains(err.Error(), "未提交任何操作") {
		t.Fatalf("expected an explicit expired-without-submission error, got %#v, %v", plan, err)
	}
	if plan.Status != "expired" || plan.ExecutionStage != "not_started" || plan.TaskID != "" {
		t.Fatalf("expired plan looked partially submitted: %#v", plan)
	}
}

func TestAISettingsAlwaysForceHighRiskConfirmation(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	settings, err := service.SaveSettings(context.Background(), aidomain.Settings{
		Enabled: true, AnalysisIntervalMinutes: 1, AlwaysConfirmHighRisk: false,
		AllowedActions: []string{"restart_mysql", "unknown_action"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AlwaysConfirmHighRisk {
		t.Fatal("high-risk confirmation must be forced on")
	}
	if settings.AnalysisIntervalMinutes != 5 {
		t.Fatalf("minimum analysis interval not enforced: %d", settings.AnalysisIntervalMinutes)
	}
	if len(settings.AllowedActions) != 1 || settings.AllowedActions[0] != "restart_mysql" {
		t.Fatalf("unknown action was not removed: %#v", settings.AllowedActions)
	}
}

func TestAIActionCatalogPublishesCallableClusterVIPContract(t *testing.T) {
	actions := AIActionCatalog()
	byID := make(map[string]AIActionDefinition, len(actions))
	var configure, remove *AIActionDefinition
	for index := range actions {
		byID[actions[index].ID] = actions[index]
		switch actions[index].ID {
		case "configure_cluster_vip":
			configure = &actions[index]
		case "remove_cluster_vip":
			remove = &actions[index]
		}
	}
	if configure == nil || remove == nil {
		t.Fatalf("cluster VIP actions are missing from the AI contract: %#v", actions)
	}
	if configure.HTTPMethod != http.MethodPost || configure.APIPath != "/api/v1/clusters/{cluster_name}/vip/config" {
		t.Fatalf("unexpected configure VIP API contract: %#v", configure)
	}
	required := map[string]bool{}
	for _, parameter := range configure.Parameters {
		if parameter.Required {
			required[parameter.Name] = true
		}
	}
	for _, name := range []string{"target_id", "vip_address", "vip_prefix", "target_machine_id", "default_interface"} {
		if !required[name] {
			t.Fatalf("configure VIP contract did not require %s: %#v", name, configure.Parameters)
		}
	}
	if remove.HTTPMethod != http.MethodDelete || !strings.Contains(remove.APIPath, "vip={vip_address}") {
		t.Fatalf("unexpected remove VIP API contract: %#v", remove)
	}
	for id, expected := range map[string]struct {
		method string
		path   string
	}{
		"create_cluster":           {http.MethodPost, "/api/v1/clusters"},
		"update_cluster":           {http.MethodPut, "/api/v1/clusters/{cluster_name}"},
		"register_cluster_members": {http.MethodPost, "/api/v1/clusters/{cluster_name}/members"},
		"remove_cluster_members":   {http.MethodDelete, "/api/v1/machines/{machine_id}/assign-cluster"},
		"scan_cluster_vip":         {http.MethodPost, "/api/v1/clusters/{cluster_name}/vip/validate"},
		"run_cluster_backup":       {http.MethodPost, "/api/v1/backup/cluster-runs"},
		"rolling_upgrade_cluster_mysql": {
			http.MethodPost, "/api/v1/tasks/mysql-cluster-upgrade/start",
		},
		"uninstall_cluster_mysql": {http.MethodPost, "/api/v1/tasks/cluster-mysql-uninstall"},
		"cleanup_cluster":         {http.MethodPost, "/api/v1/clusters/{cluster_name}/cleanup"},
		"delete_cluster":          {http.MethodDelete, "/api/v1/clusters/{cluster_name}"},
	} {
		action, ok := byID[id]
		if !ok || action.HTTPMethod != expected.method || action.APIPath != expected.path {
			t.Fatalf("cluster action %s is not callable through its documented API: %#v", id, action)
		}
	}

	actions[0].ID = "mutated"
	if AIActionCatalog()[0].ID == "mutated" {
		t.Fatal("AIActionCatalog returned shared mutable state")
	}
}

func TestClusterAPICatalogLinksEveryClusterAIActionAndMarksSecrets(t *testing.T) {
	endpoints := ClusterAPICatalog()
	ids := make(map[string]bool, len(endpoints))
	linkedActions := make(map[string]bool)
	for _, endpoint := range endpoints {
		if endpoint.ID == "" || endpoint.Method == "" || endpoint.Path == "" || endpoint.InvocationMode == "" {
			t.Fatalf("incomplete cluster endpoint contract: %#v", endpoint)
		}
		if ids[endpoint.ID] {
			t.Fatalf("duplicate cluster endpoint id %q", endpoint.ID)
		}
		ids[endpoint.ID] = true
		if endpoint.InvocationMode == "ai_action" {
			if endpoint.AIActionID == "" {
				t.Fatalf("AI endpoint has no action link: %#v", endpoint)
			}
			linkedActions[endpoint.AIActionID] = true
		}
		if endpoint.InvocationMode == "secure_input_api" && len(endpoint.SensitiveParameters) == 0 {
			t.Fatalf("secure-input endpoint did not name sensitive parameters: %#v", endpoint)
		}
	}
	for _, action := range AIActionCatalog() {
		if action.TargetKind == "cluster" && !linkedActions[action.ID] {
			t.Fatalf("cluster AI action %s is absent from the endpoint catalog", action.ID)
		}
	}
	endpoints[0].ID = "mutated"
	if ClusterAPICatalog()[0].ID == "mutated" {
		t.Fatal("ClusterAPICatalog returned shared mutable state")
	}
}

func TestAINewClusterOperationsMigrateIntoPreviousDefaultPolicy(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{
			AnalysisIntervalMinutes: 15,
			AllowedActions:          append([]string(nil), preClusterOperationsAIAllowedActions...),
		},
	}}
	service := newTestAIService(t, repo)
	_ = service
	state, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, action := range state.Settings.AllowedActions {
		allowed[action] = true
	}
	for _, action := range []string{"scan_cluster_vip", "run_cluster_backup", "rolling_upgrade_cluster_mysql", "uninstall_cluster_mysql"} {
		if !allowed[action] {
			t.Fatalf("upgraded default policy did not receive %s: %#v", action, state.Settings.AllowedActions)
		}
	}
}

func TestAIClusterMySQLUninstallSafetyRequiresDependenciesCleared(t *testing.T) {
	plan := aidomain.Plan{Action: "uninstall_cluster_mysql", TargetID: "prod", Risk: "critical"}
	guarded, err := applyClusterMySQLUninstallSafety(plan, aiClusterMySQLUninstallImpact{
		ClusterName: "prod",
		Port:        3306,
		Instances:   []string{"DB-01（10.0.0.11:3306）"},
		VIPs:        []string{"10.0.0.100/24"},
		Backups:     []string{"prod-daily"},
	})
	if err == nil || guarded.Status != "blocked" {
		t.Fatalf("destructive uninstall with dependencies was not blocked: %#v, %v", guarded, err)
	}
	for _, expected := range []string{"VIP", "备份"} {
		if !strings.Contains(guarded.Error, expected) {
			t.Fatalf("uninstall blocker did not mention %s: %s", expected, guarded.Error)
		}
	}
}

func TestAIClusterBackupUsesApprovalAndNeverRequestsCredentials(t *testing.T) {
	plan := aidomain.Plan{Action: "run_cluster_backup", TargetID: "prod", Risk: "medium"}
	guarded, err := applyClusterBackupSafety(plan, aiClusterBackupImpact{
		ClusterName: "prod",
		Policies:    []string{"daily（policy-01，full，machine-01:3306）"},
		Targets:     []string{"DB-01（10.0.0.11:3306）"},
	})
	if err != nil || guarded.Status != "approval_required" {
		t.Fatalf("ready backup plan was not approvable: %#v, %v", guarded, err)
	}
	joined := strings.Join(guarded.Evidence, "\n") + "\n" + guarded.Summary
	if strings.Contains(strings.ToLower(joined), "password") {
		t.Fatalf("backup plan exposed or requested credentials: %s", joined)
	}
	if !strings.Contains(joined, "不进入模型") {
		t.Fatalf("backup credential boundary was not explicit: %s", joined)
	}
}

func TestAIClusterCleanupCannotHideImpactOrRunWithActiveTasks(t *testing.T) {
	plan := aidomain.Plan{Action: "cleanup_cluster", TargetID: "prod", TargetName: "prod", Risk: "critical"}
	guarded, err := applyClusterCleanupSafety(plan, aiClusterDeletionImpact{
		Found:       true,
		ClusterName: "prod",
		Machines:    []string{"DB-01 (10.0.0.11)", "DB-02 (10.0.0.12)"},
		MySQL:       []string{"machine-01:3306 [running]", "machine-02:3306 [running]"},
		VIPs:        []string{"10.0.0.100/24 [L2_ARP]"},
		Backups:     []string{"prod-daily [启用]"},
		ActiveTasks: []string{"task-running [running]"},
	})
	if err == nil || guarded.Status != "blocked" {
		t.Fatalf("cleanup with an active task was not blocked: %#v, %v", guarded, err)
	}
	evidence := strings.Join(guarded.Evidence, "\n")
	for _, expected := range []string{"将处理机器：2 台", "将卸载 MySQL：2 个实例", "业务 VIP：1 个", "备份策略：1 条"} {
		if !strings.Contains(evidence, expected) {
			t.Fatalf("cleanup impact omitted %q: %s", expected, evidence)
		}
	}
}

func TestAIRemoveClusterMembersBlocksVIPAndBackupTargets(t *testing.T) {
	plan := aidomain.Plan{Action: "remove_cluster_members", TargetID: "prod", Risk: "high"}
	guarded, err := applyClusterMemberRemovalSafety(plan, aiClusterMemberRemovalImpact{
		ClusterName: "prod",
		Machines:    []aiClusterArchitectureNode{{ID: "machine-01", Name: "DB-01", IP: "10.0.0.11", Cluster: "prod"}},
		VIPHolders:  []string{"10.0.0.100（当前 machine-01 / 期望 machine-01）"},
		Backups:     []string{"prod-daily（machine-01）"},
		Blockers:    []string{"所选机器仍是业务 VIP 当前或期望持有者", "所选机器仍被备份策略引用"},
	})
	if err == nil || guarded.Status != "blocked" {
		t.Fatalf("VIP/backup target was allowed to leave the cluster: %#v, %v", guarded, err)
	}
	if !strings.Contains(guarded.Error, "VIP") || !strings.Contains(guarded.Error, "备份") {
		t.Fatalf("member-removal blockers were not explicit: %s", guarded.Error)
	}
}

func TestAIUpgradeAddsNewVIPActionsToPreviousDefaultPolicy(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{
			AnalysisIntervalMinutes: 15,
			AllowedActions:          append([]string(nil), preVIPAIAllowedActions...),
		},
	}}
	service := newTestAIService(t, repo)
	_ = service
	state, err := repo.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, action := range state.Settings.AllowedActions {
		allowed[action] = true
	}
	if !allowed["configure_cluster_vip"] || !allowed["remove_cluster_vip"] {
		t.Fatalf("upgraded default policy did not receive VIP actions: %#v", state.Settings.AllowedActions)
	}
}

func TestAIVIPSubnetValidation(t *testing.T) {
	for _, test := range []struct {
		vip, address string
		prefix       int
		want         bool
	}{
		{vip: "10.0.0.100", address: "10.0.0.11/24", prefix: 24, want: true},
		{vip: "10.0.1.100", address: "10.0.0.11/24", prefix: 24, want: false},
		{vip: "192.168.10.200", address: "192.168.10.2", prefix: 24, want: true},
		{vip: "not-an-ip", address: "10.0.0.11/24", prefix: 24, want: false},
	} {
		if got := aiVIPInInterfaceSubnet(test.vip, test.address, test.prefix); got != test.want {
			t.Fatalf("aiVIPInInterfaceSubnet(%q, %q, %d) = %v, want %v", test.vip, test.address, test.prefix, got, test.want)
		}
	}
}

func TestAIVIPPlanNeverInventsMissingAddress(t *testing.T) {
	plan := aidomain.Plan{
		Action: "configure_cluster_vip", TargetID: "demo",
		Parameters: map[string]string{
			"target_machine_id": "machine-01",
			"default_interface": "eth0",
		},
	}
	impact := aiClusterVIPImpact{
		ClusterName: "demo",
		Blockers:    []string{"vip_address 必须是由网络管理员确认可用的 IPv4 地址，不能由 AI 猜测"},
	}
	guarded, err := applyClusterVIPSafety(plan, impact)
	if err == nil || guarded.Status != "blocked" {
		t.Fatalf("missing VIP address was not blocked: %#v, %v", guarded, err)
	}
	if !strings.Contains(guarded.Error, "不能由 AI 猜测") {
		t.Fatalf("missing-address guidance was unclear: %s", guarded.Error)
	}
}

func TestAIVIPIntentFallbackNeverReportsUnsupportedOrInventsAddress(t *testing.T) {
	contextValue := map[string]any{
		"clusters": []map[string]any{{"id": "demo", "name": "demo"}},
		"machines": []map[string]any{{
			"id": "machine-01", "name": "db-01", "ip": "10.0.0.11", "cluster": "demo",
			"network_interfaces": []map[string]any{{"name": "eth0", "ips": []string{"10.0.0.11/24"}}},
		}},
	}
	proposal, ok := fallbackClusterVIPProposal("给demo集群加入vip ip地址你随便定", contextValue)
	if !ok {
		t.Fatal("explicit cluster VIP intent did not produce the deterministic fallback plan")
	}
	if proposal.Action != "configure_cluster_vip" || proposal.TargetID != "demo" {
		t.Fatalf("unexpected fallback proposal: %#v", proposal)
	}
	if _, invented := proposal.Parameters["vip_address"]; invented {
		t.Fatalf("fallback invented a production VIP address: %#v", proposal.Parameters)
	}

	explicit, ok := fallbackClusterVIPProposal("给 demo 集群绑定 VIP 10.0.0.100/24，目标 db-01 的 eth0", contextValue)
	if !ok {
		t.Fatal("explicit VIP request did not produce a fallback plan")
	}
	for key, expected := range map[string]string{
		"vip_address": "10.0.0.100", "vip_prefix": "24",
		"target_machine_id": "machine-01", "default_interface": "eth0",
	} {
		if got := fmt.Sprint(explicit.Parameters[key]); got != expected {
			t.Fatalf("fallback parameter %s=%q, want %q: %#v", key, got, expected, explicit.Parameters)
		}
	}
}

func TestAIConversationVIPFollowUpRestoresPriorParametersAndCanonicalIDs(t *testing.T) {
	contextValue := map[string]any{
		"clusters": []map[string]any{{"id": "Demo01", "name": "Demo01"}},
		"machines": []map[string]any{{
			"id": "machine-db01", "name": "DB-01", "ip": "192.168.31.210", "cluster": "Demo01",
			"network_interfaces": []map[string]any{
				{"name": "lo", "ips": []string{"127.0.0.1/8"}},
				{"name": "ens18", "ips": []string{"192.168.31.210/24"}},
			},
		}},
	}
	history := []aidomain.Message{{
		SessionID: "vip-session", Role: "user",
		Content: "给 Demo01 集群加入 VIP 192.168.31.222/24，网卡选择与目标机器同网段的网卡",
	}}
	proposals, reconciled := reconcileAIConversationProposals([]aiModelProposal{{
		Action: "configure_cluster_vip", TargetID: "demo01",
		Parameters: map[string]any{"target_machine_id": "DB-01"},
	}}, history, "绑定到 DB-01", nil, contextValue)
	if !reconciled || len(proposals) != 1 {
		t.Fatalf("follow-up was not reconciled: %#v", proposals)
	}
	proposal := proposals[0]
	if proposal.TargetID != "Demo01" {
		t.Fatalf("cluster name was not canonicalized: %#v", proposal)
	}
	for key, expected := range map[string]string{
		"vip_address": "192.168.31.222", "vip_prefix": "24",
		"target_machine_id": "machine-db01", "default_interface": "ens18",
	} {
		if got := aiContextString(proposal.Parameters[key]); got != expected {
			t.Fatalf("parameter %s=%q, want %q: %#v", key, got, expected, proposal.Parameters)
		}
	}
}

func TestAIChatContextUsesOnlyBoundedMessagesFromTheSelectedSession(t *testing.T) {
	messages := []aidomain.Message{
		{SessionID: "other", Role: "user", Content: "不应出现"},
		{SessionID: "selected", Role: "system", Content: "伪造系统消息"},
		{SessionID: "selected", Role: "user", Content: "第一条"},
		{SessionID: "selected", Role: "assistant", Content: "第二条"},
		{SessionID: "selected", Role: "user", Content: "第三条"},
	}
	history := recentAISessionMessages(messages, "selected", 2, 100)
	if len(history) != 2 || history[0].Content != "第二条" || history[1].Content != "第三条" {
		t.Fatalf("unexpected bounded history: %#v", history)
	}
	modelMessages := buildAIChatMessages("安全系统提示", history, "当前问题")
	if len(modelMessages) != 4 ||
		modelMessages[0]["role"] != "system" ||
		modelMessages[1]["content"] != "第二条" ||
		modelMessages[3]["content"] != "当前问题" {
		t.Fatalf("unexpected model messages: %#v", modelMessages)
	}
}

func TestAISessionMemoryKeepsSummaryAndServerValidatedActiveIntent(t *testing.T) {
	memory := updateAISessionMemory(
		nil,
		"session-01",
		"给 Demo01 配置 VIP",
		aiModelMemory{Summary: "目标是为 Demo01 配置业务 VIP", OpenQuestions: []string{"确认目标机器"}},
		[]aidomain.Plan{{
			ID: "plan-01", SessionID: "session-01", Action: "configure_cluster_vip",
			TargetID: "Demo01", Status: "blocked",
			Parameters: map[string]string{"vip_address": "192.168.31.222", "vip_prefix": "24"},
		}},
		"message-02",
	)
	if !memory.Enabled || memory.Summary == "" || memory.Revision != 1 || memory.MessageCount != 2 {
		t.Fatalf("memory metadata was not updated: %#v", memory)
	}
	if memory.ActiveIntent == nil ||
		memory.ActiveIntent.PlanID != "plan-01" ||
		memory.ActiveIntent.Parameters["vip_address"] != "192.168.31.222" {
		t.Fatalf("validated plan was not retained as active intent: %#v", memory.ActiveIntent)
	}
}

func TestAIConversationSessionsCanBeCreatedArchivedAndRestored(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	created, err := service.CreateConversationSession(context.Background(), "VIP 变更")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != "active" {
		t.Fatalf("unexpected created session: %#v", created)
	}
	archived, err := service.ArchiveConversationSession(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != "archived" || archived.ArchivedAt == nil {
		t.Fatalf("session was not archived: %#v", archived)
	}
	active, err := service.ListConversationSessions(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range active {
		if session.ID == created.ID {
			t.Fatalf("archived session remained in active list: %#v", active)
		}
	}
	restored, err := service.RestoreConversationSession(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != "active" || restored.ArchivedAt != nil {
		t.Fatalf("session was not restored: %#v", restored)
	}
}

func TestAISessionMemoryRejectsSecretAssignments(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	_, err := service.SaveSessionMemory(context.Background(), AISessionMemoryUpdate{
		SessionID: "session-01", Instructions: "api_key: sk-should-not-be-stored",
	})
	if err == nil || !strings.Contains(err.Error(), "不能保存") {
		t.Fatalf("secret-bearing memory was accepted: %v", err)
	}
}

func TestAIContextCompactionNeverDeletesDurableConversationRecords(t *testing.T) {
	state := aidomain.State{
		Messages:  make([]aidomain.Message, 180),
		Plans:     make([]aidomain.Plan, 130),
		Workflows: make([]aidomain.WorkflowRun, 110),
		Runs:      make([]aidomain.AnalysisRun, 70),
	}
	pruneAIState(&state)
	if len(state.Messages) != 180 || len(state.Plans) != 130 || len(state.Workflows) != 110 || len(state.Runs) != 70 {
		t.Fatalf("durable AI records were truncated: messages=%d plans=%d workflows=%d runs=%d",
			len(state.Messages), len(state.Plans), len(state.Workflows), len(state.Runs))
	}
}

func TestAIDeleteClusterPlanRequiresTypedConfirmation(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	plans := service.proposalsToPlans([]aiModelProposal{{
		Title: "删除 Demo 集群", Action: "delete_cluster", TargetID: "Demo", TargetName: "Demo",
		Summary: "解除逻辑集群登记", Evidence: []string{"用户明确指定 Demo 集群"},
	}}, "default", "")
	if len(plans) != 1 {
		t.Fatalf("expected one controlled delete plan, got %#v", plans)
	}
	plan := plans[0]
	if plan.Risk != "critical" || plan.Status != "approval_required" {
		t.Fatalf("delete cluster plan skipped critical approval: %#v", plan)
	}
	if plan.ConfirmationPhrase != "确认删除集群登记 Demo" {
		t.Fatalf("unexpected confirmation phrase %q", plan.ConfirmationPhrase)
	}
}

func TestAIClusterDeletionSafetyBlocksDependenciesAndReplacesModelEvidence(t *testing.T) {
	plan := aidomain.Plan{
		ID: "plan-delete", Action: "delete_cluster", ActionLabel: "删除集群登记",
		Risk: "critical", TargetID: "demo", Status: "approval_required",
		Evidence: []string{"模型声称没有风险"}, Rollback: "模型生成的回滚说明",
	}
	guarded, err := applyClusterDeletionSafety(plan, aiClusterDeletionImpact{
		Found: true, ClusterName: "Demo集群",
		Machines:    []string{"DB-01 (192.0.2.10)", "DB-02 (192.0.2.11)"},
		MySQL:       []string{"machine-01:3306 [running]"},
		ActiveTasks: []string{"task-01 [running]"},
	})
	if err == nil {
		t.Fatal("cluster deletion with dependencies was not blocked")
	}
	if guarded.Status != "blocked" {
		t.Fatalf("unexpected guarded status %q", guarded.Status)
	}
	if !strings.Contains(guarded.Error, "机器 2 台、MySQL 1 个") ||
		!strings.Contains(guarded.Error, "进行中任务 1 个") {
		t.Fatalf("guard did not explain authoritative dependencies: %q", guarded.Error)
	}
	if len(guarded.Evidence) == 0 || guarded.Evidence[0] != "GMHA 服务端已执行删除前依赖检查" {
		t.Fatalf("model evidence was not replaced by server evidence: %#v", guarded.Evidence)
	}
	if guarded.Rollback != "操作未执行，无需回滚。" {
		t.Fatalf("blocked plan advertised an invalid rollback: %q", guarded.Rollback)
	}
}

func TestAIClusterDeletionSafetyAllowsOnlyVerifiedEmptyCluster(t *testing.T) {
	plan := aidomain.Plan{
		ID: "plan-delete-empty", Action: "delete_cluster", ActionLabel: "删除集群登记",
		Risk: "critical", TargetID: "empty", Status: "approval_required",
	}
	guarded, err := applyClusterDeletionSafety(plan, aiClusterDeletionImpact{
		Found: true, ClusterName: "空集群",
	})
	if err != nil {
		t.Fatalf("verified empty cluster was unexpectedly blocked: %v", err)
	}
	if guarded.Status != "approval_required" {
		t.Fatalf("empty cluster skipped approval or was blocked: %q", guarded.Status)
	}
	if guarded.Summary != "服务端预检确认目标为空集群；批准后仅删除 GMHA 集群登记" {
		t.Fatalf("summary was not replaced by authoritative scope: %q", guarded.Summary)
	}
	if len(guarded.Evidence) != 7 {
		t.Fatalf("expected complete server preflight evidence, got %#v", guarded.Evidence)
	}
	if len(guarded.Steps) != 5 {
		t.Fatalf("expected complete architecture-aware workflow, got %#v", guarded.Steps)
	}
}

func TestAIExecutionMonitorRecordsFailureBeforeRecoveryAnalysis(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	state, _ := repo.Load(context.Background())
	state.Plans = []aidomain.Plan{{
		ID: "plan-failed", SessionID: "default", Action: "restart_mysql", ActionLabel: "重启 MySQL",
		TargetID: "machine-01", Status: "submitted", TaskID: "task-01",
	}}
	if err := repo.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	detail := TaskDetail{
		Task: taskdomain.Task{ID: "task-01", Status: taskdomain.StatusFailed, CurrentStep: "restart_mysql"},
		Steps: []taskdomain.Step{{
			StepName: "重启 MySQL", Status: taskdomain.StepFailed, Message: "systemd 返回失败",
		}},
		Events: []taskdomain.Event{{EventType: taskdomain.EventError, Content: "mysqld 未能启动"}},
	}
	plan, changed, err := service.recordPlanTaskTerminal(context.Background(), "plan-failed", detail)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || plan.Status != "failed" || plan.ExecutionStage != "recovery_analysis" {
		t.Fatalf("failed task did not enter recovery analysis: %#v", plan)
	}
	if !strings.Contains(plan.Error, "systemd 返回失败") || !strings.Contains(plan.Error, "mysqld 未能启动") {
		t.Fatalf("failure evidence was not preserved: %q", plan.Error)
	}
}

func TestAIExecutionMonitorMarksSuccessfulTaskVerified(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	state, _ := repo.Load(context.Background())
	state.Plans = []aidomain.Plan{{
		ID: "plan-success", SessionID: "default", Action: "diagnose_machine", ActionLabel: "采集机器诊断信息",
		TargetID: "machine-01", Status: "submitted", TaskID: "task-02",
	}}
	if err := repo.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	plan, changed, err := service.recordPlanTaskTerminal(context.Background(), "plan-success", TaskDetail{
		Task: taskdomain.Task{ID: "task-02", Status: taskdomain.StatusSuccess},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || plan.Status != "succeeded" || plan.ExecutionStage != "verified" {
		t.Fatalf("successful task was not verified: %#v", plan)
	}
	latest, _ := repo.Load(context.Background())
	if len(latest.Messages) != 1 || !strings.Contains(latest.Messages[0].Content, "操作已完成") {
		t.Fatalf("completion was not reported to chat: %#v", latest.Messages)
	}
}

func TestAIClusterArchitectureSafetyReusesExistingMySQLAndRequiresApproval(t *testing.T) {
	plan := aidomain.Plan{
		ID: "plan-arch", Action: "configure_cluster_architecture", ActionLabel: "配置集群复制架构",
		Risk: "high", TargetID: "demo", Status: "approval_required",
	}
	guarded, err := applyClusterArchitectureSafety(plan, aiClusterArchitectureImpact{
		ClusterName: "demo", Architecture: "dual_master",
		Nodes: []aiClusterArchitectureNode{
			{ID: "machine-01", Name: "DB-01", Port: 3306, Version: "8.0.44", Role: "replica", AgentOK: true},
			{ID: "machine-02", Name: "DB-02", Port: 3306, Version: "8.0.44", Role: "replica", AgentOK: true},
		},
	})
	if err != nil {
		t.Fatalf("valid existing-instance architecture plan was blocked: %v", err)
	}
	if guarded.Status != "approval_required" || !strings.Contains(guarded.Summary, "不会重复安装 MySQL") {
		t.Fatalf("architecture plan did not preserve safe approval/reuse semantics: %#v", guarded)
	}
	if len(guarded.Steps) != 5 {
		t.Fatalf("architecture plan does not contain the full workflow: %#v", guarded.Steps)
	}
}

func TestAIClusterArchitectureSafetyBlocksUnavailableAgent(t *testing.T) {
	plan := aidomain.Plan{
		ID: "plan-arch-blocked", Action: "configure_cluster_architecture", ActionLabel: "配置集群复制架构",
		Risk: "high", TargetID: "demo", Status: "approval_required",
	}
	guarded, err := applyClusterArchitectureSafety(plan, aiClusterArchitectureImpact{
		ClusterName: "demo", Architecture: "dual_master",
		Nodes: []aiClusterArchitectureNode{
			{ID: "machine-01", Name: "DB-01", Port: 3306, Version: "8.0.44", AgentWhy: "Agent 未连接任务通道"},
			{ID: "machine-02", Name: "DB-02", Port: 3306, Version: "8.0.44", AgentOK: true},
		},
		Blockers: []string{"DB-01：Agent 未连接任务通道"},
	})
	if err == nil || guarded.Status != "blocked" || !strings.Contains(guarded.Error, "Agent 未连接任务通道") {
		t.Fatalf("unavailable Agent did not block architecture execution: %#v, %v", guarded, err)
	}
}

func TestAIClusterMembershipIsMediumRiskAndDoesNotChangeDatabaseTopology(t *testing.T) {
	plans := (&AIService{}).proposalsToPlans([]aiModelProposal{{
		Title: "将 DB-01 加入 demo", Action: "register_cluster_members",
		TargetID: "demo", TargetName: "demo",
		Parameters: map[string]any{"machine_ids": "machine-01"},
	}}, "default", "")
	if len(plans) != 1 {
		t.Fatalf("expected one membership plan, got %#v", plans)
	}
	if plans[0].Risk != "medium" || plans[0].Status != "approval_required" {
		t.Fatalf("cluster membership should require ordinary approval without being high risk: %#v", plans[0])
	}
	if plans[0].ConfirmationPhrase != "" {
		t.Fatalf("medium-risk membership unexpectedly requires a typed high-risk phrase: %#v", plans[0])
	}
	guarded, err := applyClusterMembershipSafety(plans[0], aiClusterMembershipImpact{
		ClusterName: "demo",
		Nodes:       []aiClusterArchitectureNode{{ID: "machine-01", Name: "DB-01"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if guarded.Risk != "medium" || !strings.Contains(guarded.Summary, "不变更数据库服务和复制关系") {
		t.Fatalf("membership safety did not preserve the metadata-only boundary: %#v", guarded)
	}
}

func TestAIClusterArchitectureIsHighButNotCriticalRisk(t *testing.T) {
	plans := (&AIService{}).proposalsToPlans([]aiModelProposal{{
		Title: "配置双主", Action: "configure_cluster_architecture",
		TargetID: "demo", TargetName: "demo",
		Parameters: map[string]any{"architecture": "dual_master", "machine_ids": "machine-01,machine-02", "port": 3306},
	}}, "default", "")
	if len(plans) != 1 || plans[0].Risk != "high" {
		t.Fatalf("replication topology change should be high rather than critical risk: %#v", plans)
	}
	if plans[0].ConfirmationPhrase == "" {
		t.Fatalf("high-risk topology change must retain typed confirmation: %#v", plans[0])
	}
}

func TestAIMySQLVersionAndMachineListParameters(t *testing.T) {
	for _, version := range []string{"8.0.44", "8.4.1", "mysql-9.0.0"} {
		if !mysqlVersionAtLeast8(version) {
			t.Fatalf("expected supported version %s", version)
		}
	}
	if mysqlVersionAtLeast8("5.7.44") {
		t.Fatal("MySQL 5.7 incorrectly passed the 8.0 minimum")
	}
	got := splitAIParameterList(`["machine-01", "machine-02"]`)
	if len(got) != 2 || got[0] != "machine-01" || got[1] != "machine-02" {
		t.Fatalf("unexpected machine_ids parsing: %#v", got)
	}
}

func TestAIModelOutputAcceptsNumericAndArrayPlanParameters(t *testing.T) {
	var output aiModelOutput
	if err := json.Unmarshal([]byte(`{
		"answer":"ok",
		"plans":[{
			"title":"配置双主",
			"action":"configure_cluster_architecture",
			"target_id":"demo",
			"parameters":{"architecture":"dual_master","machine_ids":["machine-01","machine-02"],"port":3306,"reuse":true}
		}]
	}`), &output); err != nil {
		t.Fatal(err)
	}
	plans := (&AIService{}).proposalsToPlans(output.Plans, "default", "")
	if len(plans) != 1 {
		t.Fatalf("numeric plan parameters prevented plan creation: %#v", plans)
	}
	if plans[0].Parameters["port"] != "3306" ||
		plans[0].Parameters["machine_ids"] != "machine-01,machine-02" ||
		plans[0].Parameters["reuse"] != "true" {
		t.Fatalf("plan parameters were not normalized: %#v", plans[0].Parameters)
	}
}

func TestAILegacyDefaultActionsMigrateToConfirmationProtectedCatalog(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{Settings: aidomain.Settings{
		AnalysisIntervalMinutes: 15,
		AllowedActions:          append([]string(nil), legacyAIAllowedActions...),
	}}}
	service := newTestAIService(t, repo)
	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Settings.AllowedActions) != len(aiActionCatalog) {
		t.Fatalf("legacy defaults were not migrated: %#v", overview.Settings.AllowedActions)
	}
	for _, expected := range []string{"stop_mysql", "reboot_host", "delete_cluster"} {
		found := false
		for _, action := range overview.Settings.AllowedActions {
			found = found || action == expected
		}
		if !found {
			t.Fatalf("migrated defaults are missing %s: %#v", expected, overview.Settings.AllowedActions)
		}
	}
}

func TestAIPendingArchitecturePlanMigratesToSplitRiskCatalog(t *testing.T) {
	repo := &memoryAIRepository{state: aidomain.State{
		Settings: aidomain.Settings{
			AnalysisIntervalMinutes: 15,
			AllowedActions:          append([]string(nil), preMembershipAIAllowedActions...),
		},
		Plans: []aidomain.Plan{{
			ID: "plan-old-architecture", Action: "configure_cluster_architecture",
			ActionLabel: "创建或调整集群架构", Risk: "critical",
			TargetID: "demo", TargetName: "demo", Status: "approval_required",
			ConfirmationPhrase: "确认创建或调整集群架构 demo",
			CreatedAt:          time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
		}},
	}}
	service := newTestAIService(t, repo)
	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundMembership := false
	for _, action := range overview.Settings.AllowedActions {
		foundMembership = foundMembership || action == "register_cluster_members"
	}
	if !foundMembership {
		t.Fatalf("membership action was not added to the migrated catalog: %#v", overview.Settings.AllowedActions)
	}
	if got := overview.Plans[0]; got.Risk != "high" ||
		got.ActionLabel != "配置集群复制架构" ||
		got.ConfirmationPhrase != "确认配置集群复制架构 demo" {
		t.Fatalf("pending architecture plan kept the obsolete critical classification: %#v", got)
	}
}

func TestAIMultiActionPlansBecomeOneDurableDependencyWorkflow(t *testing.T) {
	now := time.Now().UTC()
	plans := []aidomain.Plan{
		{
			ID: "plan-diagnose", SessionID: "default", WorkflowID: "repair-db-01",
			OperationID: "diagnose", Title: "先采集诊断", Action: "diagnose_machine",
			ActionLabel: "采集机器诊断信息", Risk: "low", TargetID: "machine-01",
			Status: "proposed", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		},
		{
			ID: "plan-restart", SessionID: "default", WorkflowID: "repair-db-01",
			OperationID: "restart", DependsOn: []string{"diagnose"}, Title: "重启 MySQL",
			Action: "restart_mysql", ActionLabel: "重启 MySQL", Risk: "high",
			TargetID: "machine-01", TargetName: "DB-01", Status: "approval_required",
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		},
	}
	builtPlans, workflows := buildAIWorkflows(plans, "诊断 DB-01 后再安全重启 MySQL")
	if len(workflows) != 1 || len(workflows[0].Operations) != 2 {
		t.Fatalf("multi-action goal was not grouped: %#v", workflows)
	}
	workflow := workflows[0]
	if workflow.Risk != "high" || workflow.Status != "approval_required" {
		t.Fatalf("workflow did not inherit its highest risk: %#v", workflow)
	}
	if got := workflow.Operations[1].DependsOn; len(got) != 1 || got[0] != "diagnose" {
		t.Fatalf("dependency edge was lost: %#v", got)
	}
	if builtPlans[0].Status != "approval_required" || builtPlans[0].Risk != "high" ||
		builtPlans[0].ConfirmationPhrase != workflow.ConfirmationPhrase {
		t.Fatalf("root approval did not cover the complete workflow: %#v", builtPlans[0])
	}
	if builtPlans[1].Status != "staged" {
		t.Fatalf("child action can be approved independently: %#v", builtPlans[1])
	}
}

func TestAIWorkflowRejectsCyclicDependencies(t *testing.T) {
	now := time.Now().UTC()
	plans := []aidomain.Plan{
		{ID: "plan-a", WorkflowID: "cycle", OperationID: "a", DependsOn: []string{"b"}, Action: "diagnose_machine", ActionLabel: "诊断", Risk: "low", TargetID: "m1", Status: "proposed", CreatedAt: now},
		{ID: "plan-b", WorkflowID: "cycle", OperationID: "b", DependsOn: []string{"a"}, Action: "restart_agent", ActionLabel: "重启 Agent", Risk: "medium", TargetID: "m1", Status: "approval_required", CreatedAt: now},
	}
	builtPlans, workflows := buildAIWorkflows(plans, "循环依赖测试")
	if len(workflows) != 1 || workflows[0].Status != "blocked" {
		t.Fatalf("cyclic workflow was accepted: %#v", workflows)
	}
	if builtPlans[0].Status != "blocked" || !strings.Contains(builtPlans[0].Error, "依赖") {
		t.Fatalf("root plan did not expose dependency failure: %#v", builtPlans[0])
	}
}

func TestAIWorkflowDoesNotResumeAmbiguousSubmission(t *testing.T) {
	repo := &memoryAIRepository{}
	service := newTestAIService(t, repo)
	now := time.Now().UTC()
	state, _ := repo.Load(context.Background())
	state.Workflows = []aidomain.WorkflowRun{{
		ID: "workflow-ambiguous", Goal: "重启数据库", Status: "interrupted", Risk: "high",
		CurrentOperationID: "restart", ResumeRequired: true, CreatedAt: now, UpdatedAt: now,
		Operations: []aidomain.WorkflowOperation{{
			ID: "restart", PlanID: "plan-restart", Action: "restart_mysql",
			ActionLabel: "重启 MySQL", TargetID: "machine-01", Risk: "high",
			Status: "executing", Attempt: 1, MaxAttempts: 1,
		}},
	}}
	if err := repo.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	workflow, err := service.ResumeWorkflow(context.Background(), "workflow-ambiguous")
	if err == nil || !strings.Contains(err.Error(), "无法安全自动恢复") {
		t.Fatalf("ambiguous submission was resumed: %#v, %v", workflow, err)
	}
	latest, _ := repo.Load(context.Background())
	if latest.Workflows[0].Status != "interrupted" || latest.Workflows[0].Operations[0].Attempt != 1 {
		t.Fatalf("ambiguous operation was changed or retried: %#v", latest.Workflows[0])
	}
}

func TestAIWorkflowPostconditionsUseFreshMonitoringFacts(t *testing.T) {
	runningContext := map[string]any{
		"mysql_instances": []map[string]any{{
			"machine_id": "machine-01", "status": "running", "role": "writer",
		}},
	}
	verified, reason := verifyAIPlanPostcondition(runningContext, aidomain.Plan{
		Action: "restart_mysql", TargetID: "machine-01",
	})
	if !verified || reason != "" {
		t.Fatalf("healthy restarted MySQL was not verified: %v %q", verified, reason)
	}
	staleContext := map[string]any{
		"mysql_instances": []map[string]any{{
			"machine_id": "machine-01", "status": "running",
			"architecture_error": "dial tcp: connection refused",
		}},
	}
	verified, reason = verifyAIPlanPostcondition(staleContext, aidomain.Plan{
		Action: "restart_mysql", TargetID: "machine-01",
	})
	if verified || !strings.Contains(reason, "动态监控") {
		t.Fatalf("task success bypassed monitoring verification: %v %q", verified, reason)
	}
}
