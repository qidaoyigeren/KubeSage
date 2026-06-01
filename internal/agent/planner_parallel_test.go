package agent

import (
	"context"
	"testing"
)

func TestRulePlannerGroupsEvidenceToolsForParallelExecution(t *testing.T) {
	tools := []ToolMetadata{
		{Name: "k8s.get_pod", ReadOnly: true},
		{Name: "runbook.search", ReadOnly: true},
		{Name: "k8s.get_previous_logs", ReadOnly: true},
		{Name: "k8s.get_events", ReadOnly: true},
		{Name: "k8s.get_topology", ReadOnly: true},
	}
	plan := NewRulePlanner().BuildInitialPlan(context.Background(), Goal{ExpectedFault: "CrashLoopBackOff"}, tools)
	found := 0
	for _, step := range plan.Steps {
		if step.ParallelGroup == "evidence-snapshot" {
			found++
		}
	}
	if found < 3 {
		t.Fatalf("expected evidence tools in a parallel group, got %d: %+v", found, plan.Steps)
	}
}

func TestNextPlanStepsReturnsSameParallelGroup(t *testing.T) {
	plan := Plan{Steps: []PlanStep{
		{ID: "1", ToolName: "k8s.get_pod"},
		{ID: "2", ToolName: "k8s.get_events", ParallelGroup: "evidence-snapshot"},
		{ID: "3", ToolName: "k8s.get_previous_logs", ParallelGroup: "evidence-snapshot"},
	}}
	markStepComplete(&plan, "1")
	steps := nextPlanSteps(&plan)
	if len(steps) != 2 {
		t.Fatalf("expected two parallel steps, got %d", len(steps))
	}
}

func TestLLMPlanSanitizerAddsCrashLoopBaselineEvidence(t *testing.T) {
	tools := []ToolMetadata{
		{Name: "k8s.get_pod", ReadOnly: true},
		{Name: "k8s.get_events", ReadOnly: true},
		{Name: "k8s.get_logs", ReadOnly: true},
		{Name: "k8s.get_topology", ReadOnly: true},
	}
	plan := sanitizePlan(Plan{
		Summary: "minimal llm plan",
		Steps: []PlanStep{
			{ID: "llm-1", ToolName: "k8s.get_pod"},
		},
	}, tools, Goal{ExpectedFault: "CrashLoopBackOff", Namespace: "default", PodName: "app"})

	for _, tool := range []string{"k8s.get_events", "k8s.get_logs", "k8s.get_topology"} {
		if !planHasTool(&plan, tool) {
			t.Fatalf("expected crashloop baseline tool %s in sanitized plan: %+v", tool, plan.Steps)
		}
	}
	for _, step := range plan.Steps {
		if step.ToolName == "k8s.get_events" || step.ToolName == "k8s.get_logs" || step.ToolName == "k8s.get_topology" {
			if step.ParallelGroup != "evidence-snapshot" {
				t.Fatalf("expected %s to be in evidence-snapshot group: %+v", step.ToolName, plan.Steps)
			}
		}
	}
}
