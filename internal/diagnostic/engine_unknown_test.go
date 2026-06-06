package diagnostic_test

import (
	"testing"

	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDiagnosisEngineReturnsUnknownForUnsupportedCNIFailure(t *testing.T) {
	registry, err := diagnosticanalyzer.NewDefaultRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := diagnostic.NewDiagnosisEngine(registry.Analyzers()...)
	ctx := unsupportedCNIFailureContext()

	report, err := engine.Diagnose(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if report.FaultType != "unknown" {
		t.Fatalf("fault type = %q, want unknown", report.FaultType)
	}
	if report.ConfidenceScore != 0.3 {
		t.Fatalf("confidence = %.2f, want 0.30", report.ConfidenceScore)
	}
	if !report.NeedHumanConfirm {
		t.Fatal("unknown diagnosis must require human confirmation")
	}
	if len(report.RemediationActions) != 0 {
		t.Fatalf("unknown diagnosis generated remediation actions: %#v", report.RemediationActions)
	}
	noMatchEvidence := false
	for _, evidence := range report.Evidences {
		if evidence.Title == "No analyzer matched" {
			noMatchEvidence = true
			break
		}
	}
	if !noMatchEvidence {
		t.Fatalf("unexpected evidence: %#v", report.Evidences)
	}

	t.Logf(
		"fault_type=%s confidence=%.2f human_confirm=%t evidence=%q remediation_actions=%d",
		report.FaultType,
		report.ConfidenceScore,
		report.NeedHumanConfirm,
		"No analyzer matched",
		len(report.RemediationActions),
	)
}

func unsupportedCNIFailureContext() *diagnostic.DiagnosticContext {
	return &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "network-failure",
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "network-failure",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "example/app:latest"}},
				NodeName:   "worker-1",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "app",
					Ready: false,
				}},
			},
		},
		Events: []corev1.Event{{
			Reason:  "FailedCreatePodSandBox",
			Message: "Failed to create pod sandbox: CNI plugin failed to set up pod network",
		}},
		Logs: []diagnostic.ContainerLogs{{
			ContainerName: "app",
			Current:       "application is waiting for pod network setup",
		}},
		Topology: &diagnostic.TopologyInfo{
			NodeName: "worker-1",
			Node: &diagnostic.NodeHealth{
				Name:  "worker-1",
				Ready: true,
			},
		},
	}
}
