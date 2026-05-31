package llm

import (
	"strings"
	"testing"

	"kubesage/internal/diagnostic"
	"kubesage/internal/rag"
)

// TestBuildPromptContainsRequiredSections verifies the LLM prompt includes the
// context blocks required for RAG-enhanced RCA.
func TestBuildPromptContainsRequiredSections(t *testing.T) {
	rule := RuleBasedResult{
		FaultType:        "OOMKilled",
		RootCauseSummary: "Rule summary",
		ConfidenceScore:  0.9,
		RiskLevel:        "high",
		NeedHumanConfirm: true,
	}
	prompt := BuildPrompt(&diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-1",
	}, rule, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_key_log", Title: "Key logs", Content: "oom killed", Severity: "critical"},
		{SourceType: "prometheus", Title: "Memory", Content: "near limit", Severity: "critical"},
	}, []rag.Hit{{Title: "oom runbook", Content: "check memory limit", Score: 10}})

	for _, required := range []string{
		"pod_basic_info",
		"fault_type",
		"evidence_list",
		"events_summary",
		"key_log_fragments",
		"prometheus_metric_summary",
		"runbook_retrieval_results",
		"root_cause_summary",
	} {
		if !strings.Contains(prompt.User, required) {
			t.Fatalf("prompt missing %s: %s", required, prompt.User)
		}
	}
}

func TestBuildGroundedPromptAnchorsEvidenceIDs(t *testing.T) {
	rule := RuleBasedResult{
		FaultType:        "CrashLoopBackOff",
		RootCauseSummary: "Rule summary",
		ConfidenceScore:  0.8,
	}
	prompt := BuildGroundedPrompt(&diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-1",
	}, rule, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_event", Title: "BackOff", Content: "Back-off restarting failed container", Severity: "warning"},
	}, nil)

	for _, required := range []string{"rule_diagnosis", "evidence_id", "ev-0", "root_cause_confirmation", "evidence_chain", "additional_observations"} {
		if !strings.Contains(prompt.User, required) {
			t.Fatalf("grounded prompt missing %s: %s", required, prompt.User)
		}
	}
	if len(prompt.Evidences) != 1 || prompt.Evidences[0].ID != "ev-0" {
		t.Fatalf("unexpected grounding evidence: %#v", prompt.Evidences)
	}
}
