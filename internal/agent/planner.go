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
	add("k8s.get_pod", "load the authoritative pod snapshot", true, baseInput)
	add("runbook.search", "collect runbook planner hints", false, map[string]interface{}{"fault_type": goal.ExpectedFault, "query": goal.AlertName})

	switch fault {
	case "oomkilled":
		add("k8s.get_previous_logs", "inspect logs around the OOM restart", false, baseInput)
		add("prometheus.query_range", "inspect memory working set near the failure window", false, map[string]interface{}{"query": "container_memory_working_set_bytes"})
		add("loki.query_logs", "check centralized logs for OOM-adjacent messages", false, baseInput)
		add("k8s.get_topology", "check workload and node pressure context", false, baseInput)
	case "crashloopbackoff":
		add("k8s.get_previous_logs", "classify startup failure or runtime crash from previous logs", false, baseInput)
		add("loki.query_logs", "check centralized logs for dependency or config errors", false, baseInput)
		add("k8s.get_events", "inspect BackOff and failure events", false, baseInput)
		add("k8s.get_topology", "check workload impact and peer pod health", false, baseInput)
	case "probefailed":
		add("k8s.get_events", "inspect readiness and liveness probe events", false, baseInput)
		add("k8s.get_previous_logs", "inspect application health endpoint logs", false, baseInput)
		add("loki.query_logs", "check centralized logs around probe failures", false, baseInput)
		add("k8s.get_topology", "check service endpoints and impact", false, baseInput)
	case "pending", "podpending":
		add("k8s.get_events", "inspect scheduler failure events", false, baseInput)
		add("k8s.get_pvc_status", "check PVC binding state directly", false, baseInput)
		add("k8s.get_topology", "inspect scheduling and node context", false, baseInput)
	case "nodenotready":
		add("k8s.get_topology", "inspect node health and pressure conditions", true, baseInput)
		add("k8s.get_events", "collect node-related events", false, baseInput)
		add("k8s.get_previous_logs", "check for node-related log messages", false, baseInput)
	default:
		add("k8s.get_events", "collect general pod events", false, baseInput)
		add("k8s.get_previous_logs", "collect general container logs", false, baseInput)
		add("k8s.get_pvc_status", "check storage constraints when present", false, baseInput)
		add("k8s.get_topology", "collect workload impact context", false, baseInput)
	}

	applyDefaultParallelGroups(steps)
	return Plan{
		Summary:              "rule-based Kubernetes incident response plan",
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
			appendIfMissing(plan, tool, "added from runbook planner hint", state)
		}
	}
	for _, hypothesis := range hypotheses {
		if hypothesis.Status == "confirmed" {
			return
		}
		for _, missing := range hypothesis.MissingEvidence {
			switch {
			case strings.Contains(missing, "logs"):
				appendIfMissing(plan, "k8s.get_previous_logs", "added after hypothesis requested log evidence", state)
			case strings.Contains(missing, "events"):
				appendIfMissing(plan, "k8s.get_events", "added after hypothesis requested event evidence", state)
			case strings.Contains(missing, "pvc"):
				appendIfMissing(plan, "k8s.get_pvc_status", "added after hypothesis requested pvc evidence", state)
			case strings.Contains(missing, "metric"), strings.Contains(missing, "memory"):
				appendIfMissing(plan, "prometheus.query_range", "added after hypothesis requested metric evidence", state)
			}
		}
	}
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

func appendIfMissing(plan *Plan, tool, reason string, state *ToolState) {
	for _, step := range plan.Steps {
		if step.ToolName == tool && !step.Skipped {
			return
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
		return []string{"termination reason", "memory limits", "memory metrics", "previous logs", "node pressure"}
	case "crashloopbackoff":
		return []string{"last termination state", "previous logs", "events", "config/dependency clues"}
	case "probefailed":
		return []string{"probe config", "Unhealthy events", "health endpoint logs", "service endpoint impact"}
	case "pending", "podpending":
		return []string{"FailedScheduling events", "PVC phase", "node constraints", "taints and selectors"}
	default:
		return []string{"pod state", "events", "logs", "topology"}
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
		case "k8s.get_events", "k8s.get_previous_logs", "k8s.get_topology", "k8s.get_pvc_status", "prometheus.query_range", "loki.query_logs":
			steps[i].ParallelGroup = "evidence-snapshot"
		}
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
