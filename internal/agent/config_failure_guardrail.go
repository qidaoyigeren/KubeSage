package agent

import (
	"fmt"
	"regexp"
	"strings"

	"kubesage/internal/diagnostic"
)

const configEvidenceGuardrail = "config_evidence_guardrail"

var configContainerPattern = regexp.MustCompile(`(?i)\bcontainer=([a-z0-9]([-a-z0-9]*[a-z0-9])?)`)

func ensureConfigFailureEvidence(plan *Plan, state *ToolState) bool {
	if plan == nil || state == nil {
		return false
	}
	containerName, matched := configFailureContainer(state.EvidenceSnapshot())
	if !matched {
		return false
	}

	baseInput := map[string]interface{}{
		"namespace": state.Goal.Namespace,
		"pod_name":  state.Goal.PodName,
	}
	if containerName != "" {
		baseInput["container_name"] = containerName
	}
	requests := []PlanStep{
		{
			ToolName:   "k8s.get_config_refs",
			Input:      cloneToolInput(baseInput),
			Reason:     "Configuration-file failure detected; inspect live ConfigMap/Secret keys and volume mount paths",
			AppendedBy: configEvidenceGuardrail,
		},
		{
			ToolName: "runbook.search",
			Input: map[string]interface{}{
				"fault_type": firstNonEmptyString(state.Goal.ExpectedFault, "CrashLoopBackOff"),
				"query":      "config file not found configmap secret volume mount path",
			},
			Reason:     "Configuration-file failure detected; retrieve the matching runbook checks",
			AppendedBy: configEvidenceGuardrail,
		},
	}

	changed := false
	for _, step := range requests {
		tool := canonicalToolName(step.ToolName)
		if !toolAvailableInState(state, tool) ||
			toolCompletedWithInput(state, tool, step.Input) ||
			hasPendingStepWithMatchingInput(plan, tool, step.Input) {
			continue
		}
		step.ID = uniqueReflectionStepID(plan, PlanStep{ToolName: step.ToolName, ID: "guard-config-" + strings.ReplaceAll(step.ToolName, ".", "-")})
		plan.Steps = append(plan.Steps, step)
		changed = true
	}
	return changed
}

func configFailureContainer(records []diagnostic.EvidenceRecord) (string, bool) {
	for _, record := range records {
		text := strings.ToLower(record.Title + "\n" + record.Content)
		if record.SourceType == "k8s_event" && configReferenceFailureSignal(text) {
			return "", true
		}
		if (record.SourceType != "k8s_log" && record.SourceType != "loki") || !configFileFailureSignal(text) {
			continue
		}
		if logs, ok := record.Raw.(diagnostic.ContainerLogs); ok {
			return logs.ContainerName, true
		}
		if match := configContainerPattern.FindStringSubmatch(record.Content); len(match) > 1 {
			return match[1], true
		}
		return "", true
	}
	return "", false
}

func configReferenceFailureSignal(text string) bool {
	text = strings.ToLower(text)
	configObject := strings.Contains(text, "configmap") || strings.Contains(text, "secret")
	return configObject && (strings.Contains(text, "not found") ||
		strings.Contains(text, "couldn't find key") ||
		strings.Contains(text, "could not find key") ||
		strings.Contains(text, "references non-existent"))
}

func configFileFailureSignal(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "config file not found") ||
		strings.Contains(text, "configuration file not found") ||
		strings.Contains(text, "missing config file") ||
		(strings.Contains(text, "no such file") && strings.Contains(text, "config"))
}

func configGuardrailSummary(state *ToolState) string {
	container, matched := configFailureContainer(state.EvidenceSnapshot())
	if !matched {
		return ""
	}
	if container == "" {
		return "configuration-file failure detected"
	}
	return fmt.Sprintf("configuration-file failure detected in container %s", container)
}
