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

// AlignReportWithHypotheses lets the Agent's confirmed primary hypothesis
// reconcile the rule-analyzer report before remediation and persistence.
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
	if originalPrimary != nil && !faultTypesEquivalent(originalPrimary.FaultType, hypothesisFaultType(primaryType)) {
		displaced := *originalPrimary
		displaced.ContributingRole = firstNonEmptyString(displaced.ContributingRole, "analyzer_primary")
		if displaced.ContributingRole == "primary" {
			displaced.ContributingRole = "analyzer_primary"
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
