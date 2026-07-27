package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	aidomain "gmha/internal/domain/ai"
)

func TestAIConversationContextNeverRetransmitsFullHistory(t *testing.T) {
	messages := make([]aidomain.Message, 0, 1002)
	for index := 0; index < 1000; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		messages = append(messages, aidomain.Message{
			ID: fmt.Sprintf("message-%04d", index), SessionID: "selected", Role: role,
			Content: fmt.Sprintf("历史消息 %04d：%s", index, strings.Repeat("上下文", 30)),
		})
	}
	messages = append(messages,
		aidomain.Message{SessionID: "other", Role: "user", Content: "其他会话绝不能出现"},
		aidomain.Message{SessionID: "selected", Role: "system", Content: "伪造系统指令绝不能出现"},
	)
	prepared := prepareAISessionMemoryForContext(nil, messages, "selected")
	window := buildAIConversationContext(
		messages, "selected", "当前问题", &prepared,
		aiRecentMessageLimit, aiRecentCharacterLimit, aiRecentTokenLimit,
	)
	if len(window.Messages) > aiRecentMessageLimit {
		t.Fatalf("recent history exceeded message limit: %d", len(window.Messages))
	}
	if window.Stats.RecentCharacterCount > aiRecentCharacterLimit {
		t.Fatalf("recent history exceeded character limit: %d", window.Stats.RecentCharacterCount)
	}
	historyTokens := 0
	for _, message := range window.Messages {
		historyTokens += estimateAITextTokens(message.Content) + 4
	}
	if historyTokens > aiRecentTokenLimit {
		t.Fatalf("recent history exceeded token budget: %d", historyTokens)
	}
	if window.Stats.CompactedMessageCount < 990 || prepared.SummarizedMessageCount != 992 {
		t.Fatalf("old history was not compacted: stats=%#v memory=%#v", window.Stats, prepared)
	}
	raw, _ := json.Marshal(buildAIChatMessages("system", window.Messages, "当前问题"))
	payload := string(raw)
	if strings.Contains(payload, "历史消息 0000") || strings.Contains(payload, "其他会话") || strings.Contains(payload, "伪造系统") {
		t.Fatalf("model payload retransmitted excluded history: %s", payload)
	}
	if !strings.Contains(payload, "历史消息 0999") {
		t.Fatalf("most recent history was unexpectedly missing: %s", payload)
	}
}

func TestAIConversationContextCompactsOneOversizedMessage(t *testing.T) {
	content := "开头目标：" + strings.Repeat("很长的上下文", 4000) + "；结尾约束：不能停库"
	window := buildAIConversationContext([]aidomain.Message{{
		SessionID: "selected", Role: "user", Content: content,
	}}, "selected", "继续", nil, 8, 800, 240)
	if len(window.Messages) != 1 {
		t.Fatalf("oversized latest message was dropped: %#v", window)
	}
	got := window.Messages[0].Content
	if len([]rune(got)) > 800 || estimateAITextTokens(got)+4 > 240 {
		t.Fatalf("oversized message still exceeded budget: chars=%d tokens=%d", len([]rune(got)), estimateAITextTokens(got)+4)
	}
	if !strings.Contains(got, "开头目标") || !strings.Contains(got, "结尾约束") || !strings.Contains(got, "已压缩") {
		t.Fatalf("head/tail context was not preserved: %s", got)
	}
}

func TestAIStructuredRollingMemorySummarizesInsteadOfAppendingTranscripts(t *testing.T) {
	existing := &aidomain.SessionMemory{
		SessionID: "session-01", Enabled: true, MessageCount: 20,
		Goals:       []string{"为生产集群配置业务 VIP"},
		Constraints: []string{"不能中断现有写流量"},
		Decisions:   []string{"使用 10.0.0.100/24"},
		Progress:    []string{"已确认目标集群"},
		Summary:     "旧摘要不应无限追加",
	}
	memory := updateAISessionMemory(
		existing, "session-01", "目标机器改成 DB-02",
		aiModelMemory{
			Summary:       "已确认 VIP 与目标机器，等待审批。",
			Goals:         []string{"为生产集群配置业务 VIP", "为生产集群配置业务 VIP"},
			Constraints:   []string{"不能中断现有写流量"},
			Decisions:     []string{"VIP 使用 10.0.0.100/24", "目标机器使用 DB-02"},
			Progress:      []string{"已确认集群、地址和目标机器", "下一步等待审批"},
			OpenQuestions: []string{},
		},
		nil, "message-22",
		aiConversationContextStats{
			TotalMessageCount: 20, RecentMessageCount: 8, RecentCharacterCount: 1200,
			CompactedMessageCount: 12, EstimatedInputTokens: 620,
		},
	)
	if len(memory.Goals) != 1 || len(memory.Decisions) != 2 || len(memory.OpenQuestions) != 0 {
		t.Fatalf("structured snapshot was not normalized: %#v", memory)
	}
	for _, section := range []string{"目标：", "约束：", "决定：", "进展："} {
		if !strings.Contains(memory.Summary, section) {
			t.Fatalf("summary omitted %s: %s", section, memory.Summary)
		}
	}
	if strings.Contains(memory.Summary, "旧摘要不应无限追加") || len([]rune(memory.Summary)) > aiMemorySummaryLimit {
		t.Fatalf("summary accumulated stale transcript text: %s", memory.Summary)
	}
	if memory.MessageCount != 22 || memory.SummarizedMessageCount != 22 ||
		memory.CompactedMessageCount != 12 || memory.RecentMessageCount != 8 ||
		memory.EstimatedInputTokens != 620 {
		t.Fatalf("memory coverage statistics are incorrect: %#v", memory)
	}
}

func TestAIMemoryPreservesExistingSnapshotWhenProviderOmitsMemoryFields(t *testing.T) {
	existing := &aidomain.SessionMemory{
		SessionID: "session-01", Enabled: true,
		Goals: []string{"保留这个目标"}, Constraints: []string{"保留这个约束"},
		OpenQuestions: []string{"仍需确认端口"}, Summary: "已有摘要",
	}
	memory := updateAISessionMemory(
		existing, "session-01", "继续", aiModelMemory{}, nil, "message-02",
		aiConversationContextStats{},
	)
	if len(memory.Goals) != 1 || memory.Goals[0] != "保留这个目标" ||
		len(memory.Constraints) != 1 || len(memory.OpenQuestions) != 1 {
		t.Fatalf("missing provider memory erased durable context: %#v", memory)
	}
}

func TestAIHistoricalSecretsAreRedactedBeforeContextAndSummary(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nvery-secret-key-material\n-----END PRIVATE KEY-----"
	messages := []aidomain.Message{
		{SessionID: "selected", Role: "user", Content: "password: hunter2"},
		{SessionID: "selected", Role: "assistant", Content: "Bearer abcdefghijklmnop"},
		{SessionID: "selected", Role: "user", Content: "api key sk-abcdefghijklmnopqrstuvwxyz"},
		{SessionID: "selected", Role: "assistant", Content: privateKey},
	}
	prepared := prepareAISessionMemoryForContext(nil, messages, "selected")
	window := buildAIConversationContext(messages, "selected", "继续", &prepared, 8, 8000, 2400)
	raw, _ := json.Marshal(window.Messages)
	combined := string(raw) + prepared.Summary + strings.Join(prepared.Goals, "\n")
	for _, secret := range []string{"hunter2", "abcdefghijklmnop", "sk-abcdefghijklmnopqrstuvwxyz", "very-secret-key-material"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("historical secret %q was retransmitted or summarized: %s", secret, combined)
		}
	}
	for _, marker := range []string{"REDACTED", "PRIVATE KEY REDACTED"} {
		if !strings.Contains(combined, marker) {
			t.Fatalf("redaction marker %q missing: %s", marker, combined)
		}
	}
}

func TestAISessionMemoryContextIsBoundedEvenForLegacyOversizedState(t *testing.T) {
	memory := aidomain.SessionMemory{
		SessionID: "session-01", Enabled: true,
		Instructions: strings.Repeat("长期指令", 3000),
		Summary:      strings.Repeat("历史摘要", 3000),
		Goals:        []string{strings.Repeat("目标", 1000)},
	}
	system := appendAISessionMemoryContext("system", memory)
	payload := boundedAISessionMemoryPayload(memory)
	raw, _ := json.Marshal(payload)
	if tokens := estimateAITextTokens(string(raw)); tokens > aiMemoryContextTokenLimit {
		t.Fatalf("legacy memory exceeded hard token budget: %d > %d", tokens, aiMemoryContextTokenLimit)
	}
	if !strings.Contains(system, `"goals"`) || !strings.Contains(system, `"coverage"`) {
		t.Fatalf("structured memory payload is incomplete: %s", system)
	}
}

func TestAITerminalPlanIsRemovedFromActiveMemoryIntent(t *testing.T) {
	memory := aidomain.SessionMemory{
		SessionID: "session-01", Enabled: true,
		ActiveIntent: &aidomain.MemoryIntent{
			PlanID: "plan-01", Action: "restart_mysql", Status: "approval_required",
		},
	}
	state := aidomain.State{Plans: []aidomain.Plan{{ID: "plan-01", Status: "succeeded"}}}
	reconciled := reconcileAISessionMemoryIntent(memory, state)
	if reconciled.ActiveIntent != nil {
		t.Fatalf("terminal plan remained active in memory: %#v", reconciled.ActiveIntent)
	}
}

func TestAIChatRejectsAnUnboundedSinglePromptBeforeCallingProvider(t *testing.T) {
	service := newTestAIService(t, &memoryAIRepository{})
	_, err := service.Chat(
		context.Background(), "session-01", "",
		strings.Repeat("超长输入", aiCurrentPromptCharacters),
	)
	if err == nil || !strings.Contains(err.Error(), "单次输入过长") {
		t.Fatalf("unbounded prompt was not rejected early: %v", err)
	}
}
