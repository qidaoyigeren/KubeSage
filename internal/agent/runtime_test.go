package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type memoryStore struct {
	steps      []model.AgentStep
	hypotheses []model.Hypothesis
	executions []model.RemediationExecution
}

func (s *memoryStore) CreateStep(ctx context.Context, step *model.AgentStep) error {
	_ = ctx
	step.ID = uint(len(s.steps) + 1)
	s.steps = append(s.steps, *step)
	return nil
}

func (s *memoryStore) CreateHypotheses(ctx context.Context, hypotheses []model.Hypothesis) error {
	_ = ctx
	s.hypotheses = append(s.hypotheses, hypotheses...)
	return nil
}

func (s *memoryStore) CreateRemediationExecutions(ctx context.Context, executions []model.RemediationExecution) error {
	_ = ctx
	s.executions = append(s.executions, executions...)
	return nil
}

type fakeAnalyzer struct {
	report *diagnostic.Report
	panic  bool
}

func (a fakeAnalyzer) Diagnose(ctx *diagnostic.DiagnosticContext) (*diagnostic.Report, error) {
	if a.panic {
		panic("analyzer exploded")
	}
	report := *a.report
	report.Namespace = ctx.Namespace
	report.PodName = ctx.PodName
	return &report, nil
}

type fakeRetriever struct {
	err error
}

func (r fakeRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]RunbookHit, error) {
	_ = ctx
	_ = faultType
	_ = query
	_ = limit
	if r.err != nil {
		return nil, r.err
	}
	return []RunbookHit{{Title: "oom / checks", Content: "check memory metrics", Score: 3}}, nil
}

func TestRuntimeOOMKilledPath(t *testing.T) {
	store, result, err := runRuntimeForTest(t, Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled", IncludeLogs: true, IncludeMetrics: true}, fakeRetriever{}, oomContext(), fakeAnalyzer{report: baseReport("OOMKilled")}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.FaultType != "OOMKilled" {
		t.Fatalf("unexpected fault type: %s", result.Report.FaultType)
	}
	if len(store.steps) == 0 || !hasStepStage(store.steps, StageReflection) {
		t.Fatalf("expected reflection steps, got %#v", store.steps)
	}
	if len(store.hypotheses) == 0 {
		t.Fatalf("expected persisted hypotheses")
	}
}

func TestRuntimeCrashLoopPath(t *testing.T) {
	_, result, err := runRuntimeForTest(t, Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "CrashLoopBackOff", IncludeLogs: true, IncludeEvents: true}, fakeRetriever{}, crashLoopContext(), fakeAnalyzer{report: baseReport("CrashLoopBackOff")}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.FaultType != "CrashLoopBackOff" {
		t.Fatalf("unexpected fault type: %s", result.Report.FaultType)
	}
	if result.StopReason == "" {
		t.Fatalf("expected stop reason")
	}
}

func TestRuntimeContinuesAfterNonCriticalToolFailure(t *testing.T) {
	store, result, err := runRuntimeForTest(t, Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "CrashLoopBackOff", IncludeLogs: true, IncludeEvents: true}, fakeRetriever{err: errors.New("runbook offline")}, crashLoopContext(), fakeAnalyzer{report: baseReport("CrashLoopBackOff")}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report == nil {
		t.Fatalf("expected final report")
	}
	if !hasFailedTool(store.steps, "runbook.search") {
		t.Fatalf("expected failed runbook step, got %#v", store.steps)
	}
}

func TestRuntimeCriticalToolFailureFailsTask(t *testing.T) {
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return nil, errors.New("pod not found")
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{Store: store, Registry: registry, Analyzer: fakeAnalyzer{report: baseReport("OOMKilled")}, Policy: NewRemediationPolicy(true)})
	result, err := runtime.Run(context.Background(), RuntimeOptions{TaskID: 1, MaxSteps: 12, ToolTimeout: time.Second, Goal: Goal{Namespace: "default", PodName: "missing", ExpectedFault: "OOMKilled"}})
	if err == nil {
		t.Fatalf("expected critical tool error")
	}
	if result == nil || result.StopReason != StopReasonCriticalToolFailed {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !hasFailedTool(store.steps, "k8s.get_pod") {
		t.Fatalf("expected failed critical tool step")
	}
}

func TestRuntimeRecoversPanic(t *testing.T) {
	_, _, err := runRuntimeForTest(t, Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled"}, fakeRetriever{}, oomContext(), fakeAnalyzer{report: baseReport("OOMKilled"), panic: true}, 12)
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic recovery error, got %v", err)
	}
}

func TestRuntimeMaxStepsStop(t *testing.T) {
	_, result, err := runRuntimeForTest(t, Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled"}, fakeRetriever{}, oomContext(), fakeAnalyzer{report: baseReport("OOMKilled")}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonMaxSteps {
		t.Fatalf("unexpected stop reason: %s", result.StopReason)
	}
}

func TestRuntimeConfirmedHypothesisEarlyStop(t *testing.T) {
	store := &memoryStore{}
	registry := NewToolRegistry()
	if err := registry.Register(confirmedTool{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{Store: store, Registry: registry, Analyzer: fakeAnalyzer{report: baseReport("OOMKilled")}, Policy: NewRemediationPolicy(true)})
	result, err := runtime.Run(context.Background(), RuntimeOptions{TaskID: 1, MaxSteps: 12, ToolTimeout: time.Second, Goal: Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonConfirmedHypothesis {
		t.Fatalf("unexpected stop reason: %s", result.StopReason)
	}
}

func runRuntimeForTest(t *testing.T, goal Goal, retriever Retriever, diagCtx *diagnostic.DiagnosticContext, analyzer Analyzer, maxSteps int) (*memoryStore, *RunResult, error) {
	t.Helper()
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			return diagCtx, nil
		},
		Retriever: retriever,
		Policy:    NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{Store: store, Registry: registry, Analyzer: analyzer, Policy: NewRemediationPolicy(true)})
	result, err := runtime.Run(context.Background(), RuntimeOptions{TaskID: 1, MaxSteps: maxSteps, ToolTimeout: time.Second, Goal: goal})
	return store, result, err
}

func baseReport(faultType string) *diagnostic.Report {
	return &diagnostic.Report{
		FaultType:        faultType,
		RootCauseSummary: faultType + " detected",
		ConfidenceScore:  0.82,
		Evidences: []diagnostic.EvidenceRecord{{
			SourceType: "rule",
			Title:      faultType,
			Content:    faultType + " evidence",
			Severity:   "warning",
			Timestamp:  time.Now(),
		}},
	}
}

func oomContext() *diagnostic.DiagnosticContext {
	return &diagnostic.DiagnosticContext{
		Namespace:      "default",
		PodName:        "api-0",
		Pod:            testPod("OOMKilled"),
		Logs:           []diagnostic.ContainerLogs{{ContainerName: "app", Previous: "fatal oom memory allocation failed"}},
		MetricsEnabled: true,
	}
}

func crashLoopContext() *diagnostic.DiagnosticContext {
	return &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       testPod("Error"),
		Logs:      []diagnostic.ContainerLogs{{ContainerName: "app", Previous: "config file missing connection refused"}},
		Events:    []corev1.Event{{Reason: "BackOff", Message: "Back-off restarting failed container"}},
	}
}

func testPod(reason string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				RestartCount: 2,
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason:   reason,
					ExitCode: 137,
				}},
			}},
		},
	}
}

type confirmedTool struct{}

func (confirmedTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_pod", Description: "confirmed evidence", RiskLevel: "low", ReadOnly: true, Critical: true}
}

func (confirmedTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	state.DiagnosticContext = oomContext()
	return ToolResult{
		ToolName:    "k8s.get_pod",
		Success:     true,
		Observation: "confirmed memory evidence",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "prometheus",
			Title:      "OOM memory working set near limit",
			Content:    "oom memory working set limit prometheus",
			Severity:   "critical",
			Timestamp:  time.Now(),
		}},
	}
}

func hasStepStage(steps []model.AgentStep, stage string) bool {
	for _, step := range steps {
		if step.Stage == stage {
			return true
		}
	}
	return false
}

func hasFailedTool(steps []model.AgentStep, tool string) bool {
	for _, step := range steps {
		if step.ToolName == tool && step.Status == model.AgentStepStatusFailed {
			return true
		}
	}
	return false
}
