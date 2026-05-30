package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

func TestDefaultRegistryOrdersPluginAnalyzers(t *testing.T) {
	registry, err := NewDefaultRegistry(nil)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	metadata := registry.Metadata()
	if len(metadata) < 7 {
		t.Fatalf("expected new and existing analyzers, got %d", len(metadata))
	}
	if metadata[0].FaultType != "OOMKilled" {
		t.Fatalf("expected OOMKilled highest priority, got %+v", metadata[0])
	}
	seen := map[string]bool{}
	for _, item := range metadata {
		seen[item.FaultType] = true
		if len(item.MatchSignals) == 0 || len(item.RequiredEvidence) == 0 {
			t.Fatalf("metadata missing signals/evidence: %+v", item)
		}
	}
	for _, fault := range []string{"ImagePullBackOff", "InitError", "Evicted"} {
		if !seen[fault] {
			t.Fatalf("missing analyzer for %s", fault)
		}
	}
}

func TestImagePullBackOffAnalyzerMatchesWaitingReason(t *testing.T) {
	analyzer := NewImagePullBackOffAnalyzer()
	ctx := &diagnostic.DiagnosticContext{Pod: &corev1.Pod{
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}},
	}}
	if !analyzer.Match(ctx) {
		t.Fatalf("expected image pull analyzer to match")
	}
}

func TestEvictedAnalyzerMatchesPodReason(t *testing.T) {
	analyzer := NewEvictedAnalyzer()
	ctx := &diagnostic.DiagnosticContext{Pod: &corev1.Pod{
		Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"},
	}}
	if !analyzer.Match(ctx) {
		t.Fatalf("expected evicted analyzer to match")
	}
}
