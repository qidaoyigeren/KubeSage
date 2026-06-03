package agent

import "testing"

func TestSanitizePlanUsesToolMetadataForCriticalFlag(t *testing.T) {
	tools := []ToolMetadata{
		{Name: "k8s.get_pod", ReadOnly: true, Critical: true},
		{Name: "prometheus.query_range", ReadOnly: true, Critical: false},
	}
	plan := sanitizePlan(Plan{Steps: []PlanStep{
		{ID: "pod", ToolName: "k8s.get_pod", Critical: false},
		{ID: "metrics", ToolName: "prometheus.query_range", Critical: true},
	}}, tools, Goal{Namespace: "default", PodName: "api-0"})
	if len(plan.Steps) != 2 {
		t.Fatalf("expected two sanitized steps, got %#v", plan.Steps)
	}
	if !plan.Steps[0].Critical {
		t.Fatalf("expected k8s.get_pod to remain critical")
	}
	if plan.Steps[1].Critical {
		t.Fatalf("expected prometheus.query_range to be non-critical despite LLM critical flag")
	}
}
