package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/rag"

	corev1 "k8s.io/api/core/v1"
)

type promptEvidence struct {
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Severity   string `json:"severity"`
}

type promptEvent struct {
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp,omitempty"`
}

type promptPayload struct {
	PodBasicInfo       map[string]interface{} `json:"pod_basic_info"`
	FaultType          string                 `json:"fault_type"`
	RuleBasedResult    RuleBasedResult        `json:"rule_based_result"`
	Evidences          []promptEvidence       `json:"evidence_list"`
	EventsSummary      []promptEvent          `json:"events_summary"`
	KeyLogFragments    []promptEvidence       `json:"key_log_fragments"`
	PrometheusSummary  []promptEvidence       `json:"prometheus_metric_summary"`
	RunbookHits        []rag.Hit              `json:"runbook_retrieval_results"`
	OutputRequirements map[string]string      `json:"output_requirements"`
}

// BuildPrompt converts the rule report and live diagnostic context into the
// strict JSON prompt expected by the LLM.
func BuildPrompt(ctx *diagnostic.DiagnosticContext, ruleResult RuleBasedResult, evidences []diagnostic.EvidenceRecord, runbookHits []rag.Hit) Prompt {
	payload := promptPayload{
		PodBasicInfo:      podBasicInfo(ctx),
		FaultType:         ruleResult.FaultType,
		RuleBasedResult:   ruleResult,
		Evidences:         promptEvidences(evidences, ""),
		EventsSummary:     promptEvents(ctx),
		KeyLogFragments:   promptEvidences(evidences, "k8s_key_log"),
		PrometheusSummary: promptEvidences(evidences, "prometheus"),
		RunbookHits:       runbookHits,
		OutputRequirements: map[string]string{
			"format": "Return JSON only.",
			"schema": "root_cause_summary, confidence_score, evidence_reasoning, suggested_actions, risk_level, need_human_confirm",
			"safety": "Do not produce dangerous commands as executable actions. Suggestions must require human confirmation.",
		},
	}
	bytes, _ := json.MarshalIndent(payload, "", "  ")
	return Prompt{
		System: systemPrompt(),
		User:   string(bytes),
	}
}

// RuleResultFromReport captures the original deterministic diagnosis before LLM
// enhancement.
func RuleResultFromReport(report *diagnostic.Report) RuleBasedResult {
	if report == nil {
		return RuleBasedResult{}
	}
	return RuleBasedResult{
		FaultType:          report.FaultType,
		RootCauseSummary:   report.RootCauseSummary,
		ConfidenceScore:    report.ConfidenceScore,
		ImpactAnalysis:     report.ImpactAnalysis,
		SuggestedActions:   report.SuggestedActions,
		RemediationActions: report.RemediationActions,
		RiskLevel:          report.RiskLevel,
		NeedHumanConfirm:   report.NeedHumanConfirm,
	}
}

// systemPrompt sets strict boundaries: summarize only, never execute.
func systemPrompt() string {
	return strings.Join([]string{
		"You are KubeSage's Kubernetes RCA report writer.",
		"Use the rule_based_result as the primary source of truth; do not replace deterministic evidence with speculation.",
		"Summarize, explain, and suggest human-reviewed next steps only.",
		"Never claim that you executed or will execute operations.",
		"Do not output dangerous commands such as delete, patch, replace, scale, or apply as actions.",
		"Return strict JSON only with fields: root_cause_summary, confidence_score, evidence_reasoning, suggested_actions, risk_level, need_human_confirm.",
	}, "\n")
}

// podBasicInfo extracts compact pod metadata for the prompt.
func podBasicInfo(ctx *diagnostic.DiagnosticContext) map[string]interface{} {
	info := map[string]interface{}{}
	if ctx == nil {
		return info
	}
	info["namespace"] = ctx.Namespace
	info["pod_name"] = ctx.PodName
	if ctx.FaultTime.IsZero() {
		info["fault_time"] = ""
	} else {
		info["fault_time"] = ctx.FaultTime.Format(time.RFC3339)
	}
	if ctx.Pod == nil {
		return info
	}
	info["node_name"] = ctx.Pod.Spec.NodeName
	info["phase"] = string(ctx.Pod.Status.Phase)
	info["labels"] = ctx.Pod.Labels
	info["containers"] = podContainers(ctx.Pod)
	return info
}

// podContainers summarizes container status and resources.
func podContainers(pod *corev1.Pod) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(pod.Spec.Containers))
	statusByName := map[string]corev1.ContainerStatus{}
	for _, status := range pod.Status.ContainerStatuses {
		statusByName[status.Name] = status
	}
	for _, container := range pod.Spec.Containers {
		item := map[string]interface{}{
			"name":           container.Name,
			"memory_request": container.Resources.Requests.Memory().String(),
			"memory_limit":   container.Resources.Limits.Memory().String(),
			"cpu_request":    container.Resources.Requests.Cpu().String(),
			"cpu_limit":      container.Resources.Limits.Cpu().String(),
		}
		if status, ok := statusByName[container.Name]; ok {
			item["ready"] = status.Ready
			item["restart_count"] = status.RestartCount
			if status.LastTerminationState.Terminated != nil {
				item["last_terminated_reason"] = status.LastTerminationState.Terminated.Reason
				item["last_terminated_exit_code"] = status.LastTerminationState.Terminated.ExitCode
				item["last_terminated_finished_at"] = status.LastTerminationState.Terminated.FinishedAt.Time.Format(time.RFC3339)
			}
		}
		items = append(items, item)
	}
	return items
}

// promptEvidences filters and truncates evidence for prompt size control.
func promptEvidences(records []diagnostic.EvidenceRecord, sourceType string) []promptEvidence {
	result := make([]promptEvidence, 0)
	for _, record := range records {
		if sourceType != "" && record.SourceType != sourceType {
			continue
		}
		result = append(result, promptEvidence{
			SourceType: record.SourceType,
			Title:      record.Title,
			Content:    truncate(record.Content, 1200),
			Severity:   record.Severity,
		})
		if len(result) >= 30 {
			return result
		}
	}
	return result
}

// promptEvents summarizes Kubernetes Events for the LLM.
func promptEvents(ctx *diagnostic.DiagnosticContext) []promptEvent {
	if ctx == nil {
		return nil
	}
	result := make([]promptEvent, 0, len(ctx.Events))
	for _, event := range ctx.Events {
		ts := event.LastTimestamp.Time
		if ts.IsZero() {
			ts = event.EventTime.Time
		}
		item := promptEvent{
			Reason:  event.Reason,
			Message: truncate(event.Message, 800),
			Type:    event.Type,
		}
		if !ts.IsZero() {
			item.Timestamp = ts.Format(time.RFC3339)
		}
		result = append(result, item)
		if len(result) >= 20 {
			return result
		}
	}
	return result
}

// truncate limits text size while making truncation explicit.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return fmt.Sprintf("%s...<truncated>", text[:limit])
}
