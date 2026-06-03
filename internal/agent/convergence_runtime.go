package agent

import "fmt"

func shouldConfirmWithExhaustedEvidence(plan *Plan, state *ToolState, decision ConvergenceDecision, threshold float64) (bool, string) {
	if decision.Converged {
		if decision.Top == nil {
			return true, "convergence decision already confirmed"
		}
		if hasRunnableDistinguishingEvidence(plan, state, decision) {
			return false, ""
		}
		return true, fmt.Sprintf("top hypothesis %s is separated from alternatives with complete key evidence", decision.Top.Type)
	}
	if decision.Top == nil {
		return false, ""
	}
	top := *decision.Top
	if top.Confidence < threshold || len(top.SupportingRefs) == 0 {
		return false, ""
	}
	if decision.Ambiguous || (decision.MultipleConfirmed && decision.Top1Top2Margin < convergenceMinTopGap) {
		return true, fmt.Sprintf("top hypothesis %s confidence %.2f is confirmed; competing hypotheses remain but distinguishing evidence is exhausted", top.Type, top.Confidence)
	}
	if hasRunnableDistinguishingEvidence(plan, state, decision) {
		return false, ""
	}
	return true, fmt.Sprintf("top hypothesis %s confidence %.2f is confirmed; remaining evidence gaps were already attempted or unavailable", top.Type, top.Confidence)
}

func hasRunnableDistinguishingEvidence(plan *Plan, state *ToolState, decision ConvergenceDecision) bool {
	gaps := convergenceEvidenceGaps(decision)
	for _, gap := range gaps {
		for _, tool := range toolsForEvidenceGap(gap) {
			if evidenceToolCanStillRun(plan, state, tool) {
				return true
			}
		}
	}
	return false
}

func ensureRunnableConvergenceEvidence(plan *Plan, state *ToolState, decision ConvergenceDecision, reason string) bool {
	if !hasRunnableDistinguishingEvidence(plan, state, decision) {
		return false
	}
	for _, gap := range convergenceEvidenceGaps(decision) {
		appendEvidenceCollectionSteps(plan, gap, reason, state)
	}
	return hasRunnableDistinguishingEvidence(plan, state, decision)
}

func convergenceEvidenceGaps(decision ConvergenceDecision) []string {
	gaps := append([]string(nil), decision.DistinguishingEvidence...)
	if len(gaps) == 0 && decision.Top != nil {
		gaps = append(gaps, decision.Top.MissingEvidence...)
	}
	if len(gaps) == 0 && decision.Top != nil {
		gaps = append(gaps, defaultDistinguishingEvidence(decision.Top.Type)...)
	}
	return appendUniqueStrings(gaps)
}

func evidenceToolCanStillRun(plan *Plan, state *ToolState, tool string) bool {
	if !toolAvailableInState(state, tool) {
		return false
	}
	if planHasPendingTool(plan, tool) {
		return true
	}
	if toolAttempted(state, tool) {
		return false
	}
	if planHasAnyTool(plan, tool) {
		return false
	}
	return true
}

func planHasAnyTool(plan *Plan, tool string) bool {
	if plan == nil {
		return false
	}
	canonical := canonicalToolName(tool)
	for _, step := range plan.Steps {
		if canonicalToolName(step.ToolName) == canonical {
			return true
		}
	}
	return false
}
