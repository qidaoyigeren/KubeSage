package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

func TestProbeFailedAnalyzerMatchesStartupProbeFailures(t *testing.T) {
	analyzer := NewProbeFailedAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Events: []corev1.Event{{
			Reason:  "Unhealthy",
			Message: "Startup probe failed: context deadline exceeded",
		}},
	}

	if !analyzer.Match(ctx) {
		t.Fatal("expected startup probe failure event to match")
	}
}
