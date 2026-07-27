package app

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	aidomain "gmha/internal/domain/ai"
)

// resolveAIFuzzyConversationProposals is the server-side grounding layer
// between model output and the action catalog. A model may understand the
// intent but return "第一个集群" instead of an opaque ID; this layer resolves
// that selector against the exact ordered snapshot that was shown to it.
func resolveAIFuzzyConversationProposals(
	proposals []aiModelProposal,
	history []aidomain.Message,
	prompt string,
	previousPlan *aidomain.Plan,
	contextValue map[string]any,
) ([]aiModelProposal, bool, []string) {
	conversationText := aiConversationUserText(history, prompt)
	changed := false
	notes := make([]string, 0, 2)

	for index := range proposals {
		proposal := &proposals[index]
		action, ok := lookupAIAction(proposal.Action)
		if !ok {
			continue
		}
		switch action.TargetKind {
		case "cluster":
			if cluster, note, resolved := resolveAIClusterReference(proposal.TargetID, conversationText, contextValue); resolved {
				if proposal.TargetID != cluster.id {
					changed = true
				}
				proposal.TargetID = cluster.id
				proposal.TargetName = firstNonEmptyAI(proposal.TargetName, cluster.name, cluster.id)
				notes = appendUniqueAIText(notes, note)
			}
			if reconcileAIMachineParameters(proposal, conversationText, contextValue) {
				changed = true
			}
		case "machine":
			clusterID := resolveAIClusterScope(conversationText, contextValue)
			if machine, note, resolved := resolveAIMachineReference(proposal.TargetID, conversationText, clusterID, contextValue); resolved {
				if proposal.TargetID != machine.id {
					changed = true
				}
				proposal.TargetID = machine.id
				proposal.TargetName = firstNonEmptyAI(proposal.TargetName, machine.name, machine.id)
				notes = appendUniqueAIText(notes, note)
			}
		}
	}

	inferredAction := inferAIFuzzyAction(prompt, previousPlan)
	if containsAnyAIText(strings.ToLower(prompt), "删除", "删掉", "移除", "去掉", "delete", "remove") &&
		!aiDeleteTargetsMachineSubresource(prompt) {
		if _, matched := matchAIMachineText(conversationText, aiContextMachines(contextValue, "")); matched {
			inferredAction = "delete_machine"
		}
	}
	if inferredAction != "" && !hasAIProposalAction(proposals, inferredAction) {
		if fallback, fallbackNotes, ok := fallbackAIFuzzyIntentProposal(
			inferredAction, conversationText, previousPlan, contextValue,
		); ok {
			proposals = append(proposals, fallback)
			changed = true
			for _, note := range fallbackNotes {
				notes = appendUniqueAIText(notes, note)
			}
		}
	}

	var expanded bool
	proposals, expanded, notes = expandAIMachineDeletionWorkflow(proposals, conversationText, contextValue, notes)
	return proposals, changed || expanded, notes
}

type aiResolvedCluster struct {
	id   string
	name string
}

type aiResolvedMachine struct {
	id      string
	name    string
	ip      string
	cluster string
}

func resolveAIClusterReference(selector, text string, contextValue map[string]any) (aiResolvedCluster, string, bool) {
	clusters := aiContextClusters(contextValue)
	if len(clusters) == 0 {
		return aiResolvedCluster{}, "", false
	}
	if cluster, ok := matchAIClusterText(selector, clusters); ok {
		return cluster, "", true
	}
	if ordinal, fromEnd, ok := findAIEntityOrdinal(firstNonEmptyAI(selector, text), `集群|cluster`); ok {
		index := ordinal - 1
		if fromEnd {
			index = len(clusters) - ordinal
		}
		if index >= 0 && index < len(clusters) {
			cluster := clusters[index]
			return cluster, fmt.Sprintf("模糊目标解析：%s集群为 %s（%s）", aiOrdinalLabel(ordinal, fromEnd), cluster.name, cluster.id), true
		}
		return aiResolvedCluster{}, "", false
	}
	if cluster, ok := matchAIClusterText(text, clusters); ok {
		return cluster, "", true
	}
	return aiResolvedCluster{}, "", false
}

func resolveAIClusterScope(text string, contextValue map[string]any) string {
	cluster, _, ok := resolveAIClusterReference("", text, contextValue)
	if !ok {
		return ""
	}
	return cluster.id
}

func resolveAIMachineReference(selector, text, clusterID string, contextValue map[string]any) (aiResolvedMachine, string, bool) {
	machines := aiContextMachines(contextValue, clusterID)
	if len(machines) == 0 {
		return aiResolvedMachine{}, "", false
	}
	if _, _, clusterOrdinalRequested := findAIEntityOrdinal(text, `集群|cluster`); clusterOrdinalRequested && clusterID == "" {
		// An out-of-range cluster selector must not silently degrade into a
		// global machine ordinal.
		return aiResolvedMachine{}, "", false
	}
	if machine, ok := matchAIMachineText(selector, machines); ok {
		return machine, "", true
	}
	searchText := firstNonEmptyAI(selector, text)
	if ordinal, fromEnd, ok := findAIEntityOrdinal(searchText, `机器|主机|节点|服务器|machine|host|node`); ok {
		index := ordinal - 1
		if fromEnd {
			index = len(machines) - ordinal
		}
		if index >= 0 && index < len(machines) {
			machine := machines[index]
			scope := "机器列表"
			if clusterID != "" {
				scope = "集群 " + clusterID
			}
			return machine, fmt.Sprintf("模糊目标解析：%s中的%s机器为 %s（%s，%s）", scope, aiOrdinalLabel(ordinal, fromEnd), machine.name, machine.id, machine.ip), true
		}
		return aiResolvedMachine{}, "", false
	}
	// Some models return target_id="第一个" after already selecting a machine
	// action. In that constrained position the generic ordinal is unambiguous.
	if ordinal, fromEnd, ok := findAIEntityOrdinal(selector, ``); ok {
		index := ordinal - 1
		if fromEnd {
			index = len(machines) - ordinal
		}
		if index >= 0 && index < len(machines) {
			machine := machines[index]
			return machine, fmt.Sprintf("模糊目标解析：%s机器为 %s（%s，%s）", aiOrdinalLabel(ordinal, fromEnd), machine.name, machine.id, machine.ip), true
		}
	}
	if machine, ok := matchAIMachineText(text, machines); ok {
		return machine, "", true
	}
	if len(machines) == 1 && containsAnyAIText(strings.ToLower(text), "这个机器", "该机器", "这台机器", "这个主机", "该节点", "它") {
		return machines[0], fmt.Sprintf("上下文目标解析：唯一候选机器为 %s（%s，%s）", machines[0].name, machines[0].id, machines[0].ip), true
	}
	return aiResolvedMachine{}, "", false
}

func reconcileAIMachineParameters(proposal *aiModelProposal, text string, contextValue map[string]any) bool {
	if proposal.Parameters == nil {
		proposal.Parameters = map[string]any{}
	}
	changed := false
	clusterID := proposal.TargetID
	switch proposal.Action {
	case "register_cluster_members", "remove_cluster_members", "configure_cluster_architecture":
		values := aiSelectorList(proposal.Parameters["machine_ids"])
		resolved := make([]string, 0, len(values))
		for _, value := range values {
			if machine, _, ok := resolveAIMachineReference(value, text, clusterID, contextValue); ok {
				resolved = append(resolved, machine.id)
				if machine.id != value {
					changed = true
				}
			}
		}
		if len(resolved) == 0 {
			if machine, _, ok := resolveAIMachineReference("", text, clusterID, contextValue); ok {
				resolved = append(resolved, machine.id)
				changed = true
			}
		}
		if len(resolved) > 0 {
			proposal.Parameters["machine_ids"] = strings.Join(resolved, ",")
		}
	case "configure_cluster_vip":
		value := aiContextString(proposal.Parameters["target_machine_id"])
		if machine, _, ok := resolveAIMachineReference(value, text, clusterID, contextValue); ok && machine.id != value {
			proposal.Parameters["target_machine_id"] = machine.id
			changed = true
		}
	}
	return changed
}

func aiSelectorList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := aiContextString(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return splitAIParameterList(aiContextString(value))
	}
}

func fallbackAIFuzzyIntentProposal(
	actionID, text string,
	previousPlan *aidomain.Plan,
	contextValue map[string]any,
) (aiModelProposal, []string, bool) {
	action, ok := lookupAIAction(actionID)
	if !ok {
		return aiModelProposal{}, nil, false
	}
	proposal := aiModelProposal{
		Action: action.ID, Title: action.Label, Summary: "由 GMHA 根据平台实时对象列表解析模糊目标后生成",
		Parameters: map[string]any{},
		Evidence:   []string{"用户表达了明确操作意图；目标由服务端对象解析器定位，执行前仍会重新预检"},
	}
	notes := make([]string, 0, 2)
	switch action.TargetKind {
	case "cluster":
		cluster, note, resolved := resolveAIClusterReference("", text, contextValue)
		if !resolved && previousPlan != nil {
			cluster, note, resolved = resolveAIClusterReference(previousPlan.TargetID, text, contextValue)
		}
		if !resolved {
			return aiModelProposal{}, nil, false
		}
		proposal.TargetID, proposal.TargetName = cluster.id, cluster.name
		notes = appendUniqueAIText(notes, note)
	case "machine":
		clusterID := resolveAIClusterScope(text, contextValue)
		selector := ""
		if previousPlan != nil {
			if previousAction, found := lookupAIAction(previousPlan.Action); found && previousAction.TargetKind == "machine" {
				selector = previousPlan.TargetID
			}
		}
		machine, note, resolved := resolveAIMachineReference(selector, text, clusterID, contextValue)
		if !resolved {
			return aiModelProposal{}, nil, false
		}
		proposal.TargetID, proposal.TargetName = machine.id, machine.name
		notes = appendUniqueAIText(notes, note)
	}
	if action.ID == "delete_machine" {
		deleteMySQL, deleteAgent := explicitAIMachineCleanupScope(text)
		detachOnly := !deleteMySQL && !deleteAgent
		proposal.Parameters["detach_only"] = strconv.FormatBool(detachOnly)
		proposal.Parameters["delete_mysql"] = strconv.FormatBool(deleteMySQL)
		proposal.Parameters["delete_agent"] = strconv.FormatBool(deleteAgent)
	}
	for _, note := range notes {
		if note != "" {
			proposal.Evidence = append(proposal.Evidence, note)
		}
	}
	return proposal, notes, true
}

func expandAIMachineDeletionWorkflow(
	proposals []aiModelProposal,
	text string,
	contextValue map[string]any,
	notes []string,
) ([]aiModelProposal, bool, []string) {
	changed := false
	for index := 0; index < len(proposals); index++ {
		proposal := &proposals[index]
		if proposal.Action != "delete_machine" {
			continue
		}
		machine, ok := matchAIMachineText(proposal.TargetID, aiContextMachines(contextValue, ""))
		if !ok {
			continue
		}
		if proposal.Parameters == nil {
			proposal.Parameters = map[string]any{}
		}
		if aiContextString(proposal.Parameters["detach_only"]) == "" {
			deleteMySQL, deleteAgent := explicitAIMachineCleanupScope(text)
			proposal.Parameters["detach_only"] = strconv.FormatBool(!deleteMySQL && !deleteAgent)
			proposal.Parameters["delete_mysql"] = strconv.FormatBool(deleteMySQL)
			proposal.Parameters["delete_agent"] = strconv.FormatBool(deleteAgent)
			changed = true
		}
		if machine.cluster == "" || hasAIMemberRemovalForMachine(proposals, machine.cluster, machine.id) {
			continue
		}
		workflowKey := firstNonEmptyAI(proposal.WorkflowKey, "delete-machine-"+machine.id)
		removeOperationID := "remove-from-cluster"
		deleteOperationID := firstNonEmptyAI(proposal.OperationID, "delete-machine")
		remove := aiModelProposal{
			Action: "remove_cluster_members", Title: "先将机器移出集群",
			Summary:  fmt.Sprintf("机器 %s 当前属于集群 %s，删除前必须先安全解除集群归属", machine.name, machine.cluster),
			TargetID: machine.cluster, TargetName: machine.cluster,
			Parameters: map[string]any{"machine_ids": machine.id},
			Evidence: []string{
				fmt.Sprintf("服务端解析目标机器：%s（%s，%s）", machine.name, machine.id, machine.ip),
				"机器删除服务禁止直接删除仍属于集群的成员",
			},
			WorkflowKey: workflowKey, OperationID: removeOperationID,
		}
		proposal.WorkflowKey = workflowKey
		proposal.OperationID = deleteOperationID
		proposal.DependsOn = appendUniqueAIText(proposal.DependsOn, removeOperationID)
		proposal.Parameters["expected_cluster_removal"] = machine.cluster
		proposals = append(proposals[:index], append([]aiModelProposal{remove}, proposals[index:]...)...)
		index++
		changed = true
		notes = appendUniqueAIText(notes, fmt.Sprintf("依赖解析：%s 当前属于集群 %s，工作流将先移出集群再删除机器", machine.name, machine.cluster))
	}
	return proposals, changed, notes
}

func inferAIFuzzyAction(prompt string, previousPlan *aidomain.Plan) string {
	text := strings.ToLower(strings.TrimSpace(prompt))
	hasMachine := containsAnyAIText(text, "机器", "主机", "节点", "服务器", "machine", "host", "node")
	hasCluster := containsAnyAIText(text, "集群", "cluster")
	hasDelete := containsAnyAIText(text, "删除", "删掉", "移除", "去掉", "取消纳管", "解除纳管", "delete", "remove")
	hasRestart := containsAnyAIText(text, "重启", "重新启动", "restart")
	hasStop := containsAnyAIText(text, "停止", "停掉", "关闭", "stop")
	switch {
	case hasDelete && hasMachine && !aiDeleteTargetsMachineSubresource(text):
		return "delete_machine"
	case hasDelete && hasCluster:
		return "delete_cluster"
	case hasMachine && hasRestart && containsAnyAIText(text, "agent", "代理"):
		return "restart_agent"
	case hasMachine && hasRestart && containsAnyAIText(text, "mysql", "数据库"):
		return "restart_mysql"
	case hasMachine && hasStop && containsAnyAIText(text, "mysql", "数据库"):
		return "stop_mysql"
	case hasMachine && (containsAnyAIText(text, "reboot") || hasRestart):
		return "reboot_host"
	case hasMachine && containsAnyAIText(text, "诊断", "检查", "采集信息", "看看状态", "diagnose"):
		return "diagnose_machine"
	}
	if hasDelete && previousPlan != nil {
		if action, ok := lookupAIAction(previousPlan.Action); ok {
			if action.TargetKind == "machine" {
				return "delete_machine"
			}
			if action.TargetKind == "cluster" {
				return "delete_cluster"
			}
		}
	}
	return ""
}

func aiDeleteTargetsMachineSubresource(text string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(text), ""))
	if containsAnyAIText(lower, "彻底删除", "完全删除", "整台机器", "整台主机", "取消纳管", "解除纳管", "连同mysql", "连同agent", "wipe") {
		return false
	}
	subresource := `(?:mysql|数据库|agent|代理|vip|备份|索引|账号|用户|进程|服务|实例)`
	return regexp.MustCompile(`(?:机器|主机|节点|服务器|machine|host|node|[a-z0-9_.-]+)(?:上|中|里|内|的|上的|里的).{0,12}` + subresource).MatchString(lower)
}

func explicitAIMachineCleanupScope(text string) (deleteMySQL, deleteAgent bool) {
	lower := strings.ToLower(text)
	fullCleanup := containsAnyAIText(lower, "彻底删除", "完全删除", "全部清理", "连同软件", "清空机器", "wipe")
	deleteMySQL = fullCleanup || (containsAnyAIText(lower, "mysql", "数据库", "数据目录") &&
		containsAnyAIText(lower, "卸载", "清理", "删除", "一起", "连同"))
	deleteAgent = fullCleanup || (containsAnyAIText(lower, "agent", "代理") &&
		containsAnyAIText(lower, "卸载", "清理", "删除", "一起", "连同"))
	if containsAnyAIText(lower, "只解除纳管", "仅解除纳管", "只删记录", "保留软件", "保留mysql", "保留 mysql", "detach only") {
		return false, false
	}
	return deleteMySQL, deleteAgent
}

func aiContextClusters(contextValue map[string]any) []aiResolvedCluster {
	rows, _ := contextValue["clusters"].([]map[string]any)
	out := make([]aiResolvedCluster, 0, len(rows))
	for _, row := range rows {
		id := aiContextString(row["id"])
		if id == "" {
			continue
		}
		out = append(out, aiResolvedCluster{id: id, name: firstNonEmptyAI(aiContextString(row["name"]), id)})
	}
	return out
}

func aiContextMachines(contextValue map[string]any, clusterID string) []aiResolvedMachine {
	rows, _ := contextValue["machines"].([]map[string]any)
	out := make([]aiResolvedMachine, 0, len(rows))
	for _, row := range rows {
		cluster := aiContextString(row["cluster"])
		if clusterID != "" && cluster != clusterID {
			continue
		}
		id := aiContextString(row["id"])
		if id == "" {
			continue
		}
		out = append(out, aiResolvedMachine{
			id: id, name: firstNonEmptyAI(aiContextString(row["name"]), id),
			ip: aiContextString(row["ip"]), cluster: cluster,
		})
	}
	return out
}

func matchAIClusterText(text string, clusters []aiResolvedCluster) (aiResolvedCluster, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return aiResolvedCluster{}, false
	}
	matches := make([]aiResolvedCluster, 0, 1)
	for _, cluster := range clusters {
		if equalOrContainsAIEntity(text, cluster.id, cluster.name) {
			matches = append(matches, cluster)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return aiResolvedCluster{}, false
}

func matchAIMachineText(text string, machines []aiResolvedMachine) (aiResolvedMachine, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return aiResolvedMachine{}, false
	}
	matches := make([]aiResolvedMachine, 0, 1)
	for _, machine := range machines {
		if equalOrContainsAIEntity(text, machine.id, machine.name, machine.ip) {
			matches = append(matches, machine)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return aiResolvedMachine{}, false
}

func equalOrContainsAIEntity(text string, values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if text == value || (len([]rune(value)) >= 2 && strings.Contains(text, value)) {
			return true
		}
	}
	return false
}

func findAIEntityOrdinal(text, nounPattern string) (ordinal int, fromEnd bool, ok bool) {
	text = strings.ToLower(strings.Join(strings.Fields(text), ""))
	if text == "" {
		return 0, false, false
	}
	noun := nounPattern
	if noun == "" {
		noun = `(?:个|台|组|套)?`
	} else {
		noun = `(?:` + noun + `)`
	}
	number := `([0-9]+|[一二两三四五六七八九十]+)`
	patterns := []struct {
		expression string
		fromEnd    bool
	}{
		{`倒数第` + number + `(?:个|台|组|套)?` + noun, true},
		{`(?:` + noun + `)(?:列表)?(?:中|里|内|的)?倒数第` + number, true},
		{`第` + number + `(?:个|台|组|套)?` + noun, false},
		{`(?:` + noun + `)(?:列表)?(?:中|里|内|的)?(?:排)?第` + number, false},
		{number + `号` + noun, false},
	}
	for _, item := range patterns {
		re := regexp.MustCompile(item.expression)
		match := re.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		if parsed := parseAIOrdinal(match[1]); parsed > 0 {
			return parsed, item.fromEnd, true
		}
	}
	firstPatterns := []string{`首(?:个|台|组)?`, `头(?:个|一台|一组)?`, `最前面(?:的)?`, `排最前(?:的)?`}
	lastPatterns := []string{`最后(?:一个|一台|一组|的)?`, `最末(?:一个|一台|一组|的)?`, `末尾(?:的)?`}
	for _, prefix := range firstPatterns {
		if regexp.MustCompile(prefix + noun).MatchString(text) {
			return 1, false, true
		}
	}
	for _, prefix := range lastPatterns {
		if regexp.MustCompile(prefix + noun).MatchString(text) {
			return 1, true, true
		}
	}
	return 0, false, false
}

func parseAIOrdinal(value string) int {
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	digits := map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(value)
	if len(runes) == 1 {
		if runes[0] == '十' {
			return 10
		}
		return digits[runes[0]]
	}
	if len(runes) == 2 && runes[0] == '十' {
		return 10 + digits[runes[1]]
	}
	if len(runes) == 2 && runes[1] == '十' {
		return digits[runes[0]] * 10
	}
	if len(runes) == 3 && runes[1] == '十' {
		return digits[runes[0]]*10 + digits[runes[2]]
	}
	return 0
}

func aiOrdinalLabel(ordinal int, fromEnd bool) string {
	if fromEnd {
		return fmt.Sprintf("倒数第 %d 个", ordinal)
	}
	return fmt.Sprintf("第 %d 个", ordinal)
}

func hasAIProposalAction(proposals []aiModelProposal, action string) bool {
	for _, proposal := range proposals {
		if proposal.Action == action {
			return true
		}
	}
	return false
}

func hasAIMemberRemovalForMachine(proposals []aiModelProposal, clusterID, machineID string) bool {
	for _, proposal := range proposals {
		if proposal.Action != "remove_cluster_members" || proposal.TargetID != clusterID {
			continue
		}
		for _, value := range splitAIParameterList(aiContextString(proposal.Parameters["machine_ids"])) {
			if value == machineID {
				return true
			}
		}
	}
	return false
}

func appendUniqueAIText(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
