package agent

import (
	"strings"
	"testing"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestAlignReportWithHypothesesOverridesAnalyzerPrimary(t *testing.T) {
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
		t.Fatalf("expected alignment to change report")
	}
	if report.FaultType != "ProbeFailed" {
		t.Fatalf("expected ProbeFailed fault type, got %s", report.FaultType)
	}
	if report.PrimaryRootCause == nil || report.PrimaryRootCause.FaultType != "ProbeFailed" {
		t.Fatalf("unexpected primary root cause: %#v", report.PrimaryRootCause)
	}
	if !strings.Contains(report.RootCauseSummary, "Readiness probe") {
		t.Fatalf("expected hypothesis summary in report, got %q", report.RootCauseSummary)
	}
	if len(report.ContributingFactors) == 0 || report.ContributingFactors[0].FaultType != "CrashLoopBackOff" {
		t.Fatalf("expected displaced analyzer primary as contributing factor, got %#v", report.ContributingFactors)
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
