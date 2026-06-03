package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

// BuildReportSnapshot converts runtime state into the structured Agent report
// sections stored on diagnosis_reports.agent_report_snapshot.
func BuildReportSnapshot(report *diagnostic.Report, hypotheses []model.Hypothesis, hits []RunbookHit, executions []model.RemediationExecution, verification []VerificationPlan, stopReason string) ReportSnapshot {
	summary := executionSummary(report, hypotheses, stopReason)
	chain := evidenceChain(report)
	primary, contributing := rootCauseFactors(report, hypotheses, chain)
	return ReportSnapshot{
		RuleBasedResult:       reportValue(report, func(r *diagnostic.Report) interface{} { return r.RuleBasedResult }),
		AgentExecutionSummary: summary,
		Hypotheses:            hypotheses,
		EvidenceChain:         chain,
		PrimaryRootCause:      primary,
		ContributingFactors:   contributing,
		RootCauseEvidenceRefs: rootCauseEvidenceRefs(report, chain),
		ConfidenceBreakdown:   confidenceBreakdown(report, chain),
		MissingEvidence:       missingEvidence(report, hypotheses, chain),
		RunbookGuidance:       hits,
		LLMEnhancedSummary:    reportValue(report, func(r *diagnostic.Report) interface{} { return r.LLMEnhancedSummary }),
		RemediationActions:    reportActions(report),
		RemediationExecutions: executions,
		VerificationPlan:      verification,
		ResidualRisks:         residualRisks(hypotheses, executions, stopReason),
		StopReason:            stopReason,
	}
}

func executionSummary(report *diagnostic.Report, hypotheses []model.Hypothesis, stopReason string) string {
	faultType := "unknown"
	confidence := 0.0
	if report != nil {
		faultType = report.FaultType
		confidence = report.ConfidenceScore
	}
	confirmed := []string{}
	active := []string{}
	for _, hypothesis := range hypotheses {
		switch hypothesis.Status {
		case model.HypothesisStatusConfirmed:
			confirmed = append(confirmed, hypothesis.HypothesisType)
		case model.HypothesisStatusActive:
			active = append(active, hypothesis.HypothesisType)
		}
	}
	primary, contributing := rootCauseFactors(report, hypotheses, nil)
	primaryType := ""
	if primary != nil {
		primaryType = primary.HypothesisType
	}
	contributingTypes := make([]string, 0, len(contributing))
	for _, factor := range contributing {
		contributingTypes = append(contributingTypes, factor.HypothesisType)
	}
	return fmt.Sprintf("Agent completed with stop_reason=%s fault_type=%s rule_confidence=%.2f primary_root_cause=%s contributing_factors=%s confirmed=%s active=%s", stopReason, faultType, confidence, primaryType, strings.Join(contributingTypes, ","), strings.Join(confirmed, ","), strings.Join(active, ","))
}

func rootCauseFactors(report *diagnostic.Report, hypotheses []model.Hypothesis, chain []EvidenceRef) (*RootCauseFactor, []RootCauseFactor) {
	if len(hypotheses) == 0 {
		if report == nil {
			return nil, nil
		}
		if report.PrimaryRootCause != nil {
			primary := factorFromDiagnostic(*report.PrimaryRootCause)
			contributing := make([]RootCauseFactor, 0, len(report.ContributingFactors))
			for _, factor := range report.ContributingFactors {
				contributing = append(contributing, factorFromDiagnostic(factor))
			}
			return &primary, contributing
		}
		return &RootCauseFactor{
			HypothesisType:  report.FaultType,
			Summary:         report.RootCauseSummary,
			ConfidenceScore: report.ConfidenceScore,
			Status:          "rule_based",
			EvidenceRefs:    rootCauseEvidenceRefs(report, chain),
			Reason:          "deterministic analyzer result",
		}, nil
	}

	ranked := append([]model.Hypothesis(nil), hypotheses...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return betterPrimaryHypothesis(ranked[i], ranked[j])
	})

	primaryIndex := -1
	for i, hypothesis := range ranked {
		if hypothesis.Status != model.HypothesisStatusRejected {
			primaryIndex = i
			break
		}
	}
	if primaryIndex == -1 {
		if report == nil {
			return nil, nil
		}
		return &RootCauseFactor{
			HypothesisType:  report.FaultType,
			Summary:         report.RootCauseSummary,
			ConfidenceScore: report.ConfidenceScore,
			Status:          "rule_based",
			EvidenceRefs:    rootCauseEvidenceRefs(report, chain),
			Reason:          "deterministic analyzer fallback because all hypotheses were rejected",
		}, nil
	}

	primary := factorFromHypothesis(ranked[primaryIndex], "highest-confidence non-rejected hypothesis")
	contributing := []RootCauseFactor{}
	for i, hypothesis := range ranked {
		if i == primaryIndex {
			continue
		}
		if !isContributingFactor(hypothesis) {
			continue
		}
		contributing = append(contributing, factorFromHypothesis(hypothesis, "high-confidence supporting or competing hypothesis"))
		if len(contributing) >= 5 {
			break
		}
	}
	return &primary, contributing
}

func factorFromDiagnostic(factor diagnostic.RootCauseFactor) RootCauseFactor {
	return RootCauseFactor{
		HypothesisType:  factor.FaultType,
		Summary:         factor.Summary,
		ConfidenceScore: factor.ConfidenceScore,
		Status:          factor.ContributingRole,
		EvidenceRefs:    append([]string(nil), factor.EvidenceRefs...),
		Reason:          factor.AnalyzerName,
	}
}

func factorFromHypothesis(hypothesis model.Hypothesis, reason string) RootCauseFactor {
	return RootCauseFactor{
		HypothesisType:  hypothesis.HypothesisType,
		Summary:         hypothesis.Summary,
		ConfidenceScore: hypothesis.ConfidenceScore,
		Status:          hypothesis.Status,
		EvidenceRefs:    jsonTextItems(hypothesis.SupportingEvidenceRefs),
		MissingEvidence: jsonTextItems(hypothesis.MissingEvidence),
		Reason:          reason,
	}
}

func isContributingFactor(hypothesis model.Hypothesis) bool {
	if hypothesis.Status == model.HypothesisStatusRejected {
		return false
	}
	return hypothesis.Status == model.HypothesisStatusConfirmed || hypothesis.ConfidenceScore >= 0.45
}

func evidenceChain(report *diagnostic.Report) []EvidenceRef {
	if report == nil {
		return nil
	}
	result := make([]EvidenceRef, 0, len(report.Evidences))
	for i, record := range report.Evidences {
		result = append(result, EvidenceRef{
			Ref:        fmt.Sprintf("E%d", i+1),
			SourceType: record.SourceType,
			Title:      record.Title,
			Severity:   record.Severity,
		})
	}
	return result
}

func rootCauseEvidenceRefs(report *diagnostic.Report, chain []EvidenceRef) []string {
	if report == nil {
		return nil
	}
	refs := []string{}
	for i, record := range report.Evidences {
		if i >= len(chain) {
			break
		}
		text := strings.ToLower(record.SourceType + " " + record.Title + " " + record.Content + " " + record.Severity)
		if record.Severity == "critical" || record.Severity == "warning" || evidenceMentionsFault(text, report.FaultType) {
			refs = append(refs, chain[i].Ref)
		}
		if len(refs) >= 6 {
			break
		}
	}
	return refs
}

func confidenceBreakdown(report *diagnostic.Report, chain []EvidenceRef) []ConfidenceComponent {
	if report == nil {
		return nil
	}
	sourceGroups := []struct {
		name    string
		weight  float64
		sources []string
		reason  string
	}{
		{name: "pod_status", weight: 0.30, sources: []string{"k8s_pod", "k8s_pod_status"}, reason: "Pod status, container state, restart count, requests, and limits."},
		{name: "events", weight: 0.20, sources: []string{"k8s_event"}, reason: "Kubernetes Events near the fault window."},
		{name: "logs", weight: 0.15, sources: []string{"k8s_log", "k8s_key_log", "loki"}, reason: "Container logs and centralized log entries."},
		{name: "metrics", weight: 0.15, sources: []string{"prometheus", "prometheus_trend"}, reason: "Prometheus samples and trend evidence."},
		{name: "topology", weight: 0.10, sources: []string{"k8s_topology", "k8s_pvc", "k8s_node", "correlation_evidence"}, reason: "Workload, PVC, node, and blast-radius context."},
		{name: "runbook", weight: 0.05, sources: []string{"runbook"}, reason: "Runbook guidance matched to the suspected fault."},
	}
	components := make([]ConfidenceComponent, 0, len(sourceGroups))
	for _, group := range sourceGroups {
		refs := refsForSources(report, chain, group.sources)
		component := ConfidenceComponent{
			Source:       group.name,
			Weight:       group.weight,
			EvidenceRefs: refs,
			Missing:      len(refs) == 0,
			Reason:       group.reason,
		}
		if component.Missing {
			component.Weight = 0
			component.Reason = "Missing " + group.reason
		}
		components = append(components, component)
	}
	return components
}

func missingEvidence(report *diagnostic.Report, hypotheses []model.Hypothesis, chain []EvidenceRef) []string {
	items := []string{}
	for _, component := range confidenceBreakdown(report, chain) {
		if component.Missing {
			items = append(items, component.Source)
		}
	}
	for _, hypothesis := range hypotheses {
		items = append(items, jsonTextItems(hypothesis.MissingEvidence)...)
	}
	if report != nil && strings.EqualFold(report.FaultType, "OOMKilled") && len(refsForSources(report, chain, []string{"prometheus", "prometheus_trend"})) == 0 {
		items = append(items, "prometheus metrics for memory near limit")
	}
	return appendUniqueStrings(items)
}

func refsForSources(report *diagnostic.Report, chain []EvidenceRef, sources []string) []string {
	if report == nil {
		return nil
	}
	sourceSet := map[string]struct{}{}
	for _, source := range sources {
		sourceSet[source] = struct{}{}
	}
	refs := []string{}
	for i, record := range report.Evidences {
		if i >= len(chain) {
			break
		}
		if _, ok := sourceSet[record.SourceType]; ok {
			refs = append(refs, chain[i].Ref)
		}
	}
	return refs
}

func evidenceMentionsFault(text, faultType string) bool {
	normalizedText := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(text))
	for _, part := range strings.Split(strings.ToLower(faultType), ",") {
		part = strings.TrimSpace(part)
		part = strings.NewReplacer("_", "", "-", "", " ", "").Replace(part)
		if part != "" && strings.Contains(normalizedText, part) {
			return true
		}
	}
	return false
}

func jsonTextItems(value model.JSONText) []string {
	text := strings.TrimSpace(string(value))
	if text == "" || text == "null" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(text), &items); err == nil {
		return items
	}
	return []string{text}
}

func appendUniqueStrings(items []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func residualRisks(hypotheses []model.Hypothesis, executions []model.RemediationExecution, stopReason string) []string {
	risks := []string{}
	for _, hypothesis := range hypotheses {
		if hypothesis.Status == model.HypothesisStatusActive {
			risks = append(risks, "active hypothesis remains: "+hypothesis.HypothesisType)
		}
	}
	for _, execution := range executions {
		if execution.Status == model.RemediationExecutionStatusPendingApproval {
			risks = append(risks, "manual approval required for action: "+execution.ActionID)
		}
		if execution.Status == model.RemediationExecutionStatusManualAck {
			risks = append(risks, "manual remediation handling remains outside KubeSage automation: "+execution.ActionID)
		}
		if execution.Status == model.RemediationExecutionStatusBlocked {
			risks = append(risks, "blocked remediation action: "+execution.ActionID)
		}
	}
	if stopReason == StopReasonTimeout || stopReason == StopReasonMaxSteps {
		risks = append(risks, "diagnosis stopped before all possible observations were collected")
	}
	if len(risks) == 0 {
		risks = append(risks, "no additional residual risk identified by MVP agent")
	}
	return risks
}

func reportValue(report *diagnostic.Report, getter func(*diagnostic.Report) interface{}) interface{} {
	if report == nil {
		return nil
	}
	return getter(report)
}

func reportActions(report *diagnostic.Report) []diagnostic.RemediationAction {
	if report == nil {
		return nil
	}
	return report.RemediationActions
}
