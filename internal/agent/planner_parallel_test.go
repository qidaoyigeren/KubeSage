package agent

import (
	"context"
	"strings"
	"testing"

	"kubesage/internal/diagnostic"
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

func TestLLMPlannerUsesCollectedContextForAdjustmentAndReflection(t *testing.T) {
	client := &capturingPlanClient{}
	planner := NewLLMPlanner(client)
	tools := []ToolMetadata{
		{Name: "k8s.get_pod", ReadOnly: true},
		{Name: "k8s.get_events", ReadOnly: true},
		{Name: "danger.patch", ReadOnly: false},
	}
	goal := Goal{Namespace: "default", PodName: "api-0"}
	state := newToolState(1, goal, tools)
	state.RecordToolResult(ToolResult{
		ToolName:    "k8s.get_pod",
		Success:     true,
		Observation: "pod restart count increased",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "Pod restart evidence",
			Content:    "restartCount=4 reason=OOMKilled",
			Severity:   "warning",
		}},
	})
	plan := Plan{Steps: []PlanStep{{ID: "pod", ToolName: "k8s.get_pod", Completed: true}}}
	planner.AdjustPlan(&plan, state, ToolResult{}, []HypothesisScore{{Type: "memory_limit_too_low", Confidence: 0.7}})
	if len(client.adjustment.Tools) != 2 {
		t.Fatalf("expected read-only tools in adjustment prompt, got %#v", client.adjustment.Tools)
	}
	if len(client.adjustment.Observations) == 0 || !strings.Contains(client.adjustment.Observations[0], "restart count") {
		t.Fatalf("expected collected observations, got %#v", client.adjustment.Observations)
	}
	if !strings.Contains(client.adjustment.Evidence, "reason=OOMKilled") {
		t.Fatalf("expected collected evidence summary, got %q", client.adjustment.Evidence)
	}

	if _, err := planner.Reflect(context.Background(), plan, state, []HypothesisScore{{Type: "memory_limit_too_low", Confidence: 0.7}}); err != nil {
		t.Fatal(err)
	}
	if len(client.reflection.Tools) != 2 {
		t.Fatalf("expected read-only tools in reflection prompt, got %#v", client.reflection.Tools)
	}
	if !strings.Contains(client.reflection.Evidence, "reason=OOMKilled") {
		t.Fatalf("expected reflection evidence summary, got %q", client.reflection.Evidence)
	}
	if !containsInstruction(client.reflection.Safety, "container_name") ||
		!containsInstruction(client.reflection.Safety, "ConfigMap") {
		t.Fatalf("expected input-aware config reflection guidance, got %#v", client.reflection.Safety)
	}
}

func TestSanitizeReflectionStepEnforcesReadOnlyScopeAndSchema(t *testing.T) {
	tools := []ToolMetadata{
		{
			Name:        "k8s.get_logs",
			ReadOnly:    true,
			InputSchema: map[string]string{"namespace": "string", "pod_name": "string", "container_name": "string"},
		},
		{Name: "danger.patch", ReadOnly: false, InputSchema: map[string]string{"namespace": "string"}},
	}
	step, ok := sanitizeReflectionStep(PlanStep{
		ToolName: "k8s.get_logs",
		Input: map[string]interface{}{
			"namespace":     "other",
			"pod_name":      "other-pod",
			"containerName": "init-config",
			"unexpected":    "drop-me",
		},
	}, tools, Goal{Namespace: "default", PodName: "api-0"})
	if !ok {
		t.Fatal("expected registered read-only tool to pass")
	}
	if step.Input["namespace"] != "default" || step.Input["pod_name"] != "api-0" {
		t.Fatalf("expected diagnosis scope to be enforced, got %#v", step.Input)
	}
	if step.Input["container_name"] != "init-config" {
		t.Fatalf("expected container input alias to normalize, got %#v", step.Input)
	}
	if _, exists := step.Input["unexpected"]; exists {
		t.Fatalf("unexpected input must be removed, got %#v", step.Input)
	}
	if _, ok := sanitizeReflectionStep(PlanStep{ToolName: "danger.patch"}, tools, Goal{}); ok {
		t.Fatal("read-write reflection tool must be rejected")
	}
}

func containsInstruction(items []string, needle string) bool {
	for _, item := range items {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}

type capturingPlanClient struct {
	adjustment AdjustmentPrompt
	reflection ReflectionPrompt
}

func (c *capturingPlanClient) GenerateAgentPlan(ctx context.Context, prompt PlanPrompt) (Plan, error) {
	_ = ctx
	_ = prompt
	return Plan{}, nil
}

func (c *capturingPlanClient) GeneratePlanAdjustment(ctx context.Context, prompt AdjustmentPrompt) (Plan, error) {
	_ = ctx
	c.adjustment = prompt
	return Plan{Steps: []PlanStep{{ID: "events", ToolName: "k8s.get_events", Reason: "collect distinguishing events"}}}, nil
}

func (c *capturingPlanClient) GenerateReflection(ctx context.Context, prompt ReflectionPrompt) (ReflectionResult, error) {
	_ = ctx
	c.reflection = prompt
	return ReflectionResult{ShouldContinue: true, Reason: "more evidence"}, nil
}
