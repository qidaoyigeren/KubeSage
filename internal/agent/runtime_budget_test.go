package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRunLocalLLMTokenBudgetsAreIndependent(t *testing.T) {
	first := WithLLMTokenBudget(context.Background(), 100)
	second := WithLLMTokenBudget(context.Background(), 100)

	RecordLLMTokenUsage(first, 40)
	if got := LLMTokenUsage(first); got != 40 {
		t.Fatalf("first run usage = %d, want 40", got)
	}
	if got := LLMTokenUsage(second); got != 0 {
		t.Fatalf("second run was contaminated by first run usage: %d", got)
	}
}

func TestToolAndReflectionRoundBudgetsAreEnforced(t *testing.T) {
	steps := []*PlanStep{
		{ID: "one", ToolName: "k8s.get_logs", Input: map[string]interface{}{"container_name": "one"}},
		{ID: "two", ToolName: "k8s.get_logs", Input: map[string]interface{}{"container_name": "two"}},
		{ID: "three", ToolName: "k8s.get_logs", Input: map[string]interface{}{"container_name": "three"}},
	}
	runnable := enforceToolCallBudgets(
		steps,
		map[string]int{},
		RuntimeOptions{MaxToolCallsPerTool: 2},
		newStepRecorder(nil, 1, "budget-test"),
		context.Background(),
	)
	if len(runnable) != 2 || !steps[2].Skipped {
		t.Fatalf("generic tool budget was not enforced: runnable=%d steps=%#v", len(runnable), steps)
	}

	ctx := WithLLMTokenBudget(context.Background(), 100)
	if allowed, reason := reflectionBudgetAvailable(ctx, 2, 0, RuntimeOptions{MaxReflectionRounds: 2, MaxReflectionSteps: 4}); allowed || reason != "reflection round budget exhausted" {
		t.Fatalf("reflection round budget result = allowed:%t reason:%q", allowed, reason)
	}
	// Token budget no longer blocks reflection — only round/step limits do.
	if allowed, _ := reflectionBudgetAvailable(ctx, 0, 0, RuntimeOptions{MaxReflectionRounds: 4, MaxReflectionSteps: 4}); !allowed {
		t.Fatal("reflection was blocked despite having round/step budget remaining")
	}
}

func TestRuntimeCapsReflectionAppendedSteps(t *testing.T) {
	planner := &manyReflectionStepsPlanner{}
	retriever := &countingRetriever{}
	store, result := runBudgetRuntime(t, planner, retriever, RuntimeOptions{
		MaxSteps:            12,
		MaxReflectionSteps:  2,
		MaxReflectionRounds: 5,
		MaxToolCallsPerTool: 10,
		MaxRunbookSearches:  10,
	})
	if result.StepsExecuted != 3 {
		t.Fatalf("expected seed plus two reflected steps, got %d", result.StepsExecuted)
	}
	if retriever.CallCount() != 2 {
		t.Fatalf("reflection step cap allowed %d runbook calls, want 2", retriever.CallCount())
	}
	if planner.ReflectionCalls() != 1 {
		t.Fatalf("expected reflection to stop after exhausting appended-step budget, got %d calls", planner.ReflectionCalls())
	}
	if countDecisionReason(store.steps, "reflection step budget exhausted") == 0 {
		t.Fatalf("expected rejected reflected steps to be audited, got %#v", store.steps)
	}
}

func TestRuntimeCapsDistinctRunbookSearches(t *testing.T) {
	retriever := &countingRetriever{}
	steps := []PlanStep{seedContextStep()}
	for index := 0; index < 5; index++ {
		steps = append(steps, PlanStep{
			ID:       fmt.Sprintf("runbook-%d", index),
			ToolName: "runbook.search",
			Input:    map[string]interface{}{"query": fmt.Sprintf("different query %d", index)},
			Reason:   "exercise distinct-query call budget",
		})
	}
	store, _ := runBudgetRuntime(t, staticBudgetPlanner{steps: steps}, retriever, RuntimeOptions{
		MaxSteps:            12,
		MaxToolCallsPerTool: 10,
		MaxRunbookSearches:  2,
	})
	if retriever.CallCount() != 2 {
		t.Fatalf("runbook search budget allowed %d calls, want 2", retriever.CallCount())
	}
	if countDecisionReason(store.steps, "tool call budget exhausted: runbook.search limit=2") != 3 {
		t.Fatalf("expected three audited runbook skips, got %#v", store.steps)
	}
}

func TestRuntimeAllowsReflectionRegardlessOfTokenUsage(t *testing.T) {
	planner := &tokenConsumingPlanner{}
	_, result := runBudgetRuntime(t, planner, &countingRetriever{}, RuntimeOptions{
		MaxSteps:            4,
		MaxReflectionSteps:  4,
		MaxReflectionRounds: 4,
		MaxLLMTokens:        50,
	})
	// Token budget no longer blocks reflection. The agent should still
	// complete normally — step/reflection limits are the real controls.
	if result == nil {
		t.Fatal("expected runtime to complete successfully")
	}
}

func TestRuntimeAppliesPerReflectionTimeout(t *testing.T) {
	planner := &timeoutReflectionPlanner{}
	start := time.Now()
	_, _ = runBudgetRuntime(t, planner, &countingRetriever{}, RuntimeOptions{
		MaxSteps:            4,
		MaxReflectionSteps:  4,
		MaxReflectionRounds: 1,
		ReflectionTimeout:   25 * time.Millisecond,
	})
	if planner.ReflectionCalls() != 1 {
		t.Fatalf("expected one reflection call, got %d", planner.ReflectionCalls())
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("reflection timeout did not bound runtime latency: %s", elapsed)
	}
}

func TestRuntimeAddsConfigEvidenceWithoutLLMFollowup(t *testing.T) {
	store := &memoryStore{}
	diagCtx := crashLoopContext()
	diagCtx.Logs = []diagnostic.ContainerLogs{{
		ContainerName: "app",
		Previous:      "fatal: config file not found at /etc/app/config.yaml",
	}}
	diagCtx.Pod.Spec.Volumes = []corev1.Volume{{
		Name: "config",
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
		}},
	}}
	diagCtx.Pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "config", MountPath: "/etc/app"}}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return diagCtx, nil
		},
		Retriever: fakeRetriever{},
		Policy:    NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("CrashLoopBackOff")},
		Policy:   NewRemediationPolicy(true),
		Planner:  singlePodPlanner{},
	})
	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    12,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "CrashLoopBackOff"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSuccessfulTool(store.steps, "k8s.get_config_refs") || !hasSuccessfulTool(store.steps, "runbook.search") {
		t.Fatalf("deterministic config guardrail did not collect both evidence sources: %#v", store.steps)
	}
	if !containsString(result.ExecutedToolNames, "k8s.get_config_refs") || !containsString(result.ExecutedToolNames, "runbook.search") {
		t.Fatalf("run result did not expose executed tools: %#v", result.ExecutedToolNames)
	}
	found := false
	for _, evidence := range result.Report.Evidences {
		if evidence.SourceType == "k8s_config_ref" && evidence.Severity != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected config reference evidence in report, got %#v", result.Report.Evidences)
	}
}

func TestConfigGuardrailRecognizesMissingObjectEvent(t *testing.T) {
	state := &ToolState{
		Goal: Goal{Namespace: "default", PodName: "api-0"},
		AvailableTools: []ToolMetadata{
			{Name: "k8s.get_config_refs", ReadOnly: true},
			{Name: "runbook.search", ReadOnly: true},
		},
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_event",
			Title:      "FailedMount",
			Content:    `MountVolume.SetUp failed: configmap "app-config" not found`,
		}},
	}
	plan := Plan{}
	if !ensureConfigFailureEvidence(&plan, state) {
		t.Fatal("expected missing ConfigMap event to append deterministic evidence steps")
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ToolName != "k8s.get_config_refs" || plan.Steps[1].ToolName != "runbook.search" {
		t.Fatalf("unexpected config guardrail plan: %#v", plan.Steps)
	}
}

func runBudgetRuntime(t *testing.T, planner Planner, retriever *countingRetriever, options RuntimeOptions) (*memoryStore, *RunResult) {
	t.Helper()
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Retriever: retriever,
		Policy:    NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(seedContextTool{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("Unknown")},
		Policy:   NewRemediationPolicy(true),
		Planner:  planner,
	})
	options.TaskID = 1
	options.ToolTimeout = time.Second
	options.Goal = Goal{Namespace: "default", PodName: "api-0"}
	result, err := runtime.Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	return store, result
}

type staticBudgetPlanner struct {
	steps []PlanStep
}

func (p staticBudgetPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	_ = ctx
	_ = goal
	_ = tools
	return Plan{Summary: "budget test plan", Steps: append([]PlanStep(nil), p.steps...)}
}

func (staticBudgetPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
}

type manyReflectionStepsPlanner struct {
	mu           sync.Mutex
	reflectCalls int
}

func (p *manyReflectionStepsPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	return Plan{Steps: []PlanStep{seedContextStep()}}
}

func (p *manyReflectionStepsPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
}

func (p *manyReflectionStepsPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	p.mu.Lock()
	p.reflectCalls++
	p.mu.Unlock()
	steps := make([]PlanStep, 0, 5)
	for index := 0; index < 5; index++ {
		steps = append(steps, PlanStep{
			ToolName: "runbook.search",
			Input:    map[string]interface{}{"query": fmt.Sprintf("reflection query %d", index)},
			Reason:   "test reflection budget",
		})
	}
	return ReflectionResult{ShouldContinue: true, Reason: "request many steps", NewSteps: steps}, nil
}

func (p *manyReflectionStepsPlanner) ReflectionCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reflectCalls
}

type tokenConsumingPlanner struct {
	mu           sync.Mutex
	reflectCalls int
}

func (p *tokenConsumingPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	RecordLLMTokenUsage(ctx, 50)
	return Plan{Steps: []PlanStep{seedContextStep()}}
}

func (p *tokenConsumingPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
}

func (p *tokenConsumingPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	p.mu.Lock()
	p.reflectCalls++
	p.mu.Unlock()
	return ReflectionResult{ShouldContinue: false}, nil
}

func (p *tokenConsumingPlanner) ReflectionCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reflectCalls
}

type timeoutReflectionPlanner struct {
	mu           sync.Mutex
	reflectCalls int
}

func (p *timeoutReflectionPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	return Plan{Steps: []PlanStep{seedContextStep()}}
}

func (p *timeoutReflectionPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
}

func (p *timeoutReflectionPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	p.mu.Lock()
	p.reflectCalls++
	p.mu.Unlock()
	<-ctx.Done()
	return ReflectionResult{}, ctx.Err()
}

func (p *timeoutReflectionPlanner) ReflectionCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reflectCalls
}

type countingRetriever struct {
	mu    sync.Mutex
	calls int
}

func (r *countingRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]RunbookHit, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return []RunbookHit{{Title: query, Content: "test runbook", Score: 1}}, nil
}

func (r *countingRetriever) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type seedContextTool struct{}

func (seedContextTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "test.seed_context", Description: "seed test diagnostic context", RiskLevel: "low", ReadOnly: true}
}

func (seedContextTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	diagCtx := &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api-0"}},
	}
	return ToolResult{
		ToolName:    "test.seed_context",
		Success:     true,
		Observation: "context seeded",
		StateDelta:  &ToolStateDelta{DiagnosticContext: diagCtx},
	}
}

func seedContextStep() PlanStep {
	return PlanStep{ID: "seed-context", ToolName: "test.seed_context", Reason: "seed runtime test context"}
}

func countDecisionReason(steps []model.AgentStep, reason string) int {
	count := 0
	for _, step := range steps {
		if step.Stage == StageDecision && step.ReasoningSummary == reason {
			count++
		}
	}
	return count
}
