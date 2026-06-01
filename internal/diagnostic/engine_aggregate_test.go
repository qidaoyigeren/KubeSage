package diagnostic

import "testing"

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

	if report.RootCauseSummary != "oom summary" {
		t.Fatalf("expected OOM summary to win, got %q", report.RootCauseSummary)
	}
	if report.ConfidenceScore != 0.82 {
		t.Fatalf("expected OOM confidence to be preserved, got %v", report.ConfidenceScore)
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

	if report.RootCauseSummary != "pending summary" {
		t.Fatalf("expected confidence order for event-only OOM, got %q", report.RootCauseSummary)
	}
}
