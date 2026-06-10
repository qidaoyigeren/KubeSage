package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/observability"
)

// evidenceSummarizer is an optional interface that LLM clients can implement
// to provide semantic summaries of evidence records via a fast/cheap model.
type evidenceSummarizer interface {
	SummarizeEvidences(ctx context.Context, records []diagnostic.EvidenceRecord) (string, error)
}

// logLLMPlannerError logs LLM planner errors so fallback reasons are visible.
func logLLMPlannerError(operation string, err error) {
	log.Printf("[LLMPlanner] %s failed, falling back to rule planner: %v", operation, err)
}

const (
	stopReasonLLMReflectionComplete = "llm_reflection_complete"
)

type LLMPlanner struct {
	fallback *RulePlanner
	client   PlanClient
}

func NewLLMPlanner(client ...PlanClient) *LLMPlanner {
	var plannerClient PlanClient
	if len(client) > 0 {
		plannerClient = client[0]
	}
	return &LLMPlanner{fallback: NewRulePlanner(), client: plannerClient}
}

func (p *LLMPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	readOnly := readonlyTools(tools)
	if p.client != nil {
		plan, err := p.client.GenerateAgentPlan(ctx, PlanPrompt{
			Goal:  goal,
			Tools: readOnly,
			Safety: []string{
				"Use only tools present in the provided tool list.",
				"Use read-only tools only.",
				"Do not propose remediation execution.",
				"Prefer parallel evidence collection when tools are independent.",
			},
		})
		if err == nil {
			return sanitizePlan(plan, readOnly, goal)
		}
		observability.IncLLMPlannerFallback("build_plan", "llm_error")
		// Log the error so it is visible in diagnostics instead of silently falling back.
		logLLMPlannerError("BuildInitialPlan", err)
	}
	plan := p.fallback.BuildInitialPlan(ctx, goal, readOnly)
	plan.Summary = "LLM Planner 降级为规则兜底，生成已通过策略校验的只读诊断计划"
	return plan
}

// AdjustPlan uses the LLM to revise the plan based on observations and
// hypothesis scores. Falls back to the rule-based planner on error.
func (p *LLMPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	if p.client == nil {
		p.fallback.AdjustPlan(plan, state, last, hypotheses)
		return
	}

	// Collect all observations and evidence gathered before the analyzer runs.
	observations := collectObservations(state, last, p.summarizer())
	evidence := collectEvidenceSummary(state, p.summarizer())
	readOnly := readonlyToolsFromState(state)

	adjustContext := context.Background()
	if state != nil && state.RunContext != nil {
		adjustContext = state.RunContext
	}
	adjusted, err := p.client.GeneratePlanAdjustment(adjustContext, AdjustmentPrompt{
		CurrentPlan:  *plan,
		Observations: observations,
		Hypotheses:   hypotheses,
		Tools:        readOnly,
		Safety: []string{
			"Use only read-only tools.",
			"Do not propose remediation execution.",
			"Remove steps that are no longer needed.",
			"Add steps to fill evidence gaps identified by hypotheses.",
		},
		Goal:     state.Goal,
		Evidence: evidence,
	})
	if err != nil || len(adjusted.Steps) == 0 {
		// Fallback to rule-based adjustment.
		observability.IncLLMPlannerFallback("adjust_plan", "llm_error_or_empty")
		if err != nil {
			logLLMPlannerError("AdjustPlan", err)
		}
		p.fallback.AdjustPlan(plan, state, last, hypotheses)
		return
	}

	sanitized := sanitizePlan(adjusted, readOnly, state.Goal)
	// Preserve completed/skipped state from the original plan.
	completed := map[string]bool{}
	skipped := map[string]bool{}
	for _, s := range plan.Steps {
		completed[s.ID] = s.Completed
		skipped[s.ID] = s.Skipped
	}
	for i := range sanitized.Steps {
		if completed[sanitized.Steps[i].ID] {
			sanitized.Steps[i].Completed = true
		}
		if skipped[sanitized.Steps[i].ID] {
			sanitized.Steps[i].Skipped = true
		}
	}
	*plan = sanitized
}

// Reflect asks the LLM whether the current evidence is sufficient to confirm
// a hypothesis, or whether more investigation is needed.
func (p *LLMPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	if p.client == nil {
		return ReflectionResult{ShouldContinue: true, Reason: "no LLM client configured, continuing"}, nil
	}

	evidence := collectEvidenceSummary(state, p.summarizer())
	observations := collectObservations(state, ToolResult{}, p.summarizer())
	readOnly := readonlyToolsFromState(state)

	prompt := ReflectionPrompt{
		Plan:       plan,
		Goal:       state.Goal,
		Hypotheses: hypotheses,
		Tools:      readOnly,
		Safety: []string{
			"Use only tools present in the provided read-only tool list.",
			"Do not propose remediation execution.",
			"Every new step must include the smallest input that makes it materially different from completed calls.",
			"Use container_name when another app or init container needs separate logs or configuration inspection.",
			"For missing configuration files, inspect Pod ConfigMap/Secret/volume references and Events; PVC evidence does not prove ConfigMap or Secret state.",
			"Use runbook.search with a specific query when a known failure pattern can narrow the next evidence check.",
			"Do not claim a ConfigMap or Secret is missing unless Events or direct evidence support that conclusion.",
		},
		Evidence:     evidence,
		Observations: observations,
	}

	result, err := p.client.GenerateReflection(ctx, prompt)
	if err != nil {
		logLLMPlannerError("Reflect", err)
		return ReflectionResult{ShouldContinue: true, Reason: "LLM reflection failed: " + err.Error()}, nil
	}
	return result, nil
}

// HasLLMClient returns true if this planner has an LLM backend configured.
func (p *LLMPlanner) HasLLMClient() bool {
	return p.client != nil
}

func (p *LLMPlanner) CumulativeTokenUsage() int {
	if p == nil || p.client == nil {
		return 0
	}
	reporter, ok := p.client.(TokenUsageReporter)
	if !ok {
		return 0
	}
	return reporter.CumulativeTokenUsage()
}

// summarizer returns the LLM client as an evidenceSummarizer if it implements
// the interface, nil otherwise. This allows graceful degradation when the LLM
// client does not support evidence summarization.
func (p *LLMPlanner) summarizer() evidenceSummarizer {
	if p == nil || p.client == nil {
		return nil
	}
	s, ok := p.client.(evidenceSummarizer)
	if !ok {
		return nil
	}
	return s
}

func readonlyTools(tools []ToolMetadata) []ToolMetadata {
	result := make([]ToolMetadata, 0, len(tools))
	for _, tool := range tools {
		if tool.ReadOnly {
			result = append(result, tool)
		}
	}
	return result
}

func readonlyToolsFromState(state *ToolState) []ToolMetadata {
	return readonlyTools(state.ToolMetadataSnapshot())
}

func sanitizePlan(plan Plan, tools []ToolMetadata, goal Goal) Plan {
	available := map[string]ToolMetadata{}
	for _, tool := range tools {
		available[tool.Name] = tool
		available[canonicalToolName(tool.Name)] = tool
	}
	sanitized := Plan{Summary: plan.Summary, ExpectedObservations: plan.ExpectedObservations, StopCondition: plan.StopCondition}
	if sanitized.Summary == "" {
		sanitized.Summary = "LLM Planner 生成已通过策略校验的只读诊断计划"
	}
	if len(sanitized.StopCondition) == 0 {
		sanitized.StopCondition = []string{StopReasonMaxSteps, StopReasonConfirmedHypothesis, StopReasonTimeout, StopReasonNoEffectiveTool, StopReasonCriticalToolFailed}
	}
	for _, step := range plan.Steps {
		tool, ok := available[step.ToolName]
		if !ok || !tool.ReadOnly {
			continue
		}
		if step.ID == "" {
			step.ID = "step-" + step.ToolName
		}
		if step.Input == nil {
			step.Input = map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName}
		}
		step.Critical = tool.Critical
		sanitized.Steps = append(sanitized.Steps, step)
	}
	if len(sanitized.Steps) == 0 {
		return NewRulePlanner().BuildInitialPlan(context.Background(), goal, tools)
	}
	ensureRequiredEvidenceSteps(&sanitized, available, goal)
	applyDefaultParallelGroups(sanitized.Steps)
	return sanitized
}

func sanitizeReflectionStep(step PlanStep, tools []ToolMetadata, goal Goal) (PlanStep, bool) {
	requested := canonicalToolName(strings.TrimSpace(step.ToolName))
	var metadata ToolMetadata
	found := false
	for _, tool := range tools {
		if canonicalToolName(tool.Name) == requested {
			metadata = tool
			found = true
			break
		}
	}
	if !found || !metadata.ReadOnly {
		return PlanStep{}, false
	}

	input := make(map[string]interface{})
	for key, value := range step.Input {
		key = canonicalToolInputKey(key)
		if _, ok := metadata.InputSchema[key]; ok {
			input[key] = value
		}
	}
	if _, ok := metadata.InputSchema["namespace"]; ok {
		input["namespace"] = goal.Namespace
	}
	if _, ok := metadata.InputSchema["pod_name"]; ok {
		input["pod_name"] = goal.PodName
	}
	if _, ok := metadata.InputSchema["container_name"]; ok && stringInput(input, "container_name") == "" && goal.ContainerName != "" {
		input["container_name"] = goal.ContainerName
	}

	step.ToolName = metadata.Name
	step.Input = input
	step.Critical = metadata.Critical
	if strings.TrimSpace(step.Reason) == "" {
		step.Reason = "LLM reflection identified an evidence gap"
	}
	return step, true
}

func ensureRequiredEvidenceSteps(plan *Plan, available map[string]ToolMetadata, goal Goal) {
	if plan == nil || normalizeFault(goal.ExpectedFault) != "crashloopbackoff" {
		return
	}
	baseInput := map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName}
	required := []struct {
		tool   string
		reason string
	}{
		{tool: "k8s.get_pod", reason: "CrashLoop 分析前先读取 Pod 权威快照"},
		{tool: "k8s.get_events", reason: "检查 CrashLoopBackOff 相关 BackOff 和失败事件"},
		{tool: "k8s.get_logs", reason: "检查 current/previous logs 中的启动失败证据"},
		{tool: "k8s.get_topology", reason: "检查 CrashLoopBackOff 对工作负载和节点上下文的影响"},
	}
	for _, item := range required {
		if !toolAvailable(available, item.tool) || planHasTool(plan, item.tool) {
			continue
		}
		step := PlanStep{
			ID:         "guard-" + strings.ReplaceAll(item.tool, ".", "-"),
			ToolName:   item.tool,
			Reason:     item.reason,
			Input:      baseInput,
			AppendedBy: "planner_guardrail",
		}
		if item.tool == "k8s.get_pod" {
			plan.Steps = append([]PlanStep{step}, plan.Steps...)
			continue
		}
		step.ParallelGroup = "evidence-snapshot"
		plan.Steps = append(plan.Steps, step)
	}
}

func toolAvailable(available map[string]ToolMetadata, tool string) bool {
	if len(available) == 0 {
		return false
	}
	_, ok := available[tool]
	return ok
}

func planHasTool(plan *Plan, tool string) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == tool && !step.Skipped {
			return true
		}
	}
	return false
}

func formatObservation(record ObservationRecord) string {
	if strings.TrimSpace(record.Observation) == "" && record.Error == "" {
		return ""
	}
	status := "success"
	if !record.Success {
		status = "failed"
	}
	parts := []string{fmt.Sprintf("%s [%s]: %s", record.ToolName, status, record.Observation)}
	if len(record.MissingEvidence) > 0 {
		parts = append(parts, "missing="+strings.Join(record.MissingEvidence, ","))
	}
	if len(record.Warnings) > 0 {
		parts = append(parts, "warnings="+strings.Join(record.Warnings, ","))
	}
	if record.Error != "" {
		parts = append(parts, "error="+record.Error)
	}
	if len(record.EvidenceRefs) > 0 {
		parts = append(parts, "evidence_refs="+strings.Join(record.EvidenceRefs, ","))
	}
	return strings.Join(parts, " ")
}

// evidenceScorer is a package-level scorer used by collectEvidenceSummary and
// collectObservations. It is replaced in tests when needed.

// collectEvidenceSummary builds a text summary of evidence gathered before the
// analyzer produces the final report. Uses relevance scoring to surface the most
// important evidence first, with low-scoring evidence collapsed into a summary line.
// If a summarizer is provided and there are remaining records, it uses LLM to
// produce a semantic summary instead of just grouped counts.
func collectEvidenceSummary(state *ToolState, summarizers ...evidenceSummarizer) string {
	if state == nil {
		return ""
	}
	evidence := state.EvidenceSnapshot()

	// Deduplicate first.
	deduped := GetEvidenceScorer().DeduplicateEvidence(evidence)

	// Score with empty hypotheses — scoring context is added in reflection path.
	scored := GetEvidenceScorer().ScoreEvidence(deduped, nil, len(deduped))

	// Use token-budget-aware summary: top 25 full records, rest collapsed.
	mainSummary, remaining := BuildEvidenceSummary(scored, 25, 200)
	if len(remaining) == 0 || len(summarizers) == 0 || summarizers[0] == nil {
		return mainSummary
	}

	// Use LLM to summarize remaining evidence.
	ctx := context.Background()
	if state.RunContext != nil {
		ctx = state.RunContext
	}
	if llmSummary, err := summarizers[0].SummarizeEvidences(ctx, remaining); err == nil && llmSummary != "" {
		return mainSummary + "\n\n[collapsed LLM summary] " + llmSummary
	}
	return mainSummary
}

// collectObservations gathers observation strings from completed tool calls,
// scoring them by relevance and compressing low-relevance records. When a
// summarizer is provided, low-relevance observations are sent to the LLM for
// semantic compression instead of being dropped.
func collectObservations(state *ToolState, last ToolResult, summarizers ...evidenceSummarizer) []string {
	observations := []ObservationRecord{}
	seen := map[string]struct{}{}
	for _, record := range state.ObservationSnapshot() {
		text := formatObservation(record)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		observations = append(observations, record)
	}
	if last.Observation != "" {
		record := ObservationRecord{
			ToolName:        last.ToolName,
			Success:         last.Success,
			Observation:     last.Observation,
			Warnings:        last.Warnings,
			MissingEvidence: last.MissingEvidence,
			Error:           last.Error,
		}
		text := formatObservation(record)
		if _, ok := seen[text]; !ok && text != "" {
			observations = append(observations, record)
		}
	}

	// Score and compress: top-N kept in full, rest converted for LLM summarization.
	lines, remaining := CompressObservations(observations)

	// If a summarizer is available and there are low-relevance records, use LLM
	// to produce a semantic summary instead of statistical grouping.
	if len(remaining) > 0 && len(summarizers) > 0 && summarizers[0] != nil {
		ctx := context.Background()
		if state.RunContext != nil {
			ctx = state.RunContext
		}
		if llmSummary, err := summarizers[0].SummarizeEvidences(ctx, remaining); err == nil && llmSummary != "" {
			lines = append(lines, "[collapsed LLM observation summary] "+llmSummary)
		} else {
			// Fallback: statistical summary of low-relevance observation tools.
			lines = append(lines, formatObservationRemainingSummary(remaining))
		}
	} else if len(remaining) > 0 {
		lines = append(lines, formatObservationRemainingSummary(remaining))
	}

	return lines
}

// formatObservationRemainingSummary produces a grouped statistical fallback for
// low-relevance observations that were not sent to LLM summarization.
func formatObservationRemainingSummary(remaining []diagnostic.EvidenceRecord) string {
	counts := map[string]int{}
	for _, r := range remaining {
		counts[r.Title]++
	}
	parts := make([]string, 0, len(counts))
	for tool, count := range counts {
		parts = append(parts, fmt.Sprintf("%s×%d", tool, count))
	}
	return fmt.Sprintf("[collapsed] %d lower-relevance observations (%s)", len(remaining), strings.Join(parts, ", "))
}

func countCompleted(plan *Plan) int {
	n := 0
	for _, s := range plan.Steps {
		if s.Completed {
			n++
		}
	}
	return n
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Ensure LLMPlanner implements ReflectivePlanner at compile time.
var _ ReflectivePlanner = (*LLMPlanner)(nil)
var _ Planner = (*LLMPlanner)(nil)
var _ TokenUsageReporter = (*LLMPlanner)(nil)
