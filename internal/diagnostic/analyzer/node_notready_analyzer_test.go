package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeNotReadyAnalyzer_Match(t *testing.T) {
	tests := []struct {
		name string
		ctx  *diagnostic.DiagnosticContext
		want bool
	}{
		{
			name: "nil topology",
			ctx:  &diagnostic.DiagnosticContext{},
			want: false,
		},
		{
			name: "nil node",
			ctx:  &diagnostic.DiagnosticContext{Topology: &diagnostic.TopologyInfo{}},
			want: false,
		},
		{
			name: "node ready",
			ctx: &diagnostic.DiagnosticContext{
				Topology: &diagnostic.TopologyInfo{Node: &diagnostic.NodeHealth{Ready: true}},
			},
			want: false,
		},
		{
			name: "node not ready",
			ctx: &diagnostic.DiagnosticContext{
				Topology: &diagnostic.TopologyInfo{Node: &diagnostic.NodeHealth{Ready: false}},
			},
			want: true,
		},
	}
	a := NewNodeNotReadyAnalyzer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Match(tt.ctx); got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeNotReadyAnalyzer_Analyze(t *testing.T) {
	a := NewNodeNotReadyAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Topology: &diagnostic.TopologyInfo{
			Node: &diagnostic.NodeHealth{
				Name:           "node-1",
				Ready:          false,
				MemoryPressure: true,
				DiskPressure:   false,
				PIDPressure:    false,
			},
		},
		Events: []corev1.Event{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "evt-1"},
				Reason:        "NodeNotReady",
				Message:       "Node node-1 status is now: NodeNotReady",
				Type:          "Warning",
				LastTimestamp: metav1.Now(),
			},
		},
	}
	result, err := a.Analyze(ctx)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.FaultType != "NodeNotReady" {
		t.Errorf("FaultType = %v, want NodeNotReady", result.FaultType)
	}
	if result.ConfidenceScore < 0.8 {
		t.Errorf("ConfidenceScore = %v, want >= 0.8", result.ConfidenceScore)
	}
	if len(result.Evidences) < 2 {
		t.Errorf("Evidences count = %d, want >= 2", len(result.Evidences))
	}
}

func TestNodeNotReadyAnalyzer_Metadata(t *testing.T) {
	a := NewNodeNotReadyAnalyzer()
	md := a.Metadata()
	if md.FaultType != "NodeNotReady" {
		t.Errorf("Metadata.FaultType = %v, want NodeNotReady", md.FaultType)
	}
	if md.Priority != 55 {
		t.Errorf("Metadata.Priority = %v, want 55", md.Priority)
	}
}
