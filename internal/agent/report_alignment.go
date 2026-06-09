package agent

import (
	"fmt"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

type ReportAlignmentResult struct {
	Changed       bool
	Reason        string
	OriginalFault string
	AlignedFault  string
	Primary       string
}

// AlignReportWithHypotheses lets confirmed hypotheses refine a report without
// displacing a deterministic analyzer result backed by structured evidence.
func AlignReportWithHypotheses(report *diagnostic.Report, hypotheses []model.Hypothesis) ReportAlignmentResult {
	if report == nil || len(hypotheses) == 0 {
		return ReportAlignmentResult{}
	}
	primary := selectPrimaryHypothesis(hypotheses)
	if primary == nil || primary.Status != model.HypothesisStatusConfirmed {
		return ReportAlignmentResult{}
	}
	alignedFault := hypothesisFaultType(primary.HypothesisType)
	if alignedFault == "" || faultTypesEquivalent(report.FaultType, alignedFault) {
		return ReportAlignmentResult{}
	}

	// Deterministic analyzers own the observed fault classification. A
	// hypothesis may explain or refine that result, but must not replace it.
	if analyzerPrimaryAuthoritative(report) {
		var added, refined bool
		report.ContributingFactors, added = appendHypothesisFactor(report.ContributingFactors, *primary)
		refined = refineAuthoritativeConfigSummary(report, *primary)
		return ReportAlignmentResult{
			Changed:       added || refined,
			Reason:        fmt.Sprintf("kept analyzer primary %s; attached hypothesis %s as a contributing explanation", report.FaultType, primary.HypothesisType),
			OriginalFault: report.FaultType,
			AlignedFault:  report.FaultType,
			Primary:       primary.HypothesisType,
		}
	}

	original := report.FaultType
	originalPrimary := report.PrimaryRootCause
	newPrimary := diagnostic.RootCauseFactor{
		AnalyzerName:     "agent_hypothesis",
		FaultType:        alignedFault,
		Summary:          alignedSummary(alignedFault, *primary),
		ConfidenceScore:  primary.ConfidenceScore,
		EvidenceRefs:     jsonTextItems(primary.SupportingEvidenceRefs),
		ContributingRole: "primary",
	}

	report.FaultType = alignedFault
	report.RootCauseSummary = newPrimary.Summary
	report.ConfidenceScore = primary.ConfidenceScore
	report.PrimaryRootCause = &newPrimary
	report.ContributingFactors = alignedContributingFactors(report.ContributingFactors, originalPrimary, hypotheses, primary.HypothesisType)
	report.RiskLevel = alignedRiskLevel(report.RiskLevel, primary)

	return ReportAlignmentResult{
		Changed:       true,
		Reason:        fmt.Sprintf("confirmed hypothesis %s overrides analyzer primary %s", primary.HypothesisType, original),
		OriginalFault: original,
		AlignedFault:  alignedFault,
		Primary:       primary.HypothesisType,
	}
}

func refineAuthoritativeConfigSummary(report *diagnostic.Report, hypothesis model.Hypothesis) bool {
	if report == nil || hypothesis.HypothesisType != "missing_secret_or_configmap" ||
		hypothesis.Status != model.HypothesisStatusConfirmed {
		return false
	}
	cause := confirmedConfigReferenceCause(report.Evidences)
	if cause == "" || strings.Contains(strings.ToLower(report.RootCauseSummary), strings.ToLower(cause)) {
		return false
	}
	report.RootCauseSummary = strings.TrimSpace(report.RootCauseSummary) + " Confirmed configuration cause: " + cause + "."
	return true
}

func confirmedConfigReferenceCause(evidences []diagnostic.EvidenceRecord) string {
	for _, evidence := range evidences {
		if evidence.SourceType != "k8s_config_ref" {
			continue
		}
		if ref, ok := evidence.Raw.(podConfigReference); ok {
			switch ref.ContentStatus {
			case "object_missing":
				return fmt.Sprintf("%s %s is missing", ref.Kind, ref.Name)
			case "key_missing":
				return fmt.Sprintf("%s %s is missing key %s", ref.Kind, ref.Name, ref.Key)
			case "empty":
				return fmt.Sprintf("%s %s key %s is empty", ref.Kind, ref.Name, ref.Key)
			}
		}
		text := strings.ToLower(evidence.Content)
		switch {
		case strings.Contains(text, "contentstatus=object_missing"):
			return "a referenced ConfigMap or Secret is missing"
		case strings.Contains(text, "contentstatus=key_missing"):
			return "a referenced ConfigMap or Secret key is missing"
		case strings.Contains(text, "contentstatus=empty"):
			return "a referenced ConfigMap or Secret value is empty"
		}
	}
	return ""
}

func selectPrimaryHypothesis(hypotheses []model.Hypothesis) *model.Hypothesis {
	var best *model.Hypothesis
	for i := range hypotheses {
		h := &hypotheses[i]
		if h.Status == model.HypothesisStatusRejected {
			continue
		}
		if best == nil || betterPrimaryHypothesis(*h, *best) {
			best = h
		}
	}
	return best
}

func hypothesisFaultType(hypothesisType string) string {
	switch strings.ToLower(strings.TrimSpace(hypothesisType)) {
	case "memory_limit_too_low", "application_memory_leak":
		return "OOMKilled"
	case "bad_config", "missing_secret_or_configmap", "dependency_unavailable":
		return "CrashLoopBackOff"
	case "probe_misconfigured":
		return "ProbeFailed"
	case "pvc_unbound", "scheduling_constraint":
		return "PodPending"
	case "image_pull_failed":
		return "ImagePullBackOff"
	case "init_container_crash":
		return "InitContainerError"
	case "node_eviction":
		return "Evicted"
	case "node_not_ready":
		return "NodeNotReady"
	default:
		return ""
	}
}

func faultTypesEquivalent(actual, expected string) bool {
	expected = normalizeReportFault(expected)
	for _, part := range strings.Split(actual, ",") {
		if normalizeReportFault(part) == expected {
			return true
		}
	}
	return normalizeReportFault(actual) == expected
}

func normalizeReportFault(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
}

func alignedSummary(faultType string, hypothesis model.Hypothesis) string {
	summary := strings.TrimSpace(hypothesis.Summary)
	if summary == "" {
		return faultType + ": confirmed by agent hypothesis scoring."
	}
	if strings.HasPrefix(strings.ToLower(summary), strings.ToLower(faultType)) {
		return summary
	}
	return faultType + ": " + summary
}

func alignedContributingFactors(existing []diagnostic.RootCauseFactor, originalPrimary *diagnostic.RootCauseFactor, hypotheses []model.Hypothesis, primaryType string) []diagnostic.RootCauseFactor {
	result := append([]diagnostic.RootCauseFactor(nil), existing...)
	if originalPrimary != nil &&
		!strings.EqualFold(strings.TrimSpace(originalPrimary.FaultType), "unknown") &&
		!faultTypesEquivalent(originalPrimary.FaultType, hypothesisFaultType(primaryType)) {
		displaced := *originalPrimary
		displaced.ContributingRole = firstNonEmptyString(displaced.ContributingRole, "previous_assessment")
		if displaced.ContributingRole == "primary" {
			displaced.ContributingRole = "previous_assessment"
		}
		result = append(result, displaced)
	}
	for _, hypothesis := range hypotheses {
		if hypothesis.HypothesisType == primaryType || !isContributingFactor(hypothesis) {
			continue
		}
		faultType := hypothesisFaultType(hypothesis.HypothesisType)
		if faultType == "" || contributingHasHypothesis(result, hypothesis.HypothesisType, faultType) {
			continue
		}
		result = append(result, diagnostic.RootCauseFactor{
			AnalyzerName:     "agent_hypothesis",
			FaultType:        faultType,
			Summary:          alignedSummary(faultType, hypothesis),
			ConfidenceScore:  hypothesis.ConfidenceScore,
			EvidenceRefs:     jsonTextItems(hypothesis.SupportingEvidenceRefs),
			ContributingRole: "hypothesis_factor",
		})
		if len(result) >= 6 {
			break
		}
	}
	return result
}

func appendHypothesisFactor(existing []diagnostic.RootCauseFactor, hypothesis model.Hypothesis) ([]diagnostic.RootCauseFactor, bool) {
	faultType := hypothesisFaultType(hypothesis.HypothesisType)
	if faultType == "" || contributingHasHypothesis(existing, hypothesis.HypothesisType, faultType) {
		return existing, false
	}
	return append(existing, diagnostic.RootCauseFactor{
		AnalyzerName:     "agent_hypothesis",
		FaultType:        faultType,
		Summary:          alignedSummary(faultType, hypothesis),
		ConfidenceScore:  hypothesis.ConfidenceScore,
		EvidenceRefs:     jsonTextItems(hypothesis.SupportingEvidenceRefs),
		ContributingRole: "hypothesis_explanation",
	}), true
}

func analyzerPrimaryAuthoritative(report *diagnostic.Report) bool {
	if report == nil || strings.EqualFold(strings.TrimSpace(report.FaultType), "unknown") {
		return false
	}
	if report.PrimaryRootCause != nil {
		analyzerName := strings.TrimSpace(report.PrimaryRootCause.AnalyzerName)
		if analyzerName != "" && !strings.EqualFold(analyzerName, "agent_hypothesis") {
			return true
		}
	}
	return analyzerHasStructuralEvidence(report)
}

func analyzerHasStructuralEvidence(report *diagnostic.Report) bool {
	if report == nil {
		return false
	}
	faultType := report.FaultType
	if report.PrimaryRootCause != nil && strings.TrimSpace(report.PrimaryRootCause.FaultType) != "" {
		faultType = report.PrimaryRootCause.FaultType
	}
	for _, evidence := range report.Evidences {
		source := strings.ToLower(strings.TrimSpace(evidence.SourceType))
		text := strings.ToLower(evidence.Title + " " + evidence.Content)
		switch normalizeReportFault(faultType) {
		case "oomkilled":
			if source == "k8s_pod_status" && oomFailureSignal(text) {
				return true
			}
		case "crashloopbackoff":
			if (source == "k8s_pod_status" || source == "k8s_event") &&
				(strings.Contains(text, "crashloopbackoff") || strings.Contains(text, "back-off restarting")) {
				return true
			}
		case "imagepullbackoff":
			if (source == "k8s_pod_status" || source == "k8s_event") &&
				(strings.Contains(text, "imagepullbackoff") || strings.Contains(text, "errimagepull")) {
				return true
			}
		case "initcontainererror", "initerror":
			if source == "k8s_pod_status" &&
				(strings.Contains(text, "init container") || strings.Contains(text, "initcontainer")) &&
				(strings.Contains(text, "exitcode=") || strings.Contains(text, "waitingreason=") || strings.Contains(text, "crashloopbackoff")) {
				return true
			}
		case "evicted":
			if source == "k8s_pod_status" &&
				(strings.Contains(text, "reason=evicted") || strings.Contains(text, "pod eviction status")) {
				return true
			}
		case "nodenotready":
			if (source == "k8s_topology" || source == "k8s_event") &&
				(strings.Contains(text, "nodeready=false") || strings.Contains(text, "node notready")) {
				return true
			}
		case "podpending":
			if (source == "k8s_event" || source == "k8s_pvc" || source == "k8s_pod_status") &&
				(strings.Contains(text, "failedscheduling") || strings.Contains(text, "unbound") || strings.Contains(text, "phase=pending")) {
				return true
			}
		case "probefailed":
			if (source == "k8s_event" || source == "k8s_pod_status") &&
				strings.Contains(text, "probe") &&
				(strings.Contains(text, "unhealthy") || strings.Contains(text, "failed")) {
				return true
			}
		}
	}
	return false
}

func contributingHasHypothesis(factors []diagnostic.RootCauseFactor, hypothesisType, faultType string) bool {
	for _, factor := range factors {
		if strings.EqualFold(factor.AnalyzerName, "agent_hypothesis") && strings.Contains(strings.ToLower(factor.Summary), strings.ToLower(hypothesisType)) {
			return true
		}
		if faultTypesEquivalent(factor.FaultType, faultType) {
			return true
		}
	}
	return false
}

func alignedRiskLevel(current string, primary *model.Hypothesis) string {
	if primary == nil || primary.ConfidenceScore < convergenceMinConfidence {
		return current
	}
	return firstNonEmptyString(current, "medium")
}
