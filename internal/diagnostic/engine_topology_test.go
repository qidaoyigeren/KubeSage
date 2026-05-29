package diagnostic

import (
	"strings"
	"testing"
)

// TestEnrichReportWithTopology verifies topology facts are saved as evidence
// and appended to impact analysis.
func TestEnrichReportWithTopology(t *testing.T) {
	report := &Report{ImpactAnalysis: "base impact"}
	ctx := &DiagnosticContext{
		Topology: &TopologyInfo{
			DeploymentName: "api",
			ReplicaSetName: "api-abc",
			Workload: &WorkloadInfo{
				Kind:              "Deployment",
				Name:              "api",
				DesiredReplicas:   3,
				OtherPods:         2,
				OtherRunning:      2,
				OtherReady:        2,
				OtherAbnormal:     0,
				OtherRestartCount: 0,
			},
			SelectedServices: []ServiceBrief{
				{Name: "api", AvailableEndpoints: 0},
			},
			Node: &NodeHealth{
				Name:           "node-a",
				Ready:          true,
				MemoryPressure: true,
			},
		},
	}

	enrichReportWithTopology(ctx, report)
	if len(report.Evidences) != 1 {
		t.Fatalf("expected topology evidence, got %d", len(report.Evidences))
	}
	if report.Evidences[0].SourceType != "k8s_topology" {
		t.Fatalf("unexpected evidence source: %s", report.Evidences[0].SourceType)
	}
	for _, expected := range []string{"多副本", "Service endpoints 全部不可用", "Node 存在 Pressure"} {
		if !strings.Contains(report.ImpactAnalysis, expected) {
			t.Fatalf("impact analysis missing %q: %s", expected, report.ImpactAnalysis)
		}
	}
}
