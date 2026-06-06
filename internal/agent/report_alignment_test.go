package agent

import (
	"strings"
	"testing"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestAlignReportWithHypothesesPreservesAnalyzerPrimary(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "CrashLoopBackOff",
		RootCauseSummary: "CrashLoopBackOff restart evidence",
		ConfidenceScore:  0.88,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			AnalyzerName:     "crashloopbackoff",
			FaultType:        "CrashLoopBackOff",
			Summary:          "CrashLoopBackOff restart evidence",
			ConfidenceScore:  0.88,
			ContributingRole: "primary",
		},
	}
	hypotheses := []model.Hypothesis{
		{
			HypothesisType:         "probe_misconfigured",
			Summary:                "Readiness probe path does not match the application.",
			ConfidenceScore:        0.75,
			Status:                 model.HypothesisStatusConfirmed,
			SupportingEvidenceRefs: model.JSONText(`["k8s_event:Unhealthy"]`),
		},
		{
			HypothesisType:  "bad_config",
			Summary:         "Application startup may fail because of invalid configuration.",
			ConfidenceScore: 0.55,
			Status:          model.HypothesisStatusActive,
		},
	}

	result := AlignReportWithHypotheses(report, hypotheses)
	if !result.Changed {
		t.Fatalf("expected hypothesis explanation to be attached")
	}
	if report.FaultType != "CrashLoopBackOff" {
		t.Fatalf("expected analyzer fault type to remain primary, got %s", report.FaultType)
	}
	if report.PrimaryRootCause == nil || report.PrimaryRootCause.FaultType != "CrashLoopBackOff" {
		t.Fatalf("unexpected primary root cause: %#v", report.PrimaryRootCause)
	}
	if report.RootCauseSummary != "CrashLoopBackOff restart evidence" {
		t.Fatalf("expected analyzer summary to remain authoritative, got %q", report.RootCauseSummary)
	}
	if len(report.ContributingFactors) == 0 || report.ContributingFactors[0].FaultType != "ProbeFailed" {
		t.Fatalf("expected hypothesis as contributing explanation, got %#v", report.ContributingFactors)
	}
	if report.ContributingFactors[0].ContributingRole != "hypothesis_explanation" {
		t.Fatalf("unexpected hypothesis role: %q", report.ContributingFactors[0].ContributingRole)
	}
	if !strings.Contains(result.Reason, "kept analyzer primary") {
		t.Fatalf("unexpected alignment reason: %q", result.Reason)
	}
}

func TestAlignReportWithHypothesesCanReplaceUnknownFallback(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "unknown",
		RootCauseSummary: "No analyzer matched.",
		ConfidenceScore:  0.30,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			FaultType:        "unknown",
			Summary:          "No analyzer matched.",
			ConfidenceScore:  0.30,
			ContributingRole: "primary",
		},
	}
	hypotheses := []model.Hypothesis{{
		HypothesisType:         "probe_misconfigured",
		Summary:                "Readiness probe path does not match the application.",
		ConfidenceScore:        0.80,
		Status:                 model.HypothesisStatusConfirmed,
		SupportingEvidenceRefs: model.JSONText(`["k8s_event:Unhealthy"]`),
	}}

	result := AlignReportWithHypotheses(report, hypotheses)
	if !result.Changed {
		t.Fatal("expected unknown fallback to be replaced")
	}
	if report.FaultType != "ProbeFailed" || report.PrimaryRootCause == nil || report.PrimaryRootCause.AnalyzerName != "agent_hypothesis" {
		t.Fatalf("unexpected aligned report: %#v", report)
	}
}

func TestAlignReportWithHypothesesPreservesStructuralAnalyzerEvidenceWithoutName(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "OOMKilled",
		RootCauseSummary: "Container terminated with OOMKilled.",
		ConfidenceScore:  0.90,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			FaultType:        "OOMKilled",
			Summary:          "Container terminated with OOMKilled.",
			ConfidenceScore:  0.90,
			ContributingRole: "primary",
		},
		Evidences: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "OOM termination evidence",
			Content:    "reason=OOMKilled exitCode=137",
		}},
	}
	hypotheses := []model.Hypothesis{{
		HypothesisType:         "probe_misconfigured",
		Summary:                "Probe failed.",
		ConfidenceScore:        0.80,
		Status:                 model.HypothesisStatusConfirmed,
		SupportingEvidenceRefs: model.JSONText(`["k8s_event:Unhealthy"]`),
	}}

	AlignReportWithHypotheses(report, hypotheses)
	if report.FaultType != "OOMKilled" || report.PrimaryRootCause.FaultType != "OOMKilled" {
		t.Fatalf("structural OOM evidence must remain primary: %#v", report)
	}
}

func TestAlignReportWithHypothesesLeavesEquivalentFaultAlone(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "ImagePullBackOff",
		RootCauseSummary: "image pull failed",
		ConfidenceScore:  0.82,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			AnalyzerName:     "imagepullbackoff",
			FaultType:        "ImagePullBackOff",
			Summary:          "image pull failed",
			ConfidenceScore:  0.82,
			ContributingRole: "primary",
		},
	}
	hypotheses := []model.Hypothesis{{
		HypothesisType:  "image_pull_failed",
		Summary:         "Container image cannot be pulled.",
		ConfidenceScore: 0.75,
		Status:          model.HypothesisStatusConfirmed,
	}}
	result := AlignReportWithHypotheses(report, hypotheses)
	if result.Changed {
		t.Fatalf("did not expect equivalent report to change: %#v", result)
	}
	if report.FaultType != "ImagePullBackOff" {
		t.Fatalf("unexpected fault type: %s", report.FaultType)
	}
}

func TestPrimaryHypothesisPrefersSpecificConfirmedRootCause(t *testing.T) {
	hypotheses := []model.Hypothesis{
		{
			HypothesisType:  "scheduling_constraint",
			Summary:         "Pod cannot be scheduled.",
			ConfidenceScore: 0.79,
			Status:          model.HypothesisStatusConfirmed,
		},
		{
			HypothesisType:  "pvc_unbound",
			Summary:         "PVC is not bound.",
			ConfidenceScore: 0.79,
			Status:          model.HypothesisStatusConfirmed,
		},
	}

	primary := selectPrimaryHypothesis(hypotheses)
	if primary == nil || primary.HypothesisType != "pvc_unbound" {
		t.Fatalf("expected pvc_unbound primary, got %#v", primary)
	}

	snapshotPrimary, contributing := rootCauseFactors(nil, hypotheses, nil)
	if snapshotPrimary == nil || snapshotPrimary.HypothesisType != "pvc_unbound" {
		t.Fatalf("expected pvc_unbound snapshot primary, got %#v", snapshotPrimary)
	}
	if len(contributing) == 0 || contributing[0].HypothesisType != "scheduling_constraint" {
		t.Fatalf("expected scheduling_constraint as contributing factor, got %#v", contributing)
	}
}
