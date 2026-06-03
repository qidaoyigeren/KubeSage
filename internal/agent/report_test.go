package agent

import (
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestBuildReportSnapshotUsesStableEvidenceRefs(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:       "OOMKilled",
		ConfidenceScore: 0.82,
		Evidences: []diagnostic.EvidenceRecord{
			{SourceType: "k8s_pod_status", Title: "OOM termination evidence", Content: "reason=OOMKilled", Severity: "critical", Timestamp: time.Now()},
			{SourceType: "prometheus", Title: "Prometheus unavailable", Content: "base_url is not configured", Severity: "warning", Timestamp: time.Now()},
			{SourceType: "runbook", Title: "OOMKilled Runbook", Content: "memory checks", Severity: "info", Timestamp: time.Now()},
		},
	}
	snapshot := BuildReportSnapshot(report, nil, nil, nil, nil, StopReasonPlanComplete)
	if len(snapshot.EvidenceChain) != 3 {
		t.Fatalf("expected 3 evidence refs, got %d", len(snapshot.EvidenceChain))
	}
	if snapshot.EvidenceChain[0].Ref != "E1" || snapshot.EvidenceChain[1].Ref != "E2" {
		t.Fatalf("unexpected refs: %#v", snapshot.EvidenceChain)
	}
	if len(snapshot.RootCauseEvidenceRefs) == 0 || snapshot.RootCauseEvidenceRefs[0] != "E1" {
		t.Fatalf("expected root cause evidence refs, got %#v", snapshot.RootCauseEvidenceRefs)
	}
}

func TestBuildReportSnapshotIncludesConfidenceBreakdownAndMissingEvidence(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:       "OOMKilled",
		ConfidenceScore: 0.72,
		Evidences: []diagnostic.EvidenceRecord{
			{SourceType: "k8s_pod_status", Title: "OOM termination evidence", Content: "reason=OOMKilled", Severity: "critical", Timestamp: time.Now()},
		},
	}
	hypotheses := []model.Hypothesis{{
		HypothesisType:  "memory_limit_too_low",
		MissingEvidence: model.JSONText(`["memory metrics"]`),
	}}
	snapshot := BuildReportSnapshot(report, hypotheses, nil, nil, nil, StopReasonPlanComplete)
	if len(snapshot.ConfidenceBreakdown) == 0 {
		t.Fatal("expected confidence breakdown")
	}
	if !containsString(snapshot.MissingEvidence, "metrics") && !containsString(snapshot.MissingEvidence, "memory metrics") {
		t.Fatalf("expected metrics missing evidence, got %#v", snapshot.MissingEvidence)
	}
}

func TestBuildReportSnapshotIncludesPrimaryAndContributingFactors(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:       "NodeNotReady",
		ConfidenceScore: 0.82,
		Evidences: []diagnostic.EvidenceRecord{
			{SourceType: "k8s_event", Title: "Node pressure events", Content: "node notready and eviction", Severity: "warning", Timestamp: time.Now()},
		},
	}
	hypotheses := []model.Hypothesis{
		{
			HypothesisType:         "node_not_ready",
			Summary:                "Node is NotReady.",
			ConfidenceScore:        0.86,
			Status:                 model.HypothesisStatusConfirmed,
			SupportingEvidenceRefs: model.JSONText(`["E1"]`),
		},
		{
			HypothesisType:         "node_eviction",
			Summary:                "Eviction may contribute.",
			ConfidenceScore:        0.76,
			Status:                 model.HypothesisStatusConfirmed,
			SupportingEvidenceRefs: model.JSONText(`["E1"]`),
			MissingEvidence:        model.JSONText(`["node pressure metrics"]`),
		},
	}
	snapshot := BuildReportSnapshot(report, hypotheses, nil, nil, nil, StopReasonConfirmedHypothesis)
	if snapshot.PrimaryRootCause == nil || snapshot.PrimaryRootCause.HypothesisType != "node_not_ready" {
		t.Fatalf("unexpected primary root cause: %#v", snapshot.PrimaryRootCause)
	}
	if len(snapshot.ContributingFactors) != 1 || snapshot.ContributingFactors[0].HypothesisType != "node_eviction" {
		t.Fatalf("unexpected contributing factors: %#v", snapshot.ContributingFactors)
	}
	if !containsString(snapshot.ContributingFactors[0].MissingEvidence, "node pressure metrics") {
		t.Fatalf("expected contributing factor missing evidence, got %#v", snapshot.ContributingFactors[0].MissingEvidence)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
