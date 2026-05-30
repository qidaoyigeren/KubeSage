package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

// TestPendingAnalyzerUsesPVCStatus verifies direct PVC snapshots influence
// evidence and confidence for Pending pods.
func TestPendingAnalyzerUsesPVCStatus(t *testing.T) {
	ctx := &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
		PVCs: []diagnostic.PVCBrief{{Name: "data", Phase: string(corev1.ClaimPending)}},
	}
	result, err := NewPendingAnalyzer().Analyze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfidenceScore <= 0.70 {
		t.Fatalf("expected PVC evidence to raise confidence, got %f", result.ConfidenceScore)
	}
	found := false
	for _, evidence := range result.Evidences {
		if evidence.SourceType == "k8s_pvc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected k8s_pvc evidence, got %#v", result.Evidences)
	}
}
