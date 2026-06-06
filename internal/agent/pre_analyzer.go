package agent

import (
	"fmt"
	"strings"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

const preAnalyzerEvidenceGroup = "pre-analyzer-evidence"

// PreAnalysisDecision is the structured fault classification that pins the
// remaining runtime plan to fault-specific evidence collection.
type PreAnalysisDecision struct {
	FaultType        string   `json:"fault_type"`
	AnalyzerName     string   `json:"analyzer_name,omitempty"`
	ConfidenceScore  float64  `json:"confidence_score"`
	EvidenceTools    []string `json:"evidence_tools"`
	SkippedTools     []string `json:"skipped_tools,omitempty"`
	PlanRewriteCause string   `json:"plan_rewrite_cause"`
}

type targetedEvidenceRequest struct {
	tool   string
	reason string
	input  map[string]interface{}
}

type targetedPlanRewrite struct {
	changed   bool
	scheduled []string
	skipped   []string
}

func buildPreAnalysisDecision(report *diagnostic.Report, state *ToolState) *PreAnalysisDecision {
	if report == nil || state == nil || state.DiagnosticContext == nil {
		return nil
	}
	faultType, analyzerName := preAnalysisPrimary(report)
	if !preAnalysisFaultSupported(faultType, state.DiagnosticContext) {
		return nil
	}
	requests := targetedEvidenceRequests(faultType, state)
	tools := make([]string, 0, len(requests))
	for _, request := range requests {
		if toolAvailableInState(state, request.tool) {
			tools = append(tools, request.tool)
		}
	}
	return &PreAnalysisDecision{
		FaultType:        canonicalFaultType(faultType),
		AnalyzerName:     analyzerName,
		ConfidenceScore:  report.ConfidenceScore,
		EvidenceTools:    appendUniqueStrings(tools),
		PlanRewriteCause: "deterministic analyzer matched structured Kubernetes state",
	}
}

func preAnalysisPrimary(report *diagnostic.Report) (string, string) {
	if report == nil {
		return "", ""
	}
	if report.PrimaryRootCause != nil {
		fault := strings.TrimSpace(report.PrimaryRootCause.FaultType)
		if fault != "" && !strings.EqualFold(fault, "unknown") {
			return fault, strings.TrimSpace(report.PrimaryRootCause.AnalyzerName)
		}
	}
	fault := strings.TrimSpace(report.FaultType)
	if index := strings.Index(fault, ","); index >= 0 {
		fault = strings.TrimSpace(fault[:index])
	}
	return fault, ""
}

// preAnalysisFaultSupported prevents a report or LLM-like label from
// redirecting the plan unless the current diagnostic context contains the
// corresponding structured Kubernetes signal.
func preAnalysisFaultSupported(faultType string, ctx *diagnostic.DiagnosticContext) bool {
	if ctx == nil || ctx.Pod == nil {
		return false
	}
	pod := ctx.Pod
	switch normalizeReportFault(faultType) {
	case "oomkilled":
		return podHasOOMKilledTermination(pod) || eventsContain(ctx.Events, "oomkilled")
	case "crashloopbackoff":
		return podHasCrashLoopSignal(pod)
	case "imagepullbackoff", "errimagepull":
		return podHasImagePullSignal(pod)
	case "initerror", "initcontainererror":
		return podHasInitFailureSignal(pod)
	case "evicted":
		return strings.EqualFold(pod.Status.Reason, "Evicted") ||
			pod.Status.Phase == corev1.PodFailed && strings.Contains(strings.ToLower(pod.Status.Message), "evict")
	case "podpending", "pending":
		return pod.Status.Phase == corev1.PodPending || podScheduledFalse(pod)
	case "probefailed":
		return eventsContainFailedProbe(ctx.Events)
	case "nodenotready":
		return contextHasNotReadyNode(ctx)
	default:
		return false
	}
}

func targetedEvidenceRequests(faultType string, state *ToolState) []targetedEvidenceRequest {
	if state == nil {
		return nil
	}
	baseInput := map[string]interface{}{
		"namespace": state.Goal.Namespace,
		"pod_name":  state.Goal.PodName,
	}
	if state.Goal.ContainerName != "" {
		baseInput["container_name"] = state.Goal.ContainerName
	}
	request := func(tool, reason string, input map[string]interface{}) targetedEvidenceRequest {
		if input == nil {
			input = cloneToolInput(baseInput)
		}
		return targetedEvidenceRequest{tool: tool, reason: reason, input: input}
	}

	switch normalizeReportFault(faultType) {
	case "oomkilled":
		return []targetedEvidenceRequest{
			request("k8s.get_logs", "OOMKilled is structurally confirmed; collect logs around the last termination", nil),
			request("prometheus.query_range", "OOMKilled is structurally confirmed; collect memory working-set trends around the failure", map[string]interface{}{"query": "container_memory_working_set_bytes"}),
			request("k8s.get_topology", "OOMKilled is structurally confirmed; collect workload and node pressure context", nil),
		}
	case "crashloopbackoff":
		return []targetedEvidenceRequest{
			request("k8s.get_logs", "CrashLoopBackOff is structurally confirmed; collect previous and current startup logs", nil),
			request("k8s.get_events", "CrashLoopBackOff is structurally confirmed; collect restart and BackOff events", nil),
			request("k8s.get_topology", "CrashLoopBackOff is structurally confirmed; collect workload blast-radius context", nil),
		}
	case "imagepullbackoff", "errimagepull":
		return []targetedEvidenceRequest{
			request("k8s.get_events", "ImagePullBackOff is structurally confirmed; collect registry, tag, and authentication details", nil),
		}
	case "initerror", "initcontainererror":
		return []targetedEvidenceRequest{
			request("k8s.get_logs", "Init container failure is structurally confirmed; collect init-container logs", nil),
			request("k8s.get_events", "Init container failure is structurally confirmed; collect init and BackOff events", nil),
		}
	case "evicted":
		var pod *corev1.Pod
		if state.DiagnosticContext != nil {
			pod = state.DiagnosticContext.Pod
		}
		return []targetedEvidenceRequest{
			request("k8s.get_events", "Eviction is structurally confirmed; collect the kubelet eviction reason", nil),
			request("k8s.get_topology", "Eviction is structurally confirmed; collect node pressure and affected workload context", nil),
			request("prometheus.query_range", "Eviction is structurally confirmed; collect the relevant node resource trend", map[string]interface{}{"query": evictionMetricQuery(pod)}),
		}
	case "podpending", "pending":
		requests := []targetedEvidenceRequest{
			request("k8s.get_events", "PodPending is structurally confirmed; collect FailedScheduling details", nil),
		}
		if podUsesPVC(state.DiagnosticContext.Pod) {
			requests = append(requests, request("k8s.get_pvc", "PodPending references PVCs; inspect binding and storage topology", nil))
		}
		return append(requests, request("k8s.get_topology", "PodPending is structurally confirmed; collect node capacity and scheduling constraints", nil))
	case "probefailed":
		return []targetedEvidenceRequest{
			request("k8s.get_events", "Probe failure is structurally confirmed; retain exact Unhealthy event details", nil),
			request("k8s.get_logs", "Probe failure is structurally confirmed; collect health endpoint logs", nil),
			request("k8s.get_topology", "Probe failure is structurally confirmed; collect Service endpoint impact", nil),
		}
	case "nodenotready":
		return []targetedEvidenceRequest{
			request("k8s.get_topology", "NodeNotReady is structurally confirmed; collect node conditions and affected workloads", nil),
			request("k8s.get_events", "NodeNotReady is structurally confirmed; collect node transition events", nil),
		}
	default:
		return nil
	}
}

func applyTargetedEvidencePlan(plan *Plan, state *ToolState, decision *PreAnalysisDecision) targetedPlanRewrite {
	if plan == nil || state == nil || decision == nil {
		return targetedPlanRewrite{}
	}
	requests := targetedEvidenceRequests(decision.FaultType, state)
	wanted := make(map[string]targetedEvidenceRequest, len(requests))
	for _, request := range requests {
		if toolAvailableInState(state, request.tool) && !toolAttempted(state, request.tool) {
			wanted[canonicalToolName(request.tool)] = request
		}
	}

	history := make([]PlanStep, 0, len(plan.Steps))
	selected := make([]PlanStep, 0, len(wanted))
	selectedByTool := map[string]bool{}
	var runbook *PlanStep
	rewrite := targetedPlanRewrite{}
	usedStepIDs := map[string]bool{}
	for _, step := range plan.Steps {
		usedStepIDs[step.ID] = true
	}
	nextStepID := func() string {
		for index := len(plan.Steps) + 1; ; index++ {
			candidate := fmt.Sprintf("step-%02d", index)
			if !usedStepIDs[candidate] {
				usedStepIDs[candidate] = true
				return candidate
			}
		}
	}

	for i := range plan.Steps {
		step := plan.Steps[i]
		tool := canonicalToolName(step.ToolName)
		request, targeted := wanted[tool]
		switch {
		case step.Completed:
			history = append(history, step)
		case targeted && !selectedByTool[tool]:
			if step.Skipped || step.ParallelGroup != preAnalyzerEvidenceGroup || step.AppendedBy != "pre_analyzer" {
				rewrite.changed = true
			}
			step.Skipped = false
			step.Reason = request.reason
			step.Input = mergeToolInput(step.Input, request.input)
			step.AppendedBy = "pre_analyzer"
			step.ParallelGroup = preAnalyzerEvidenceGroup
			selected = append(selected, step)
			selectedByTool[tool] = true
		case tool == "runbook.search" && runbook == nil && !toolAttempted(state, tool):
			step.Skipped = false
			step.Input = mergeToolInput(step.Input, map[string]interface{}{"fault_type": decision.FaultType})
			step.Reason = "Search runbook guidance for the pre-analyzer fault classification"
			runbook = &step
		default:
			if !step.Skipped {
				rewrite.changed = true
				rewrite.skipped = append(rewrite.skipped, step.ToolName)
			}
			step.Skipped = true
			history = append(history, step)
		}
	}

	for _, request := range requests {
		tool := canonicalToolName(request.tool)
		if selectedByTool[tool] || !toolAvailableInState(state, request.tool) || toolAttempted(state, request.tool) {
			continue
		}
		selected = append(selected, PlanStep{
			ID:            nextStepID(),
			ToolName:      request.tool,
			Input:         cloneToolInput(request.input),
			Reason:        request.reason,
			AppendedBy:    "pre_analyzer",
			ParallelGroup: preAnalyzerEvidenceGroup,
		})
		selectedByTool[tool] = true
		rewrite.changed = true
	}

	for _, step := range selected {
		rewrite.scheduled = append(rewrite.scheduled, step.ToolName)
	}
	rewritten := append(history, selected...)
	if runbook != nil {
		rewritten = append(rewritten, *runbook)
	}
	plan.Steps = rewritten
	plan.Summary = fmt.Sprintf("Pre-analyzer targeted evidence plan for %s", decision.FaultType)
	plan.ExpectedObservations = expectedObservations(normalizeFault(decision.FaultType))
	decision.SkippedTools = appendUniqueStrings(append(decision.SkippedTools, rewrite.skipped...))
	return rewrite
}

func hasPendingTargetedEvidence(plan *Plan) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if step.AppendedBy == "pre_analyzer" && !step.Completed && !step.Skipped {
			return true
		}
	}
	return false
}

func enforceRuntimeEvidencePlan(plan *Plan, state *ToolState, decision *PreAnalysisDecision) bool {
	if decision != nil {
		return applyTargetedEvidencePlan(plan, state, decision).changed
	}
	if enforceProbeEvidenceFallback(plan, state) {
		return true
	}
	return enforceStateDrivenEvidenceFallback(plan, state)
}

func canonicalFaultType(faultType string) string {
	switch normalizeReportFault(faultType) {
	case "oomkilled":
		return "OOMKilled"
	case "crashloopbackoff":
		return "CrashLoopBackOff"
	case "imagepullbackoff", "errimagepull":
		return "ImagePullBackOff"
	case "initerror", "initcontainererror":
		return "InitError"
	case "evicted":
		return "Evicted"
	case "podpending", "pending":
		return "PodPending"
	case "probefailed":
		return "ProbeFailed"
	case "nodenotready":
		return "NodeNotReady"
	default:
		return strings.TrimSpace(faultType)
	}
}

func podHasOOMKilledTermination(pod *corev1.Pod) bool {
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		for _, terminated := range []*corev1.ContainerStateTerminated{status.State.Terminated, status.LastTerminationState.Terminated} {
			if terminated != nil && strings.EqualFold(terminated.Reason, "OOMKilled") {
				return true
			}
		}
	}
	return false
}

func podHasCrashLoopSignal(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && strings.EqualFold(status.State.Waiting.Reason, "CrashLoopBackOff") {
			return true
		}
		terminated := status.LastTerminationState.Terminated
		if status.RestartCount > 0 && terminated != nil &&
			terminated.ExitCode != 137 && !strings.EqualFold(terminated.Reason, "OOMKilled") {
			return true
		}
	}
	return false
}

func podHasImagePullSignal(pod *corev1.Pod) bool {
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if status.State.Waiting == nil {
			continue
		}
		reason := normalizeReportFault(status.State.Waiting.Reason)
		if reason == "imagepullbackoff" || reason == "errimagepull" {
			return true
		}
	}
	return false
}

func podHasInitFailureSignal(pod *corev1.Pod) bool {
	if strings.HasPrefix(strings.ToLower(pod.Status.Reason), "init") {
		return true
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" && status.State.Waiting.Reason != "PodInitializing" {
			return true
		}
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			return true
		}
		if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.ExitCode != 0 {
			return true
		}
	}
	return false
}

func podScheduledFalse(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return true
		}
	}
	return false
}

func eventsContain(events []corev1.Event, needle string) bool {
	needle = strings.ToLower(needle)
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Reason+" "+event.Message), needle) {
			return true
		}
	}
	return false
}

func eventsContainFailedProbe(events []corev1.Event) bool {
	for _, event := range events {
		if event.Reason == "Unhealthy" && failedProbeEventMessage(strings.ToLower(event.Message)) {
			return true
		}
	}
	return false
}

func failedProbeEventMessage(message string) bool {
	return strings.Contains(message, "readiness probe failed") ||
		strings.Contains(message, "liveness probe failed") ||
		strings.Contains(message, "startup probe failed")
}

func contextHasNotReadyNode(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Topology != nil && ctx.Topology.Node != nil && !ctx.Topology.Node.Ready {
		return true
	}
	for _, node := range ctx.NodeSnapshots {
		if !node.Ready {
			return true
		}
	}
	return eventsContain(ctx.Events, "nodenotready") || eventsContain(ctx.Events, "node notready")
}

func podUsesPVC(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			return true
		}
	}
	return false
}

func evictionMetricQuery(pod *corev1.Pod) string {
	if pod == nil {
		return "node_memory_MemAvailable_bytes"
	}
	text := strings.ToLower(pod.Status.Reason + " " + pod.Status.Message)
	switch {
	case strings.Contains(text, "ephemeral-storage"), strings.Contains(text, "disk"):
		return "node_filesystem_avail_bytes"
	case strings.Contains(text, "pid"):
		return "node_processes_state"
	default:
		return "node_memory_MemAvailable_bytes"
	}
}

func cloneToolInput(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeToolInput(existing, required map[string]interface{}) map[string]interface{} {
	out := cloneToolInput(existing)
	for key, value := range required {
		out[key] = value
	}
	return out
}
