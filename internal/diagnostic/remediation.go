package diagnostic

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	remediationViewPreviousLogs       = "view_previous_logs"
	remediationAdjustMemoryLimit      = "adjust_memory_limit"
	remediationCheckConfigMapSecret   = "check_configmap_secret"
	remediationExtendProbeDelay       = "extend_probe_initial_delay"
	remediationFixProbePath           = "fix_probe_path"
	remediationCheckTaintToleration   = "check_node_taint_toleration"
	remediationMVPExecutable          = false
	defaultRemediationContainerTarget = "<container>"
)

type probeContainerRef struct {
	Index     int
	Container corev1.Container
}

var forbiddenRemediationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
	regexp.MustCompile(`(?i)\bdelete\s+from\b`),
	regexp.MustCompile(`(?i)\btruncate\s+table\b`),
	regexp.MustCompile(`(?i)\brm\s+-rf\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(ns|namespace|namespaces)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(pvc|persistentvolumeclaim|persistentvolumeclaims)\b`),
}

// AttachRemediationActions adds structured, risk-aware remediation suggestions
// to the report. MVP actions are advisory only and are never marked executable.
func AttachRemediationActions(ctx *DiagnosticContext, report *Report) {
	if ctx == nil || report == nil {
		return
	}
	report.RemediationActions = GenerateRemediationActions(ctx, report)
}

// GenerateRemediationActions maps the detected fault type and Kubernetes
// context into reviewable actions with explicit risk and confirmation metadata.
func GenerateRemediationActions(ctx *DiagnosticContext, report *Report) []RemediationAction {
	if ctx == nil || report == nil {
		return nil
	}

	actions := []RemediationAction{}
	faultType := strings.ToLower(report.FaultType)

	if strings.Contains(faultType, "oomkilled") {
		actions = append(actions, previousLogActions(ctx)...)
		actions = append(actions, adjustMemoryLimitActions(ctx)...)
	}
	if strings.Contains(faultType, "crashloopbackoff") {
		actions = append(actions, previousLogActions(ctx)...)
		actions = append(actions, checkConfigMapSecretAction(ctx))
	}
	if strings.Contains(faultType, "probefailed") || strings.Contains(faultType, "probe failed") {
		actions = append(actions, previousLogActions(ctx)...)
		actions = append(actions, extendProbeDelayActions(ctx)...)
		actions = append(actions, fixProbePathActions(ctx)...)
	}
	if strings.Contains(faultType, "pending") || hasSchedulingEvidence(report) || nodeHasRemediationPressure(ctx) {
		actions = append(actions, checkNodeTaintTolerationAction(ctx))
	}

	return sanitizeRemediationActions(actions)
}

// previousLogActions suggests reading previous container logs around restarts.
func previousLogActions(ctx *DiagnosticContext) []RemediationAction {
	namespace, podName := podIdentity(ctx)
	containers := remediationContainers(ctx)
	actions := make([]RemediationAction, 0, len(containers))
	for _, container := range containers {
		actions = append(actions, RemediationAction{
			ActionType:       remediationViewPreviousLogs,
			Description:      fmt.Sprintf("Review previous logs for container %s to inspect the failure window before restart.", container),
			CommandPreview:   fmt.Sprintf("kubectl logs -n %s %s -c %s --previous --timestamps --tail=200", namespace, podName, container),
			RiskLevel:        "low",
			NeedHumanConfirm: false,
			Executable:       remediationMVPExecutable,
		})
	}
	return actions
}

// adjustMemoryLimitActions suggests a human-reviewed memory limit change.
func adjustMemoryLimitActions(ctx *DiagnosticContext) []RemediationAction {
	namespace := safeNamespace(ctx)
	target := workloadTarget(ctx)
	containers := remediationContainers(ctx)
	actions := make([]RemediationAction, 0, len(containers))
	for _, container := range containers {
		actions = append(actions, RemediationAction{
			ActionType:       remediationAdjustMemoryLimit,
			Description:      fmt.Sprintf("Review memory usage evidence and raise the memory limit for container %s only after capacity and regression risk are confirmed.", container),
			CommandPreview:   fmt.Sprintf("kubectl set resources %s -n %s --containers=%s --limits=memory=<reviewed-memory-limit> --dry-run=server -o yaml", target, namespace, container),
			RiskLevel:        "high",
			NeedHumanConfirm: true,
			Executable:       remediationMVPExecutable,
		})
	}
	return actions
}

// checkConfigMapSecretAction suggests read-only inspection of mounted config.
func checkConfigMapSecretAction(ctx *DiagnosticContext) RemediationAction {
	namespace, podName := podIdentity(ctx)
	return RemediationAction{
		ActionType:       remediationCheckConfigMapSecret,
		Description:      "Check referenced ConfigMaps, Secrets, envFrom entries, and mounted files for missing keys or invalid values.",
		CommandPreview:   fmt.Sprintf("kubectl describe pod -n %s %s; kubectl get configmap,secret -n %s --show-labels", namespace, podName, namespace),
		RiskLevel:        "low",
		NeedHumanConfirm: false,
		Executable:       remediationMVPExecutable,
	}
}

// extendProbeDelayActions suggests increasing initialDelaySeconds via dry-run.
func extendProbeDelayActions(ctx *DiagnosticContext) []RemediationAction {
	namespace := safeNamespace(ctx)
	target := workloadTarget(ctx)
	actions := []RemediationAction{}
	for _, ref := range probeContainers(ctx) {
		actions = append(actions, RemediationAction{
			ActionType:       remediationExtendProbeDelay,
			Description:      fmt.Sprintf("If container %s starts slowly, review whether readiness/liveness initialDelaySeconds should be extended.", ref.Container.Name),
			CommandPreview:   fmt.Sprintf("kubectl patch %s -n %s --type=json --patch='[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/%d/readinessProbe/initialDelaySeconds\",\"value\":<reviewed-seconds>}]' --dry-run=server -o yaml", target, namespace, ref.Index),
			RiskLevel:        "medium",
			NeedHumanConfirm: true,
			Executable:       remediationMVPExecutable,
		})
	}
	return actions
}

// fixProbePathActions suggests correcting readiness or liveness HTTP paths.
func fixProbePathActions(ctx *DiagnosticContext) []RemediationAction {
	namespace := safeNamespace(ctx)
	target := workloadTarget(ctx)
	actions := []RemediationAction{}
	for _, ref := range probeContainers(ctx) {
		if ref.Container.ReadinessProbe != nil && ref.Container.ReadinessProbe.HTTPGet != nil {
			actions = append(actions, probePathAction(target, namespace, ref.Index, ref.Container.Name, "readinessProbe"))
		}
		if ref.Container.LivenessProbe != nil && ref.Container.LivenessProbe.HTTPGet != nil {
			actions = append(actions, probePathAction(target, namespace, ref.Index, ref.Container.Name, "livenessProbe"))
		}
	}
	return actions
}

// probePathAction builds a dry-run preview for one HTTP probe path update.
func probePathAction(target, namespace string, index int, containerName, probeName string) RemediationAction {
	return RemediationAction{
		ActionType:       remediationFixProbePath,
		Description:      fmt.Sprintf("Verify and correct %s HTTP path for container %s if Events show 404/503 or wrong endpoint routing.", probeName, containerName),
		CommandPreview:   fmt.Sprintf("kubectl patch %s -n %s --type=json --patch='[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/%d/%s/httpGet/path\",\"value\":\"<correct-health-path>\"}]' --dry-run=server -o yaml", target, namespace, index, probeName),
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
		Executable:       remediationMVPExecutable,
	}
}

// checkNodeTaintTolerationAction suggests read-only scheduler constraint checks.
func checkNodeTaintTolerationAction(ctx *DiagnosticContext) RemediationAction {
	namespace, podName := podIdentity(ctx)
	nodeName := "<node>"
	if ctx != nil && ctx.Pod != nil && ctx.Pod.Spec.NodeName != "" {
		nodeName = ctx.Pod.Spec.NodeName
	}
	return RemediationAction{
		ActionType:       remediationCheckTaintToleration,
		Description:      "Check whether node taints, pod tolerations, nodeSelector, or affinity are blocking scheduling or causing node-level pressure.",
		CommandPreview:   fmt.Sprintf("kubectl describe node %s; kubectl get pod -n %s %s -o jsonpath='{.spec.tolerations}'", nodeName, namespace, podName),
		RiskLevel:        "low",
		NeedHumanConfirm: false,
		Executable:       remediationMVPExecutable,
	}
}

// sanitizeRemediationActions normalizes risk metadata and drops forbidden
// command previews such as database, PVC, or Namespace deletion.
func sanitizeRemediationActions(actions []RemediationAction) []RemediationAction {
	result := make([]RemediationAction, 0, len(actions))
	seen := map[string]struct{}{}
	for _, action := range actions {
		action.RiskLevel = normalizeRemediationRisk(action.RiskLevel)
		action.Executable = remediationMVPExecutable
		if action.RiskLevel == "medium" || action.RiskLevel == "high" {
			action.NeedHumanConfirm = true
		}
		if action.ActionType == "" || forbiddenRemediationCommand(action.CommandPreview) {
			continue
		}
		key := action.ActionType + "\x00" + action.CommandPreview
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, action)
	}
	return result
}

// forbiddenRemediationCommand detects actions KubeSage must never recommend.
func forbiddenRemediationCommand(command string) bool {
	for _, pattern := range forbiddenRemediationPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// normalizeRemediationRisk maps unknown risk levels to medium.
func normalizeRemediationRisk(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(risk))
	default:
		return "medium"
	}
}

// remediationContainers returns pod container names, or a placeholder when the
// pod snapshot is unavailable.
func remediationContainers(ctx *DiagnosticContext) []string {
	if ctx == nil || ctx.Pod == nil || len(ctx.Pod.Spec.Containers) == 0 {
		return []string{defaultRemediationContainerTarget}
	}
	result := make([]string, 0, len(ctx.Pod.Spec.Containers))
	for _, container := range ctx.Pod.Spec.Containers {
		result = append(result, container.Name)
	}
	return result
}

// probeContainers returns probed containers with their original Pod spec index.
func probeContainers(ctx *DiagnosticContext) []probeContainerRef {
	if ctx == nil || ctx.Pod == nil {
		return nil
	}
	result := []probeContainerRef{}
	for index, container := range ctx.Pod.Spec.Containers {
		if container.ReadinessProbe != nil || container.LivenessProbe != nil {
			result = append(result, probeContainerRef{Index: index, Container: container})
		}
	}
	return result
}

// workloadTarget prefers Deployment for previews and falls back to Pod.
func workloadTarget(ctx *DiagnosticContext) string {
	if ctx != nil && ctx.Topology != nil && ctx.Topology.DeploymentName != "" {
		return "deployment/" + ctx.Topology.DeploymentName
	}
	if ctx != nil && ctx.PodName != "" {
		return "pod/" + ctx.PodName
	}
	return "pod/<pod>"
}

// podIdentity returns namespace and pod name placeholders when missing.
func podIdentity(ctx *DiagnosticContext) (string, string) {
	namespace := safeNamespace(ctx)
	podName := "<pod>"
	if ctx != nil && ctx.PodName != "" {
		podName = ctx.PodName
	}
	return namespace, podName
}

// safeNamespace returns a namespace placeholder when context is incomplete.
func safeNamespace(ctx *DiagnosticContext) string {
	if ctx != nil && ctx.Namespace != "" {
		return ctx.Namespace
	}
	return "<namespace>"
}

// hasSchedulingEvidence detects scheduler-related evidence in the rule report.
func hasSchedulingEvidence(report *Report) bool {
	if report == nil {
		return false
	}
	for _, evidence := range report.Evidences {
		text := strings.ToLower(evidence.Title + " " + evidence.Content)
		if strings.Contains(text, "failedscheduling") || strings.Contains(text, "taint") || strings.Contains(text, "toleration") {
			return true
		}
	}
	return false
}

// nodeHasRemediationPressure reports node pressure from topology facts.
func nodeHasRemediationPressure(ctx *DiagnosticContext) bool {
	if ctx == nil || ctx.Topology == nil || ctx.Topology.Node == nil {
		return false
	}
	node := ctx.Topology.Node
	return node.MemoryPressure || node.DiskPressure || node.PIDPressure
}
