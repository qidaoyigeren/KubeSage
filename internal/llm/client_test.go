package llm

import "testing"

// TestParseEnhancedSummary verifies model JSON can be parsed into the required
// structured schema.
func TestParseEnhancedSummary(t *testing.T) {
	summary, err := parseEnhancedSummary(`{
		"root_cause_summary": "OOM happened because memory stayed near limit.",
		"confidence_score": 0.87,
		"evidence_reasoning": "Pod status and Prometheus agree.",
		"suggested_actions": ["Inspect heap profile"],
		"risk_level": "high",
		"need_human_confirm": true
	}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if summary.RootCauseSummary == "" || summary.ConfidenceScore != 0.87 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestParseGroundedSummary(t *testing.T) {
	summary, err := parseGroundedSummary(`{
		"root_cause_confirmation": {
			"agreement_level": "full_agree",
			"summary": "Rule diagnosis is supported.",
			"evidence_refs": ["ev-0"]
		},
		"evidence_chain": [
			{"claim": "OOMKilled exit code 137", "evidence_refs": ["ev-0"]}
		],
		"additional_observations": []
	}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if summary.RootCauseConfirmation.AgreementLevel != "full_agree" || len(summary.EvidenceChain) != 1 {
		t.Fatalf("unexpected grounded summary: %#v", summary)
	}
}

// TestSanitizeEnhancedSummaryRemovesDangerousActions verifies command-like
// destructive suggestions are not surfaced.
func TestSanitizeEnhancedSummaryRemovesDangerousActions(t *testing.T) {
	summary := &EnhancedSummary{
		RootCauseSummary: "CrashLoop due to bad config.",
		ConfidenceScore:  2,
		SuggestedActions: []string{
			"kubectl delete pod api-1",
			"kubectl delete pvc data -n default",
			"DROP DATABASE prod",
			"Review the ConfigMap change with an owner.",
		},
		RiskLevel: "unknown",
	}
	sanitizeEnhancedSummary(summary)
	if summary.ConfidenceScore != 1 {
		t.Fatalf("expected confidence clamp, got %f", summary.ConfidenceScore)
	}
	if len(summary.SuggestedActions) != 1 || summary.SuggestedActions[0] != "Review the ConfigMap change with an owner." {
		t.Fatalf("unexpected actions: %#v", summary.SuggestedActions)
	}
	if summary.RiskLevel != "medium" || !summary.NeedHumanConfirm {
		t.Fatalf("unexpected safety fields: %#v", summary)
	}
}
