package agent

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const probeEvidenceFallbackGroup = "probe-evidence-fallback"

func enforceProbeEvidenceFallback(plan *Plan, state *ToolState) bool {
	if plan == nil || state == nil || !needsProbeEvidenceFallback(state) {
		return false
	}

	missing := []string{}
	for _, tool := range []string{"k8s.get_events", "k8s.get_logs"} {
		if !toolAlreadyCollected(plan, state, tool) {
			missing = append(missing, tool)
		}
	}
	if len(missing) == 0 {
		return false
	}

	if shouldRefreshForProbeEvidence(state) {
		state.Goal.IncludeEvents = true
		state.Goal.IncludeLogs = true
		state.DiagnosticContext = nil
	}
	for _, tool := range missing {
		ensureProbeEvidenceStep(plan, tool, state)
	}
	prioritizeProbeEvidenceSteps(plan, missing)
	return true
}

func needsProbeEvidenceFallback(state *ToolState) bool {
	if state == nil || !unknownFault(state.Goal.ExpectedFault) || state.DiagnosticContext == nil {
		return false
	}
	pod := state.DiagnosticContext.Pod
	return podReadyFalse(pod) && podHasProbe(pod)
}

func unknownFault(fault string) bool {
	switch normalizeFault(fault) {
	case "", "unknown", "unspecified", "auto":
		return true
	default:
		return false
	}
}

func podReadyFalse(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionFalse
		}
	}
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return true
		}
	}
	return false
}

func podHasProbe(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, container := range pod.Spec.Containers {
		if container.ReadinessProbe != nil || container.LivenessProbe != nil || container.StartupProbe != nil {
			return true
		}
	}
	return false
}

func shouldRefreshForProbeEvidence(state *ToolState) bool {
	if state == nil || state.DiagnosticContext == nil {
		return false
	}
	return !state.Goal.IncludeEvents || !state.Goal.IncludeLogs || len(state.DiagnosticContext.Events) == 0 || len(state.DiagnosticContext.Logs) == 0
}

func planHasCompletedTool(plan *Plan, tool string) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == tool && step.Completed && !step.Skipped {
			return true
		}
	}
	return false
}

func markToolComplete(state *ToolState, tool string) {
	if state == nil {
		return
	}
	if state.CompletedTools == nil {
		state.CompletedTools = map[string]bool{}
	}
	state.CompletedTools[canonicalToolName(tool)] = true
}

func toolCompleted(state *ToolState, tool string) bool {
	if state == nil || state.CompletedTools == nil {
		return false
	}
	return state.CompletedTools[canonicalToolName(tool)]
}

func toolAlreadyCollected(plan *Plan, state *ToolState, tool string) bool {
	return toolCompleted(state, tool) || planHasCompletedTool(plan, tool)
}

func planHasPendingTool(plan *Plan, tool string) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == tool && !step.Completed && !step.Skipped {
			return true
		}
	}
	return false
}

func redundantReflectionStep(plan *Plan, state *ToolState, step PlanStep) bool {
	tool := canonicalToolName(step.ToolName)
	if !snapshotEvidenceTool(tool) {
		return false
	}
	return toolAlreadyCollected(plan, state, tool) || planHasPendingTool(plan, tool)
}

func dropCompletedSnapshotSteps(plan *Plan, state *ToolState) {
	if plan == nil || state == nil {
		return
	}
	for i := range plan.Steps {
		tool := canonicalToolName(plan.Steps[i].ToolName)
		if snapshotEvidenceTool(tool) && toolCompleted(state, tool) && !plan.Steps[i].Completed {
			plan.Steps[i].Skipped = true
		}
	}
}

func snapshotEvidenceTool(tool string) bool {
	switch tool {
	case "k8s.get_pod", "k8s.get_events", "k8s.get_logs", "k8s.get_topology", "k8s.get_pvc":
		return true
	default:
		return false
	}
}

func ensureProbeEvidenceStep(plan *Plan, tool string, state *ToolState) {
	for i := range plan.Steps {
		if canonicalToolName(plan.Steps[i].ToolName) == tool && !plan.Steps[i].Completed && !plan.Steps[i].Skipped {
			plan.Steps[i].ParallelGroup = ""
			plan.Steps[i].AppendedBy = firstNonEmptyString(plan.Steps[i].AppendedBy, probeEvidenceFallbackGroup)
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
		Reason:        "Probe fallback: collect Events and logs before analyzer",
		Input:         input,
		AppendedBy:    probeEvidenceFallbackGroup,
		ParallelGroup: "",
	})
}

func prioritizeProbeEvidenceSteps(plan *Plan, tools []string) {
	if plan == nil || len(tools) == 0 {
		return
	}
	want := map[string]bool{}
	for _, tool := range tools {
		want[tool] = true
	}
	prefix := []PlanStep{}
	fallback := []PlanStep{}
	rest := []PlanStep{}
	for _, step := range plan.Steps {
		if want[canonicalToolName(step.ToolName)] && !step.Completed && !step.Skipped {
			step.ParallelGroup = ""
			fallback = append(fallback, step)
			continue
		}
		if step.Completed || step.Skipped {
			prefix = append(prefix, step)
			continue
		}
		rest = append(rest, step)
	}
	plan.Steps = append(append(prefix, fallback...), rest...)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
