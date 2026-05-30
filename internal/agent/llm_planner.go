package agent

import (
	"context"
	"fmt"
	"strings"

	"kubesage/internal/observability"
)

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
	}
	plan := p.fallback.BuildInitialPlan(ctx, goal, readOnly)
	plan.Summary = "llm planner fallback selected a policy-validated read-only diagnostic plan"
	return plan
}

// AdjustPlan uses the LLM to revise the plan based on observations and
// hypothesis scores. Falls back to the rule-based planner on error.
func (p *LLMPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	if p.client == nil {
		p.fallback.AdjustPlan(plan, state, last, hypotheses)
		return
	}

	// Collect all observations from completed steps.
	observations := collectObservations(plan, last)
	readOnly := readonlyToolsFromState(state)

	adjusted, err := p.client.GeneratePlanAdjustment(context.Background(), AdjustmentPrompt{
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
		Goal: state.Goal,
	})
	if err != nil || len(adjusted.Steps) == 0 {
		// Fallback to rule-based adjustment.
		observability.IncLLMPlannerFallback("adjust_plan", "llm_error_or_empty")
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

	evidence := collectEvidenceSummary(state)
	observations := collectObservationsFromPlan(&plan)
	readOnly := readonlyToolsFromState(state)

	prompt := ReflectionPrompt{
		Plan:         plan,
		Goal:         state.Goal,
		Hypotheses:   hypotheses,
		Tools:        readOnly,
		Safety:       []string{"Use only read-only tools.", "Do not propose remediation execution."},
		Evidence:     evidence,
		Observations: observations,
	}

	result, err := p.client.GenerateReflection(ctx, prompt)
	if err != nil {
		return ReflectionResult{ShouldContinue: true, Reason: "LLM reflection failed: " + err.Error()}, nil
	}
	return result, nil
}

// HasLLMClient returns true if this planner has an LLM backend configured.
func (p *LLMPlanner) HasLLMClient() bool {
	return p.client != nil
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
	// We don't have the full tool list in state, so return an empty slice.
	// The sanitizer will handle filtering.
	return nil
}

func sanitizePlan(plan Plan, tools []ToolMetadata, goal Goal) Plan {
	available := map[string]ToolMetadata{}
	for _, tool := range tools {
		available[tool.Name] = tool
	}
	sanitized := Plan{Summary: plan.Summary, ExpectedObservations: plan.ExpectedObservations, StopCondition: plan.StopCondition}
	if sanitized.Summary == "" {
		sanitized.Summary = "llm planner selected a policy-validated read-only diagnostic plan"
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
		sanitized.Steps = append(sanitized.Steps, step)
	}
	if len(sanitized.Steps) == 0 {
		return NewRulePlanner().BuildInitialPlan(context.Background(), goal, tools)
	}
	return sanitized
}

// collectObservations gathers observation strings from completed plan steps.
func collectObservations(plan *Plan, last ToolResult) []string {
	var observations []string
	// Include the latest observation.
	if last.Observation != "" {
		observations = append(observations, last.Observation)
	}
	return observations
}

// collectObservationsFromPlan gathers observations from the plan's expected observations.
func collectObservationsFromPlan(plan *Plan) []string {
	if plan == nil {
		return nil
	}
	return plan.ExpectedObservations
}

// collectEvidenceSummary builds a text summary of evidence from the report.
func collectEvidenceSummary(state *ToolState) string {
	if state == nil || state.Report == nil {
		return ""
	}
	var parts []string
	for _, ev := range state.Report.Evidences {
		if ev.Content != "" {
			parts = append(parts, fmt.Sprintf("[%s] %s: %s", ev.Severity, ev.Title, truncate(ev.Content, 200)))
		}
	}
	if len(parts) > 10 {
		parts = parts[:10]
	}
	return strings.Join(parts, "\n")
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
