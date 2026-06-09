package agent

import (
	"fmt"
	"reflect"
	"strings"

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

func markToolComplete(state *ToolState, tool string, input map[string]interface{}) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	key := canonicalToolName(tool)
	if state.CompletedTools == nil {
		state.CompletedTools = map[string]bool{}
	}
	state.CompletedTools[key] = true
	if state.CompletedToolInputs == nil {
		state.CompletedToolInputs = map[string][]map[string]interface{}{}
	}
	for _, existing := range state.CompletedToolInputs[key] {
		if inputsMatch(existing, input) {
			return
		}
	}
	state.CompletedToolInputs[key] = append(state.CompletedToolInputs[key], normalizeToolInput(input))
}

func toolCompleted(state *ToolState, tool string) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.CompletedTools[canonicalToolName(tool)]
}

func toolCompletedWithInput(state *ToolState, tool string, input map[string]interface{}) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	key := canonicalToolName(tool)
	for _, existing := range state.CompletedToolInputs[key] {
		if inputsMatch(existing, input) {
			return true
		}
	}
	// Backward compatibility for states created before input history existed:
	// a name-only completion can only prove the default, untargeted call.
	return state.CompletedTools[key] && len(normalizeToolInput(input)) == 0
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
	if tool == "" {
		return true
	}
	return hasCompletedStepWithMatchingInput(plan, state, tool, step.Input) ||
		hasPendingStepWithMatchingInput(plan, tool, step.Input)
}

// hasCompletedStepWithMatchingInput checks successful calls recorded in state.
// PlanStep.Completed also includes failed calls, so plan history alone must not
// suppress a legitimate retry.
func hasCompletedStepWithMatchingInput(_ *Plan, state *ToolState, tool string, input map[string]interface{}) bool {
	return toolCompletedWithInput(state, tool, input)
}

// hasPendingStepWithMatchingInput checks if the same call is already scheduled.
func hasPendingStepWithMatchingInput(plan *Plan, tool string, input map[string]interface{}) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == tool && !step.Completed && !step.Skipped {
			if inputsMatch(step.Input, input) {
				return true
			}
		}
	}
	return false
}

func inputsMatch(a, b map[string]interface{}) bool {
	return reflect.DeepEqual(normalizeToolInput(a), normalizeToolInput(b))
}

func normalizeToolInput(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		key = canonicalToolInputKey(key)
		if key == "" || key == "namespace" || key == "pod_name" {
			continue
		}
		out[key] = normalizeToolInputValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func canonicalToolInputKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	switch strings.ReplaceAll(key, "_", "") {
	case "namespace":
		return "namespace"
	case "pod", "podname":
		return "pod_name"
	case "container", "containername":
		return "container_name"
	default:
		return key
	}
}

func normalizeToolInputValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[canonicalToolInputKey(key)] = normalizeToolInputValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = normalizeToolInputValue(item)
		}
		return out
	default:
		return value
	}
}

func dropCompletedSnapshotSteps(plan *Plan, state *ToolState) {
	if plan == nil || state == nil {
		return
	}
	for i := range plan.Steps {
		tool := canonicalToolName(plan.Steps[i].ToolName)
		if snapshotEvidenceTool(tool) && toolCompletedWithInput(state, tool, plan.Steps[i].Input) && !plan.Steps[i].Completed {
			plan.Steps[i].Skipped = true
		}
	}
}

func snapshotEvidenceTool(tool string) bool {
	switch tool {
	case "k8s.get_pod", "k8s.get_events", "k8s.get_logs", "k8s.get_topology", "k8s.get_pvc", "k8s.get_config_refs":
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
