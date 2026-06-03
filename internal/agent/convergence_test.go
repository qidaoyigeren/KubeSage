package agent

import "testing"

func TestAssessHypothesisConvergenceRequiresTopGap(t *testing.T) {
	decision := assessHypothesisConvergence([]HypothesisScore{
		{Type: "node_eviction", Confidence: 0.82, SupportingRefs: []string{"E1"}},
		{Type: "node_not_ready", Confidence: 0.78, SupportingRefs: []string{"E2"}},
	})
	if decision.Converged {
		t.Fatalf("expected close high-confidence hypotheses to remain unconverged")
	}
	if !decision.Ambiguous {
		t.Fatalf("expected ambiguity flag, got %#v", decision)
	}
	if len(decision.DistinguishingEvidence) == 0 {
		t.Fatalf("expected distinguishing evidence suggestions")
	}
}

func TestAssessHypothesisConvergenceRequiresKeyEvidence(t *testing.T) {
	decision := assessHypothesisConvergence([]HypothesisScore{
		{Type: "memory_limit_too_low", Confidence: 0.86, SupportingRefs: []string{"E1"}, MissingEvidence: []string{"memory metrics"}},
		{Type: "application_memory_leak", Confidence: 0.42, SupportingRefs: []string{"E2"}},
	})
	if decision.Converged {
		t.Fatalf("expected missing key evidence to block convergence")
	}
	if decision.KeyEvidenceComplete {
		t.Fatalf("expected incomplete key evidence")
	}
}

func TestAssessHypothesisConvergenceAcceptsSeparatedCompleteTop(t *testing.T) {
	decision := assessHypothesisConvergence([]HypothesisScore{
		{Type: "memory_limit_too_low", Confidence: 0.86, SupportingRefs: []string{"E1"}},
		{Type: "application_memory_leak", Confidence: 0.52, SupportingRefs: []string{"E2"}},
	})
	if !decision.Converged {
		t.Fatalf("expected separated complete top hypothesis to converge: %#v", decision)
	}
}
