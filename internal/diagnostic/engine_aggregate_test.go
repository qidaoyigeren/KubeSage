package diagnostic

import (
	"strings"
	"testing"
)

func TestAggregatePrefersStrongOOMTerminationOverPending(t *testing.T) {
	report := aggregate(&DiagnosticContext{Namespace: "eval", PodName: "oom"}, []*AnalyzeResult{
		{
			FaultType:        "PodPending",
			RootCauseSummary: "pending summary",
			ConfidenceScore:  0.95,
		},
		{
			FaultType:        "OOMKilled",
			RootCauseSummary: "oom summary",
			ConfidenceScore:  0.82,
			Evidences: []EvidenceRecord{{
				SourceType: "k8s_pod_status",
				Title:      "OOM termination evidence",
				Content:    "container=app restartCount=4 reason=OOMKilled exitCode=137",
			}},
		},
	})

	if !strings.Contains(report.RootCauseSummary, "Primary root cause: oom summary") {
		t.Fatalf("expected OOM primary summary, got %q", report.RootCauseSummary)
	}
	// Confidence is adjusted down because a high-confidence competing factor exists.
	if report.ConfidenceScore >= 0.82 || report.ConfidenceScore < 0.70 {
		t.Fatalf("expected adjusted OOM confidence in [0.70, 0.82), got %v", report.ConfidenceScore)
	}
	if report.PrimaryRootCause == nil || report.PrimaryRootCause.FaultType != "OOMKilled" {
		t.Fatalf("expected OOM primary root cause, got %#v", report.PrimaryRootCause)
	}
	if len(report.ContributingFactors) != 1 || report.ContributingFactors[0].FaultType != "PodPending" {
		t.Fatalf("expected pending contributing factor, got %#v", report.ContributingFactors)
	}
	// Pending has high confidence close to OOM, so it should be labeled co-causal.
	if report.ContributingFactors[0].ContributingRole != "co-causal" {
		t.Fatalf("expected co-causal role for high-confidence pending, got %q", report.ContributingFactors[0].ContributingRole)
	}
}

func TestAggregateDoesNotPromoteEventOnlyOOMOverPending(t *testing.T) {
	report := aggregate(&DiagnosticContext{Namespace: "eval", PodName: "early"}, []*AnalyzeResult{
		{
			FaultType:        "OOMKilled",
			RootCauseSummary: "event-only oom summary",
			ConfidenceScore:  0.72,
			Evidences: []EvidenceRecord{{
				SourceType: "k8s_event",
				Title:      "OOMKilled",
				Content:    "Container app was OOMKilled after exceeding memory limit",
			}},
		},
		{
			FaultType:        "PodPending",
			RootCauseSummary: "pending summary",
			ConfidenceScore:  0.73,
		},
	})

	if !strings.Contains(report.RootCauseSummary, "Primary root cause: pending summary") {
		t.Fatalf("expected pending as primary for event-only OOM, got %q", report.RootCauseSummary)
	}
	if report.PrimaryRootCause == nil || report.PrimaryRootCause.FaultType != "PodPending" {
		t.Fatalf("expected pending primary root cause, got %#v", report.PrimaryRootCause)
	}
	if len(report.ContributingFactors) != 1 || report.ContributingFactors[0].FaultType != "OOMKilled" {
		t.Fatalf("expected OOM contributing factor, got %#v", report.ContributingFactors)
	}
	// OOM has high confidence (0.72) close to Pending (0.73), labeled co-causal.
	if report.ContributingFactors[0].ContributingRole != "co-causal" {
		t.Fatalf("expected co-causal role for high-confidence OOM close to primary, got %q", report.ContributingFactors[0].ContributingRole)
	}
}

func TestAggregateConfidenceTiebreakerUsesWeightedScore(t *testing.T) {
	// Two results with identical confidence — the one with stronger evidence
	// and higher fault severity should win via weighted tiebreaker.
	report := aggregate(&DiagnosticContext{Namespace: "eval", PodName: "tie"}, []*AnalyzeResult{
		{
			FaultType:        "PodPending",
			RootCauseSummary: "pending summary",
			ConfidenceScore:  0.80,
		},
		{
			FaultType:        "CrashLoopBackOff",
			RootCauseSummary: "crashloop summary",
			ConfidenceScore:  0.80,
			Evidences: []EvidenceRecord{
				{SourceType: "k8s_log", Title: "panic", Content: "app panic", Severity: "warning"},
				{SourceType: "k8s_event", Title: "BackOff", Content: "back-off restarting", Severity: "warning"},
			},
		},
	})

	if !strings.Contains(report.RootCauseSummary, "Primary root cause: crashloop summary") {
		t.Fatalf("expected CrashLoop to win tiebreaker, got %q", report.RootCauseSummary)
	}
	if report.PrimaryRootCause == nil || report.PrimaryRootCause.FaultType != "CrashLoopBackOff" {
		t.Fatalf("expected CrashLoop primary, got %#v", report.PrimaryRootCause)
	}
}

func TestAggregateCoCausalEscalatesRisk(t *testing.T) {
	report := aggregate(&DiagnosticContext{Namespace: "eval", PodName: "risk"}, []*AnalyzeResult{
		{
			FaultType:        "OOMKilled",
			RootCauseSummary: "oom summary",
			ConfidenceScore:  0.80,
			RiskLevel:        "medium",
			Evidences: []EvidenceRecord{{
				SourceType: "k8s_pod_status",
				Title:      "OOM termination evidence",
				Content:    "reason=OOMKilled exitCode=137",
			}},
		},
		{
			FaultType:        "CrashLoopBackOff",
			RootCauseSummary: "crashloop summary",
			ConfidenceScore:  0.78,
			RiskLevel:        "medium",
		},
	})

	// Co-causal factor should escalate risk from medium to high.
	if report.RiskLevel != "high" {
		t.Fatalf("expected risk escalation to high with co-causal factor, got %q", report.RiskLevel)
	}
	// Confidence should be adjusted down.
	if report.ConfidenceScore >= 0.80 {
		t.Fatalf("expected adjusted confidence < 0.80, got %v", report.ConfidenceScore)
	}
}

func TestAggregateSupportingRoleDoesNotEscalateRisk(t *testing.T) {
	report := aggregate(&DiagnosticContext{Namespace: "eval", PodName: "low"}, []*AnalyzeResult{
		{
			FaultType:        "OOMKilled",
			RootCauseSummary: "oom summary",
			ConfidenceScore:  0.90,
			RiskLevel:        "high",
			Evidences: []EvidenceRecord{{
				SourceType: "k8s_pod_status",
				Title:      "OOM termination evidence",
				Content:    "reason=OOMKilled exitCode=137",
			}},
		},
		{
			FaultType:        "PodPending",
			RootCauseSummary: "pending summary",
			ConfidenceScore:  0.40,
			RiskLevel:        "low",
		},
	})

	// Low-confidence supporting factor should not escalate risk.
	if report.RiskLevel != "high" {
		t.Fatalf("expected risk to stay high (from primary), got %q", report.RiskLevel)
	}
	// Confidence should be preserved (no co-causal/competing adjustment).
	if report.ConfidenceScore != 0.90 {
		t.Fatalf("expected confidence preserved at 0.90, got %v", report.ConfidenceScore)
	}
}

func TestClassifyContributingRole(t *testing.T) {
	primary := &AnalyzeResult{ConfidenceScore: 0.85}
	tests := []struct {
		name     string
		score    float64
		expected string
	}{
		{"co-causal high close", 0.78, "co-causal"},
		{"competing high far", 0.70, "competing"},
		{"secondary moderate", 0.55, "secondary"},
		{"supporting low", 0.30, "supporting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &AnalyzeResult{ConfidenceScore: tt.score}
			role := classifyContributingRole(result, primary)
			if role != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, role)
			}
		})
	}
}
