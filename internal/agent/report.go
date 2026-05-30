package agent

import (
	"fmt"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

// BuildReportSnapshot converts runtime state into the structured Agent report
// sections stored on diagnosis_reports.agent_report_snapshot.
func BuildReportSnapshot(report *diagnostic.Report, hypotheses []model.Hypothesis, hits []RunbookHit, executions []model.RemediationExecution, verification []VerificationPlan, stopReason string) ReportSnapshot {
	summary := executionSummary(report, hypotheses, stopReason)
	return ReportSnapshot{
		RuleBasedResult:       reportValue(report, func(r *diagnostic.Report) interface{} { return r.RuleBasedResult }),
		AgentExecutionSummary: summary,
		Hypotheses:            hypotheses,
		EvidenceChain:         evidenceChain(report),
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
	return fmt.Sprintf("Agent completed with stop_reason=%s fault_type=%s rule_confidence=%.2f confirmed=%s active=%s", stopReason, faultType, confidence, strings.Join(confirmed, ","), strings.Join(active, ","))
}

func evidenceChain(report *diagnostic.Report) []EvidenceRef {
	if report == nil {
		return nil
	}
	result := make([]EvidenceRef, 0, len(report.Evidences))
	for i, record := range report.Evidences {
		result = append(result, EvidenceRef{
			Ref:        evidenceRef(record, i),
			SourceType: record.SourceType,
			Title:      record.Title,
			Severity:   record.Severity,
		})
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
