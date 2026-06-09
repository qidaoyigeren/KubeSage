package agent

import "testing"

func TestInputsMatchIgnoresIdentityAndTracksMeaningfulInputs(t *testing.T) {
	base := map[string]interface{}{
		"namespace":      "default",
		"pod_name":       "api-0",
		"container_name": "app",
	}
	same := map[string]interface{}{
		"namespace":     "other",
		"podName":       "other-pod",
		"containerName": "app",
	}
	different := map[string]interface{}{
		"namespace":      "default",
		"pod_name":       "api-0",
		"container_name": "init-config",
	}
	if !inputsMatch(base, same) {
		t.Fatalf("expected identity fields and input aliases to normalize: %#v %#v", base, same)
	}
	if inputsMatch(base, different) {
		t.Fatalf("different container inputs must not match: %#v %#v", base, different)
	}
}

func TestCompletedToolInputsTrackVariants(t *testing.T) {
	state := &ToolState{}
	appInput := map[string]interface{}{"namespace": "default", "pod_name": "api-0", "container_name": "app"}
	initInput := map[string]interface{}{"namespace": "default", "pod_name": "api-0", "container_name": "init-config"}

	markToolComplete(state, "k8s.get_logs", appInput)
	markToolComplete(state, "k8s.get_logs", appInput)
	markToolComplete(state, "k8s.get_logs", initInput)

	if !toolCompletedWithInput(state, "k8s.get_logs", appInput) {
		t.Fatal("expected app log input to be complete")
	}
	if !toolCompletedWithInput(state, "k8s.get_logs", initInput) {
		t.Fatal("expected init log input to be complete")
	}
	if got := len(state.CompletedToolInputs["k8s.get_logs"]); got != 2 {
		t.Fatalf("expected two unique input variants, got %d: %#v", got, state.CompletedToolInputs)
	}
}

func TestRedundantReflectionStepIsInputAware(t *testing.T) {
	appInput := map[string]interface{}{"container_name": "app"}
	initInput := map[string]interface{}{"container_name": "init-config"}
	plan := Plan{Steps: []PlanStep{{
		ID:        "logs-app",
		ToolName:  "k8s.get_logs",
		Input:     appInput,
		Completed: true,
	}}}
	state := &ToolState{}
	markToolComplete(state, "k8s.get_logs", appInput)

	if !redundantReflectionStep(&plan, state, PlanStep{ToolName: "k8s.get_logs", Input: appInput}) {
		t.Fatal("same tool and input should be redundant")
	}
	if redundantReflectionStep(&plan, state, PlanStep{ToolName: "k8s.get_logs", Input: initInput}) {
		t.Fatal("same tool with a different container must remain runnable")
	}

	runbookInput := map[string]interface{}{"query": "configmap missing"}
	markToolComplete(state, "runbook.search", runbookInput)
	if !redundantReflectionStep(&plan, state, PlanStep{ToolName: "runbook.search", Input: runbookInput}) {
		t.Fatal("same runbook query should be redundant")
	}
	if redundantReflectionStep(&plan, state, PlanStep{ToolName: "runbook.search", Input: map[string]interface{}{"query": "volume mount path"}}) {
		t.Fatal("a materially different runbook query should remain runnable")
	}
}

func TestFailedCompletedPlanStepCanBeRetried(t *testing.T) {
	input := map[string]interface{}{"container_name": "app"}
	plan := Plan{Steps: []PlanStep{{
		ID:        "failed-logs",
		ToolName:  "k8s.get_logs",
		Input:     input,
		Completed: true,
	}}}
	if redundantReflectionStep(&plan, &ToolState{}, PlanStep{ToolName: "k8s.get_logs", Input: input}) {
		t.Fatal("plan completion without successful state history must not suppress a retry")
	}
}

func TestDropCompletedSnapshotStepsOnlyDropsMatchingInput(t *testing.T) {
	appInput := map[string]interface{}{"container_name": "app"}
	initInput := map[string]interface{}{"container_name": "init-config"}
	state := &ToolState{}
	markToolComplete(state, "k8s.get_logs", appInput)
	plan := Plan{Steps: []PlanStep{
		{ID: "logs-app", ToolName: "k8s.get_logs", Input: appInput},
		{ID: "logs-init", ToolName: "k8s.get_logs", Input: initInput},
	}}

	dropCompletedSnapshotSteps(&plan, state)

	if !plan.Steps[0].Skipped {
		t.Fatal("matching completed input should be skipped")
	}
	if plan.Steps[1].Skipped {
		t.Fatal("different input should not be skipped")
	}
}

func TestLegacyCompletedToolOnlyBlocksDefaultInput(t *testing.T) {
	state := &ToolState{CompletedTools: map[string]bool{"k8s.get_logs": true}}
	if !toolCompletedWithInput(state, "k8s.get_logs", nil) {
		t.Fatal("legacy completion should cover the default call")
	}
	if toolCompletedWithInput(state, "k8s.get_logs", map[string]interface{}{"container_name": "init-config"}) {
		t.Fatal("legacy name-only completion cannot prove a targeted call ran")
	}
}
