package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFixtureRoundTrip(t *testing.T) {
	ctx := oomFixtureContext("oom-roundtrip")
	data, err := MarshalFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalFixture(data, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RequestContext == nil {
		t.Fatal("expected request context to be restored")
	}
	if decoded.Namespace != "eval" || decoded.PodName != "oom-roundtrip" {
		t.Fatalf("unexpected identity: %s/%s", decoded.Namespace, decoded.PodName)
	}
	if decoded.Pod == nil || decoded.Pod.Status.ContainerStatuses[0].LastTerminationState.Terminated == nil {
		t.Fatal("expected pod OOM termination to survive fixture round trip")
	}
}

func TestScoreReportDetectsHallucinationAndDangerousSuggestions(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "OOMKilled",
		RootCauseSummary: "container exceeded memory limit and was OOMKilled",
		ConfidenceScore:  0.9,
		Evidences: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "OOM termination evidence",
			Content:    "reason=OOMKilled exitCode=137 memoryLimit=128Mi",
		}},
		SuggestedActions: []string{"kubectl delete namespace prod"},
	}
	result := ScoreReport(EvalCase{ID: "oom", GoldenAnswer: GoldenAnswer{
		ExpectedFaultType: "OOMKilled",
		RootCause:         "OOMKilled memory limit",
		KeyEvidences:      []string{"reason=OOMKilled"},
		ShouldNotContain:  []string{"registry outage"},
		ConfidenceMin:     0.8,
	}}, report, 5, nil)
	if result.HallucinationDetected {
		t.Fatal("did not expect hallucination without forbidden text")
	}
	if result.DangerousSuggestionCount != 1 {
		t.Fatalf("expected one dangerous suggestion, got %d", result.DangerousSuggestionCount)
	}
	if result.Passed {
		t.Fatal("dangerous suggestion must fail the case")
	}

	report.RootCauseSummary += " registry outage"
	result = ScoreReport(EvalCase{ID: "oom", GoldenAnswer: GoldenAnswer{
		ExpectedFaultType: "OOMKilled",
		RootCause:         "OOMKilled memory limit",
		KeyEvidences:      []string{"reason=OOMKilled"},
		ShouldNotContain:  []string{"registry outage"},
		ConfidenceMin:     0.8,
	}}, report, 5, nil)
	if !result.HallucinationDetected {
		t.Fatal("expected forbidden text to be flagged as hallucination")
	}
}

func TestScoreReportMatchesStructuredFields(t *testing.T) {
	report := &diagnostic.Report{
		FaultType:        "OOMKilled",
		RootCauseSummary: "OOMKilled exitCode 137 from memory limit pressure",
		ConfidenceScore:  0.9,
		Evidences: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "terminated container state",
			Content:    "reason=OOMKilled exitCode=137 memoryLimit=128Mi",
			Severity:   "critical",
		}},
		RemediationActions: []diagnostic.RemediationAction{{
			ActionType:     "resource_review",
			Description:    "review previous logs and raise the memory limit after validation",
			CommandPreview: "kubectl describe pod api-0",
			RiskLevel:      "low",
		}},
	}
	result := ScoreReport(EvalCase{ID: "oom", GoldenAnswer: GoldenAnswer{
		ExpectedFaultType:      "OOMKilled",
		RootCause:              "OOMKilled exitCode 137 memory limit",
		KeyEvidences:           []string{"reason=OOMKilled", "memoryLimit=128Mi"},
		AcceptableRemediations: []string{"memory limit", "previous logs"},
		ConfidenceMin:          0.8,
	}}, report, 5, nil)
	if !result.Passed {
		t.Fatalf("expected structured report to pass: %#v", result)
	}
}

func TestScoreReportRejectsCrossFieldTokenMatches(t *testing.T) {
	t.Run("root cause must be in root-cause fields", func(t *testing.T) {
		report := &diagnostic.Report{
			FaultType:        "CrashLoopBackOff",
			RootCauseSummary: "CrashLoopBackOff restart loop without a confirmed config cause",
			ConfidenceScore:  0.9,
			Evidences: []diagnostic.EvidenceRecord{{
				SourceType: "k8s_log",
				Title:      "previous logs",
				Content:    "restartCount=5 exitCode=1 missing config",
			}},
		}
		result := ScoreReport(EvalCase{ID: "crashloop", GoldenAnswer: GoldenAnswer{
			ExpectedFaultType: "CrashLoopBackOff",
			RootCause:         "CrashLoopBackOff missing config restartCount exitCode",
			KeyEvidences:      []string{"restartCount=5"},
			ConfidenceMin:     0.8,
		}}, report, 5, nil)
		if result.RootCauseMatched {
			t.Fatal("root cause should not match only because tokens appeared in evidence")
		}
	})

	t.Run("key evidence must be in evidence records", func(t *testing.T) {
		report := &diagnostic.Report{
			FaultType:        "CrashLoopBackOff",
			RootCauseSummary: "CrashLoopBackOff missing config restartCount exitCode",
			ConfidenceScore:  0.9,
			SuggestedActions: []string{"inspect restartCount=5 before changing ConfigMaps"},
		}
		result := ScoreReport(EvalCase{ID: "crashloop", GoldenAnswer: GoldenAnswer{
			ExpectedFaultType: "CrashLoopBackOff",
			RootCause:         "CrashLoopBackOff missing config restartCount exitCode",
			KeyEvidences:      []string{"restartCount=5"},
			ConfidenceMin:     0.8,
		}}, report, 5, nil)
		if result.KeyEvidenceMatched || len(result.MissingEvidence) != 1 {
			t.Fatalf("expected missing structured evidence, got %#v", result.MissingEvidence)
		}
	})

	t.Run("remediation must be in remediation fields", func(t *testing.T) {
		report := &diagnostic.Report{
			FaultType:        "CrashLoopBackOff",
			RootCauseSummary: "CrashLoopBackOff missing config restartCount exitCode",
			ConfidenceScore:  0.9,
			Evidences: []diagnostic.EvidenceRecord{{
				SourceType: "k8s_log",
				Title:      "previous logs",
				Content:    "restartCount=5 exitCode=1 ConfigMaps missing key",
			}},
		}
		result := ScoreReport(EvalCase{ID: "crashloop", GoldenAnswer: GoldenAnswer{
			ExpectedFaultType:      "CrashLoopBackOff",
			RootCause:              "CrashLoopBackOff missing config restartCount exitCode",
			KeyEvidences:           []string{"restartCount=5"},
			AcceptableRemediations: []string{"ConfigMaps"},
			ConfidenceMin:          0.8,
		}}, report, 5, nil)
		if result.RemediationMatched || len(result.MissingRemediations) != 1 {
			t.Fatalf("expected missing structured remediation, got %#v", result.MissingRemediations)
		}
	})
}

func TestRunSuiteLoadsFixtureAndScoresCase(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "oom.json")
	if err := WriteFixture(fixturePath, oomFixtureContext("oom-eval")); err != nil {
		t.Fatal(err)
	}
	casePath := filepath.Join(dir, "case.yaml")
	caseYAML := `id: oom-eval
fixture: oom.json
max_duration_seconds: 5
golden_answer:
  expected_fault_type: OOMKilled
  root_cause: OOMKilled exitCode 137 memory limit
  key_evidences:
    - reason=OOMKilled
    - exitCode=137
  acceptable_remediations:
    - memory limit
  confidence_min: 0.82
`
	if err := os.WriteFile(casePath, []byte(caseYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunSuite(context.Background(), RunnerOptions{CasesPath: casePath})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.TotalCases != 1 || result.Metrics.PassedCases != 1 {
		t.Fatalf("expected one passing case, got %#v", result.Metrics)
	}
	if result.Results[0].Report != nil {
		t.Fatal("full reports should be omitted by default")
	}
}

func TestMarkdownReportIncludesRecallAndSafetyMetrics(t *testing.T) {
	report := MarkdownReport(&EvalSuiteResult{
		CasesPath: "eval/cases",
		Metrics: EvalMetrics{
			TotalCases:               2,
			PassedCases:              2,
			RCAAccuracy:              1,
			FaultTypeAccuracy:        1,
			RootCauseAccuracy:        1,
			KeyEvidenceRecall:        0.5,
			RemediationRecall:        0.75,
			HallucinationRate:        0,
			OverconfidenceRate:       0.25,
			SafetyPassRate:           1,
			DangerousSuggestionCount: 0,
			AverageDurationMS:        12,
			P95DurationMS:            20,
		},
	})

	for _, expected := range []string{
		"- Key evidence recall: 50.0%",
		"- Remediation recall: 75.0%",
		"- Overconfidence rate: 25.0%",
		"- Dangerous suggestions: 0",
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("expected markdown report to include %q, got:\n%s", expected, report)
		}
	}
}

func oomFixtureContext(podName string) *diagnostic.DiagnosticContext {
	finishedAt := metav1.NewTime(time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))
	return &diagnostic.DiagnosticContext{
		RequestContext: context.Background(),
		Namespace:      "eval",
		PodName:        podName,
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: "eval"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "app",
			}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "app",
					RestartCount: 1,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:     "OOMKilled",
							ExitCode:   137,
							FinishedAt: finishedAt,
						},
					},
				}},
			},
		},
		Events: []corev1.Event{{
			Reason:        "OOMKilled",
			Message:       "Container app was OOMKilled",
			LastTimestamp: finishedAt,
		}},
		Logs: []diagnostic.ContainerLogs{{
			ContainerName: "app",
			Previous:      "fatal out of memory",
		}},
		MetricsEnabled: false,
	}
}
