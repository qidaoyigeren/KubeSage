package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestEvictedAnalyzer_Match(t *testing.T) {
	tests := []struct {
		name string
		ctx  *diagnostic.DiagnosticContext
		want bool
	}{
		{
			name: "nil pod",
			ctx:  &diagnostic.DiagnosticContext{},
			want: false,
		},
		{
			name: "evicted status reason",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{Reason: "Evicted"},
				},
			},
			want: true,
		},
		{
			name: "eviction event",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{Phase: corev1.PodFailed},
				},
				Events: []corev1.Event{
					{Message: "Pod was evicted due to memory pressure"},
				},
			},
			want: true,
		},
		{
			name: "running pod no eviction",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				},
			},
			want: false,
		},
	}
	a := NewEvictedAnalyzer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Match(tt.ctx); got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvictedAnalyzer_Analyze(t *testing.T) {
	a := NewEvictedAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: mustParse("128Mi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Reason: "Evicted",
				Message: "The node was low on resource: memory",
			},
		},
		Topology: &diagnostic.TopologyInfo{
			Node: &diagnostic.NodeHealth{
				Name:           "node-1",
				Ready:          true,
				MemoryPressure: true,
			},
		},
	}
	result, err := a.Analyze(ctx)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.FaultType != "Evicted" {
		t.Errorf("FaultType = %v, want Evicted", result.FaultType)
	}
	if result.ConfidenceScore < 0.6 {
		t.Errorf("ConfidenceScore = %v, want >= 0.6", result.ConfidenceScore)
	}
}

func mustParse(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}
