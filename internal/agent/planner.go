package agent

import (
	"context"
	"fmt"
	"strings"
)

type RulePlanner struct{}

// NewRulePlanner creates the deterministic MVP planner used by the agent.
func NewRulePlanner() *RulePlanner {
	return &RulePlanner{}
}

// BuildInitialPlan creates a safe, read-oriented diagnostic plan from the
// requested fault type and available tool set.
func (p *RulePlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	_ = ctx
	available := map[string]bool{}
	for _, tool := range tools {
		available[tool.Name] = true
		available[canonicalToolName(tool.Name)] = true
	}
	fault := normalizeFault(goal.ExpectedFault)
	steps := []PlanStep{}
	add := func(tool, reason string, critical bool, input map[string]interface{}) {
		if !available[tool] {
			return
		}
		steps = append(steps, PlanStep{
			ID:       fmt.Sprintf("step-%02d", len(steps)+1),
			ToolName: tool,
			Reason:   reason,
			Critical: critical,
			Input:    input,
		})
	}

	baseInput := map[string]interface{}{
		"namespace": goal.Namespace,
		"pod_name":  goal.PodName,
	}
	add("k8s.get_pod", "读取 Pod 权威快照", true, baseInput)
	add("runbook.search", "检索诊断手册中的计划提示", false, map[string]interface{}{"fault_type": goal.ExpectedFault, "query": goal.AlertName})

	switch fault {
	case "oomkilled":
		add("k8s.get_logs", "查看 OOM 重启前后的容器日志", false, baseInput)
		add("prometheus.query_range", "查看故障窗口内的内存工作集趋势", false, map[string]interface{}{"query": "container_memory_working_set_bytes"})
		add("loki.query_logs", "查看集中式日志中的 OOM 相关信息", false, baseInput)
		add("k8s.get_topology", "检查工作负载和节点压力上下文", false, baseInput)
	case "crashloopbackoff":
		add("k8s.get_logs", "通过 previous logs 判断是启动失败还是运行期崩溃", false, baseInput)
		add("loki.query_logs", "查看集中式日志中的依赖或配置错误", false, baseInput)
		add("k8s.get_events", "检查 BackOff 和失败事件", false, baseInput)
		add("k8s.get_topology", "检查工作负载影响和同组 Pod 健康状态", false, baseInput)
	case "probefailed":
		add("k8s.get_events", "检查 Readiness/Liveness 探针事件", false, baseInput)
		add("k8s.get_logs", "查看应用健康检查端点相关日志", false, baseInput)
		add("loki.query_logs", "查看探针失败前后的集中式日志", false, baseInput)
		add("k8s.get_topology", "检查 Service Endpoints 和影响范围", false, baseInput)
	case "pending", "podpending":
		add("k8s.get_events", "检查调度器失败事件", false, baseInput)
		add("k8s.get_pvc", "直接检查 PVC 绑定状态", false, baseInput)
		add("k8s.get_topology", "检查调度约束和节点上下文", false, baseInput)
	case "nodenotready":
		add("k8s.get_topology", "检查节点健康和压力条件", true, baseInput)
		add("k8s.get_events", "采集节点相关事件", false, baseInput)
	default:
		add("k8s.get_events", "采集通用 Pod 事件", false, baseInput)
		add("k8s.get_logs", "采集通用容器日志", false, baseInput)
		add("k8s.get_pvc", "如存在存储约束则检查 PVC", false, baseInput)
		add("k8s.get_topology", "采集工作负载影响上下文", false, baseInput)
	}

	applyDefaultParallelGroups(steps)
	return Plan{
		Summary:              "基于规则的 Kubernetes 只读诊断计划",
		Steps:                steps,
		ExpectedObservations: expectedObservations(fault),
		StopCondition: []string{
			StopReasonMaxSteps,
			StopReasonConfirmedHypothesis,
			StopReasonTimeout,
			StopReasonNoEffectiveTool,
			StopReasonCriticalToolFailed,
		},
	}
}

// AdjustPlan updates pending steps after each observation. It only appends or
// skips read-only tools and never changes safety policy.
func (p *RulePlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	if plan == nil {
		return
	}
	if !last.Success {
		markUnavailable(plan, last.ToolName)
		return
	}
	if last.ToolName == "runbook.search" {
		for _, tool := range recommendedTools(last.Data) {
			appendIfMissing(plan, tool, "根据诊断手册提示追加采集步骤", state)
		}
	}
	for _, hypothesis := range hypotheses {
		for _, missing := range hypothesis.MissingEvidence {
			appendEvidenceCollectionSteps(plan, missing, "假设分析需要补充区分性证据", state)
		}
	}
}

func appendDistinguishingEvidence(plan *Plan, state *ToolState, decision ConvergenceDecision) {
	if plan == nil || decision.Converged {
		return
	}
	for _, item := range decision.DistinguishingEvidence {
		appendEvidenceCollectionSteps(plan, item, "多个高置信假设接近，需要补充区分性证据", state)
	}
}

func appendEvidenceCollectionSteps(plan *Plan, evidence, reason string, state *ToolState) {
	if strings.TrimSpace(evidence) == "" {
		return
	}
	added := false
	for _, tool := range toolsForEvidenceGap(evidence) {
		if appendIfMissing(plan, tool, reason, state) {
			added = true
		}
	}
	if !added && len(toolsForEvidenceGap(evidence)) == 0 {
		appendIfMissing(plan, "k8s.get_events", reason, state)
	}
}

func toolsForEvidenceGap(evidence string) []string {
	missing := strings.ToLower(strings.TrimSpace(evidence))
	if missing == "" {
		return nil
	}
	tools := []string{}
	if strings.Contains(missing, "log") ||
		strings.Contains(missing, "profile") ||
		strings.Contains(missing, "heap") ||
		strings.Contains(missing, "config") ||
		strings.Contains(missing, "secret") ||
		strings.Contains(missing, "dependency") ||
		strings.Contains(missing, "registry") ||
		strings.Contains(missing, "init container") {
		tools = append(tools, "k8s.get_logs")
	}
	if strings.Contains(missing, "event") ||
		strings.Contains(missing, "scheduler") ||
		strings.Contains(missing, "probe") ||
		strings.Contains(missing, "image pull") ||
		strings.Contains(missing, "eviction") ||
		strings.Contains(missing, "config") ||
		strings.Contains(missing, "secret") ||
		strings.Contains(missing, "registry") {
		tools = append(tools, "k8s.get_events")
	}
	if strings.Contains(missing, "pvc") {
		tools = append(tools, "k8s.get_pvc")
	}
	if strings.Contains(missing, "metric") ||
		strings.Contains(missing, "memory") ||
		strings.Contains(missing, "working set") {
		tools = append(tools, "prometheus.query_range")
	}
	if strings.Contains(missing, "topology") ||
		strings.Contains(missing, "node") ||
		strings.Contains(missing, "pressure") ||
		strings.Contains(missing, "taint") ||
		strings.Contains(missing, "selector") ||
		strings.Contains(missing, "constraint") {
		tools = append(tools, "k8s.get_topology")
	}
	if len(tools) == 0 {
		tools = append(tools, "k8s.get_events")
	}
	return appendUniqueStrings(tools)
}

func recommendedTools(data interface{}) []string {
	hits, ok := data.([]RunbookHit)
	if !ok {
		return nil
	}
	result := []string{}
	for _, hit := range hits {
		result = append(result, hit.RecommendedTools...)
		result = append(result, parseRecommendedTools(hit.Content)...)
	}
	return result
}

func parseRecommendedTools(content string) []string {
	lines := strings.Split(content, "\n")
	result := []string{}
	inTools := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, "recommended_tools:"):
			inTools = true
			continue
		case strings.HasSuffix(trimmed, ":") && !strings.EqualFold(trimmed, "recommended_tools:"):
			inTools = false
		case inTools && strings.HasPrefix(trimmed, "- "):
			result = append(result, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		}
	}
	return result
}

func nextPlanStep(plan *Plan) *PlanStep {
	if plan == nil {
		return nil
	}
	for i := range plan.Steps {
		if !plan.Steps[i].Completed && !plan.Steps[i].Skipped {
			return &plan.Steps[i]
		}
	}
	return nil
}

func nextPlanSteps(plan *Plan) []*PlanStep {
	first := nextPlanStep(plan)
	if first == nil {
		return nil
	}
	if first.ParallelGroup == "" {
		return []*PlanStep{first}
	}
	result := []*PlanStep{first}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.ID == first.ID || step.Completed || step.Skipped {
			continue
		}
		if step.ParallelGroup == first.ParallelGroup {
			result = append(result, step)
		}
	}
	return result
}

func markStepComplete(plan *Plan, id string) {
	if plan == nil {
		return
	}
	for i := range plan.Steps {
		if plan.Steps[i].ID == id {
			plan.Steps[i].Completed = true
			return
		}
	}
}

func appendIfMissing(plan *Plan, tool, reason string, state *ToolState) bool {
	if !toolAvailableInState(state, tool) || toolAttempted(state, tool) {
		return false
	}
	canonical := canonicalToolName(tool)
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == canonical {
			return false
		}
	}
	input := map[string]interface{}{}
	if state != nil {
		input["namespace"] = state.Goal.Namespace
		input["pod_name"] = state.Goal.PodName
	}
	plan.Steps = append(plan.Steps, PlanStep{
		ID:            fmt.Sprintf("step-%02d", len(plan.Steps)+1),
		ToolName:      tool,
		Reason:        reason,
		Input:         input,
		AppendedBy:    "observation",
		ParallelGroup: "followup-evidence",
	})
	return true
}

func toolAvailableInState(state *ToolState, tool string) bool {
	if state == nil || len(state.AvailableTools) == 0 {
		return true
	}
	canonical := canonicalToolName(tool)
	for _, meta := range state.AvailableTools {
		if canonicalToolName(meta.Name) == canonical {
			return true
		}
	}
	return false
}

func toolAttempted(state *ToolState, tool string) bool {
	if state == nil {
		return false
	}
	canonical := canonicalToolName(tool)
	if toolCompleted(state, canonical) {
		return true
	}
	for _, observation := range state.ObservationSnapshot() {
		if canonicalToolName(observation.ToolName) == canonical {
			return true
		}
	}
	return false
}

func markUnavailable(plan *Plan, tool string) {
	if plan == nil {
		return
	}
	for i := range plan.Steps {
		if plan.Steps[i].ToolName == tool && !plan.Steps[i].Completed {
			plan.Steps[i].Skipped = true
		}
	}
}

func expectedObservations(fault string) []string {
	switch fault {
	case "oomkilled":
		return []string{"终止原因", "内存限制", "内存指标", "previous logs", "节点压力"}
	case "crashloopbackoff":
		return []string{"最近终止状态", "previous logs", "事件", "配置或依赖线索"}
	case "probefailed":
		return []string{"探针配置", "Unhealthy 事件", "健康检查端点日志", "Service Endpoint 影响"}
	case "pending", "podpending":
		return []string{"FailedScheduling 事件", "PVC 状态", "节点约束", "污点和选择器"}
	case "nodenotready":
		return []string{"节点 Ready 条件", "节点事件", "受影响 Pod 拓扑"}
	default:
		return []string{"Pod 状态", "事件", "日志", "拓扑"}
	}
}

func normalizeFault(fault string) string {
	fault = strings.ToLower(strings.TrimSpace(fault))
	fault = strings.ReplaceAll(fault, "_", "")
	fault = strings.ReplaceAll(fault, "-", "")
	fault = strings.ReplaceAll(fault, " ", "")
	return fault
}

func applyDefaultParallelGroups(steps []PlanStep) {
	for i := range steps {
		switch steps[i].ToolName {
		case "k8s.get_events", "k8s.get_logs", "k8s.get_topology", "k8s.get_pvc", "prometheus.query_range", "loki.query_logs":
			steps[i].ParallelGroup = "evidence-snapshot"
		}
	}
}

func canonicalToolName(name string) string {
	switch name {
	case "k8s.get_previous_logs":
		return "k8s.get_logs"
	case "k8s.get_pvc_status":
		return "k8s.get_pvc"
	case "remediation.dry_run_patch":
		return "remediation.dry_run"
	default:
		return name
	}
}

func firstPlanner(planner Planner) Planner {
	if planner != nil {
		return planner
	}
	return NewRulePlanner()
}

func toolNames(steps []*PlanStep) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.ToolName)
	}
	return names
}
