package llm

import "testing"

func TestEvidenceValidatorPassesFullyGroundedSummary(t *testing.T) {
	validator := NewEvidenceValidator(testGroundingEvidence(), GroundingOptions{})
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{
			AgreementLevel: "full_agree",
			Summary:        "OOMKilled exit code 137 memory limit",
			EvidenceRefs:   []string{"ev-0"},
		},
		EvidenceChain: []GroundedClaim{{
			Claim:        "OOMKilled exit code 137 memory limit",
			EvidenceRefs: []string{"ev-0"},
		}},
	}
	result := validator.Validate(summary)
	if result.Decision != GroundingDecisionPassed {
		t.Fatalf("expected pass, got %#v", result)
	}
	if result.HallucinationRisk > 0.2 {
		t.Fatalf("expected low risk, got %#v", result)
	}
	if result.Summary == nil || !result.Summary.EvidenceChain[0].Verified {
		t.Fatalf("expected verified claim: %#v", result.Summary)
	}
}

func TestEvidenceValidatorDropsInvalidReferences(t *testing.T) {
	validator := NewEvidenceValidator(testGroundingEvidence(), GroundingOptions{})
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "full_agree", Summary: "OOMKilled exit code 137 memory limit", EvidenceRefs: []string{"ev-0"}},
		EvidenceChain: []GroundedClaim{
			{Claim: "valid memory claim", EvidenceRefs: []string{"ev-0"}},
			{Claim: "invalid claim", EvidenceRefs: []string{"ev-does-not-exist"}},
		},
	}
	result := validator.Validate(summary)
	if len(result.Summary.EvidenceChain) != 1 {
		t.Fatalf("expected invalid ref claim to be dropped: %#v", result.Summary.EvidenceChain)
	}
	if len(result.InvalidReferences) != 1 || result.InvalidReferences[0] != "ev-does-not-exist" {
		t.Fatalf("expected invalid ref to be reported: %#v", result.InvalidReferences)
	}
}

func TestEvidenceValidatorRejectsHallucinatedClaim(t *testing.T) {
	validator := NewEvidenceValidator(testGroundingEvidence(), GroundingOptions{})
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "partial_agree", Summary: "database outage caused pod crash", EvidenceRefs: []string{"ev-0"}},
		EvidenceChain: []GroundedClaim{{
			Claim:        "database outage caused the pod crash",
			EvidenceRefs: []string{"ev-0"},
		}},
	}
	result := validator.Validate(summary)
	if result.Decision != GroundingDecisionRejected {
		t.Fatalf("expected rejected hallucination, got %#v", result)
	}
	if result.UngroundedClaims == 0 {
		t.Fatalf("expected ungrounded claims: %#v", result)
	}
}

func TestEvidenceValidatorDisagreeBoundaryDegrades(t *testing.T) {
	text := "OOMKilled exit code 137 memory limit"
	validator := NewEvidenceValidator([]GroundingEvidence{{ID: "ev-0", Title: text, Content: text}}, GroundingOptions{})
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "disagree", Summary: text, EvidenceRefs: []string{"ev-0"}},
		EvidenceChain: []GroundedClaim{{
			Claim:        text,
			EvidenceRefs: []string{"ev-0"},
		}},
	}
	result := validator.Validate(summary)
	if result.Decision != GroundingDecisionDegraded {
		t.Fatalf("expected disagree boundary to degrade, got %#v", result)
	}
	if result.HallucinationRisk != 0.4 {
		t.Fatalf("expected risk 0.4, got %#v", result)
	}
}

func TestGroundedSummaryRejectedMeansRuleOnlyFallback(t *testing.T) {
	rule := RuleBasedResult{
		RootCauseSummary: "Rule RCA: OOMKilled due to memory limit",
		ConfidenceScore:  0.9,
		SuggestedActions: []string{"Review memory limit"},
		RiskLevel:        "high",
		NeedHumanConfirm: true,
	}
	validator := NewEvidenceValidator(testGroundingEvidence(), GroundingOptions{})
	result := validator.Validate(&GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "partial_agree", Summary: "unrelated database outage", EvidenceRefs: []string{"ev-0"}},
		EvidenceChain:         []GroundedClaim{{Claim: "unrelated database outage", EvidenceRefs: []string{"ev-0"}}},
	})
	if result.Decision != GroundingDecisionRejected {
		t.Fatalf("expected rejected result")
	}
	enhanced := (&GroundedSummary{}).ToEnhancedSummary(rule)
	if enhanced.RootCauseSummary != rule.RootCauseSummary || enhanced.ConfidenceScore != rule.ConfidenceScore {
		t.Fatalf("fallback must preserve rule result: %#v", enhanced)
	}
}

func testGroundingEvidence() []GroundingEvidence {
	return []GroundingEvidence{{
		ID:         "ev-0",
		SourceType: "k8s_pod",
		Title:      "OOMKilled exit code 137 memory limit",
		Content:    "OOMKilled exit code 137 memory limit working set",
		Severity:   "critical",
	}}
}
