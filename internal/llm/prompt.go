package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/rag"

	corev1 "k8s.io/api/core/v1"
)

type promptEvidence struct {
	EvidenceID string `json:"evidence_id,omitempty"`
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

type groundedPromptPayload struct {
	PodBasicInfo             map[string]interface{} `json:"pod_basic_info"`
	RuleDiagnosis            RuleBasedResult        `json:"rule_diagnosis"`
	Evidences                []GroundingEvidence    `json:"evidence_list"`
	RemainingEvidenceSummary string                 `json:"remaining_evidence_summary,omitempty"`
	RunbookHits              []rag.Hit              `json:"runbook_retrieval_results"`
	OutputRequirements       map[string]interface{} `json:"output_requirements"`
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

func BuildGroundedPrompt(ctx *diagnostic.DiagnosticContext, ruleResult RuleBasedResult, evidences []diagnostic.EvidenceRecord, runbookHits []rag.Hit, summarizers ...EvidenceSummarizer) GroundedPrompt {
	groundingEvidence, remainingRecords := groundingEvidencesWithSummary(evidences)

	// If a summarizer is available and there are remaining records, use LLM
	// to produce a semantic summary instead of just grouped counts.
	remainingSummary := ""
	if len(remainingRecords) > 0 {
		remainingSummary = summarizeRemainingEvidences(remainingRecords)
		if len(summarizers) > 0 && summarizers[0] != nil {
			if llmSummary, err := summarizers[0].SummarizeEvidences(ctx.RequestContext, remainingRecords); err == nil && llmSummary != "" {
				remainingSummary = llmSummary
			}
		}
	}
	payload := groundedPromptPayload{
		PodBasicInfo:             podBasicInfo(ctx),
		RuleDiagnosis:            ruleResult,
		Evidences:                groundingEvidence,
		RemainingEvidenceSummary: remainingSummary,
		RunbookHits:              runbookHits,
		OutputRequirements: map[string]interface{}{
			"format": "Return JSON only.",
			"schema": map[string]interface{}{
				"root_cause_confirmation": map[string]interface{}{
					"agreement_level": "full_agree | partial_agree | disagree",
					"summary":         "short explanation that does not change rule_diagnosis",
					"evidence_refs":   []string{"ev-0"},
				},
				"evidence_chain": []map[string]interface{}{{
					"claim":         "one factual claim grounded in supplied evidence",
					"evidence_refs": []string{"ev-0"},
				}},
				"additional_observations": []map[string]interface{}{{
					"text":          "optional observation that is not used as root-cause proof",
					"evidence_refs": []string{},
				}},
			},
			"rules": []string{
				"Treat rule_diagnosis as read-only. Do not rewrite fault_type, root_cause_summary, confidence_score, risk_level, or suggested_actions.",
				"Every evidence_chain claim must cite one or more evidence_id values from evidence_list.",
				"Do not invent causes, owners, dependencies, metrics, commands, or remediation steps that are absent from evidence_list.",
				"Put weak or uncited thoughts only in additional_observations, never in evidence_chain.",
			},
		},
	}
	bytes, _ := json.MarshalIndent(payload, "", "  ")
	return GroundedPrompt{
		Prompt: Prompt{
			System: groundedSystemPrompt(),
			User:   string(bytes),
		},
		RuleDiagnosis: ruleResult,
		Evidences:     groundingEvidence,
	}
}

// RuleResultFromReport captures the original deterministic diagnosis before LLM
// enhancement.
func RuleResultFromReport(report *diagnostic.Report) RuleBasedResult {
	if report == nil {
		return RuleBasedResult{}
	}
	return RuleBasedResult{
		FaultType:           report.FaultType,
		RootCauseSummary:    report.RootCauseSummary,
		ConfidenceScore:     report.ConfidenceScore,
		ImpactAnalysis:      report.ImpactAnalysis,
		SuggestedActions:    report.SuggestedActions,
		RemediationActions:  report.RemediationActions,
		RiskLevel:           report.RiskLevel,
		NeedHumanConfirm:    report.NeedHumanConfirm,
		PrimaryRootCause:    report.PrimaryRootCause,
		ContributingFactors: append([]diagnostic.RootCauseFactor(nil), report.ContributingFactors...),
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

func groundedSystemPrompt() string {
	return strings.Join([]string{
		"You are KubeSage's grounded Kubernetes RCA report reviewer.",
		"You must not change rule_diagnosis. It is the authoritative diagnosis produced by deterministic analyzers.",
		"Your job is only to confirm whether the supplied evidence supports that diagnosis and explain the evidence chain.",
		"Every factual claim in evidence_chain must cite at least one evidence_id from evidence_list.",
		"Do not add new remediation actions, commands, causal relationships, dependencies, metrics, or impact claims unless they appear in cited evidence.",
		"Return strict JSON only with fields: root_cause_confirmation, evidence_chain, additional_observations.",
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

// promptEvidences filters, scores, and truncates evidence for prompt size control.
// Evidence is sorted by relevance (severity × recency) and capped at 30 records.
func promptEvidences(records []diagnostic.EvidenceRecord, sourceType string) []promptEvidence {
	// Filter and score.
	type indexed struct {
		record diagnostic.EvidenceRecord
		score  float64
	}
	scored := make([]indexed, 0, len(records))
	for i, record := range records {
		if sourceType != "" && record.SourceType != sourceType {
			continue
		}
		// Simple relevance: severity weight × recency decay.
		sevW := severityWeight(record.Severity)
		recency := 1.0 - float64(len(records)-1-i)*0.02
		if recency < 0.1 {
			recency = 0.1
		}
		scored = append(scored, indexed{record: record, score: sevW*0.5 + recency*0.5})
	}

	// Sort by score descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top 30, truncating content.
	result := make([]promptEvidence, 0, 30)
	for _, s := range scored {
		result = append(result, promptEvidence{
			SourceType: s.record.SourceType,
			Title:      s.record.Title,
			Content:    truncate(s.record.Content, 1200),
			Severity:   s.record.Severity,
		})
		if len(result) >= 30 {
			return result
		}
	}
	return result
}

// severityWeight maps evidence severity to a numeric weight for relevance scoring.
func severityWeight(severity string) float64 {
	switch strings.ToLower(severity) {
	case "critical":
		return 1.0
	case "error":
		return 0.85
	case "warning":
		return 0.6
	case "info":
		return 0.3
	default:
		return 0.2
	}
}

// groundingEvidencesWithSummary returns the top 30 evidence items sorted by
// severity, plus any remaining items that were not included. The caller can
// then summarize the remaining items via LLM or statistical grouping.
func groundingEvidencesWithSummary(records []diagnostic.EvidenceRecord) ([]GroundingEvidence, []diagnostic.EvidenceRecord) {
	const limit = 30

	// Sort by severity first.
	sort.Slice(records, func(i, j int) bool {
		return severityWeight(records[i].Severity) > severityWeight(records[j].Severity)
	})

	result := make([]GroundingEvidence, 0, limit)
	for i, record := range records {
		result = append(result, GroundingEvidence{
			ID:         fmt.Sprintf("ev-%d", i),
			SourceType: record.SourceType,
			Title:      record.Title,
			Content:    truncate(record.Content, 1200),
			Severity:   record.Severity,
		})
		if len(result) >= limit {
			break
		}
	}

	// Build summary of remaining items (index limit..end).
	if len(records) <= limit {
		return result, nil
	}
	remaining := records[limit:]
	return result, remaining
}

// summarizeRemainingEvidences produces a compact grouped summary of evidence
// records that were not included in the top-N detailed list. Groups by
// sourceType and reports count + severity distribution per group.
func summarizeRemainingEvidences(records []diagnostic.EvidenceRecord) string {
	type groupInfo struct {
		count      int
		severities map[string]int
		titles     []string // collect up to 3 representative titles
	}
	groups := map[string]*groupInfo{}
	for _, r := range records {
		g, ok := groups[r.SourceType]
		if !ok {
			g = &groupInfo{severities: map[string]int{}}
			groups[r.SourceType] = g
		}
		g.count++
		g.severities[r.Severity]++
		if len(g.titles) < 3 {
			g.titles = append(g.titles, r.Title)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d additional evidence records not shown in detail:\n", len(records)))
	for source, g := range groups {
		sevParts := make([]string, 0, len(g.severities))
		for sev, cnt := range g.severities {
			sevParts = append(sevParts, fmt.Sprintf("%s=%d", sev, cnt))
		}
		titleHint := ""
		if len(g.titles) > 0 {
			titleHint = " (e.g. " + strings.Join(g.titles, "; ") + ")"
		}
		b.WriteString(fmt.Sprintf("  - %s: %d records [%s]%s\n", source, g.count, strings.Join(sevParts, ", "), titleHint))
	}
	return b.String()
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
