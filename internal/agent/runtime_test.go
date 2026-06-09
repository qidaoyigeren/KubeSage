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
	"k8s.io/apimachinery/pkg/util/intstr"
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

func TestRuntimeToolDeadlineDoesNotHang(t *testing.T) {
	store := &memoryStore{}
	registry := NewToolRegistry()
	if err := registry.Register(blockingTool{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{Store: store, Registry: registry, Analyzer: fakeAnalyzer{report: baseReport("OOMKilled")}, Policy: NewRemediationPolicy(true)})
	start := time.Now()
	result, err := runtime.Run(context.Background(), RuntimeOptions{TaskID: 1, MaxSteps: 1, ToolTimeout: 20 * time.Millisecond, Goal: Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled"}})
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("runtime did not respect tool deadline")
	}
	if err == nil {
		t.Fatalf("expected critical timeout error")
	}
	if result == nil || result.StopReason != StopReasonCriticalToolFailed {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !hasFailedTool(store.steps, "k8s.get_pod") {
		t.Fatalf("expected failed timeout step")
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

func TestRuntimeDoesNotConfirmOOMWhenCurveEvidenceIsMissing(t *testing.T) {
	_, result, err := runRuntimeForTest(
		t,
		Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled", IncludeLogs: true, IncludeMetrics: true},
		fakeRetriever{},
		oomLimitContext(),
		fakeAnalyzer{report: baseReport("OOMKilled")},
		12,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonNoEffectiveTool {
		t.Fatalf("expected inconclusive OOM evidence to stop without confirmation, got %s", result.StopReason)
	}
}

func TestRuntimeCollectsDistinguishingEvidenceForCloseHighConfidenceHypotheses(t *testing.T) {
	store := &memoryStore{}
	registry := NewToolRegistry()
	if err := registry.Register(ambiguousRootTool{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(eventEvidenceTool{}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("NodeNotReady")},
		Policy:   NewRemediationPolicy(true),
		Planner:  singlePodPlanner{},
	})
	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    2,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "NodeNotReady"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = result
	if !hasSuccessfulTool(store.steps, "k8s.get_events") {
		t.Fatalf("expected distinguishing event evidence collection, got %#v", store.steps)
	}
}

func TestRuntimeForcesProbeEvidenceBeforeReflectionStop(t *testing.T) {
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			return probeNotReadyContext(), nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("ProbeFailed")},
		Policy:   NewRemediationPolicy(true),
		Planner:  stopAfterPodPlanner{},
	})
	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    12,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.FaultType != "ProbeFailed" {
		t.Fatalf("unexpected fault type: %s", result.Report.FaultType)
	}
	for _, tool := range []string{"k8s.get_events", "k8s.get_logs"} {
		if !hasSuccessfulTool(store.steps, tool) {
			t.Fatalf("expected forced %s before analyzer, got %#v", tool, store.steps)
		}
	}
}

func TestRuntimeAddsStateDrivenEvidenceForMinimalCrashLoopPlan(t *testing.T) {
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			diagCtx := crashLoopContext()
			if !goal.IncludeEvents {
				diagCtx.Events = nil
			}
			if !goal.IncludeLogs {
				diagCtx.Logs = nil
			}
			return diagCtx, nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("CrashLoopBackOff")},
		Policy:   NewRemediationPolicy(true),
		Planner:  stopAfterPodPlanner{},
	})
	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    12,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopReasonConfirmedHypothesis && result.StopReason != stopReasonLLMReflectionComplete {
		t.Fatalf("expected state-driven evidence to enable convergence, got %s", result.StopReason)
	}
	for _, tool := range []string{"k8s.get_events", "k8s.get_logs"} {
		if !hasSuccessfulTool(store.steps, tool) {
			t.Fatalf("expected state-driven %s collection, got %#v", tool, store.steps)
		}
	}
}

func TestRuntimeDoesNotLetReflectionStopSkipPlannedEvidence(t *testing.T) {
	store := &memoryStore{}
	registry := NewToolRegistry()
	for _, tool := range []Tool{highConfigPodTool{}, logEvidenceTool{}, configEventEvidenceTool{}} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("CrashLoopBackOff")},
		Policy:   NewRemediationPolicy(true),
		Planner:  stopWithPlannedEvidencePlanner{},
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
	if result.StopReason != stopReasonLLMReflectionComplete {
		t.Fatalf("expected LLM reflection stop after planned evidence, got %s", result.StopReason)
	}
	for _, tool := range []string{"k8s.get_logs", "k8s.get_events"} {
		if !hasSuccessfulTool(store.steps, tool) {
			t.Fatalf("expected planned %s collection before reflection stop, got %#v", tool, store.steps)
		}
	}
}

func TestRuntimeUsesLLMReflectionStopWhenEvidenceIsExhausted(t *testing.T) {
	store := &memoryStore{}
	registry := NewToolRegistry()
	for _, tool := range []Tool{highConfigPodTool{}, logEvidenceTool{}} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("CrashLoopBackOff")},
		Policy:   NewRemediationPolicy(true),
		Planner:  stopAfterPodPlanner{},
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
	if result.StopReason != stopReasonLLMReflectionComplete {
		t.Fatalf("expected exhausted evidence to stop via LLM reflection, got %s", result.StopReason)
	}
	if !hasSuccessfulTool(store.steps, "k8s.get_logs") {
		t.Fatalf("expected state-driven log collection before LLM reflection stop, got %#v", store.steps)
	}
}

func TestRuntimePrioritizesMissingProbeLogsOverReflectiveEventLoop(t *testing.T) {
	store := &memoryStore{}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			return probeNotReadyContext(), nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: registry,
		Analyzer: fakeAnalyzer{report: baseReport("ProbeFailed")},
		Policy:   NewRemediationPolicy(true),
		Planner:  eventLoopPlanner{},
	})
	_, err = runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    6,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSuccessfulTool(store.steps, "k8s.get_logs") {
		t.Fatalf("expected missing probe logs to run before repeated event follow-ups, got %#v", store.steps)
	}
	if countSuccessfulTool(store.steps, "k8s.get_events") != 1 {
		t.Fatalf("expected redundant event follow-ups to be skipped, got %#v", store.steps)
	}
}

func TestRuntimeExecutesLLMConfigFollowupsAfterPreAnalysisLock(t *testing.T) {
	store := &memoryStore{}
	diagCtx := crashLoopContext()
	diagCtx.Logs = []diagnostic.ContainerLogs{{
		ContainerName: "app",
		Previous:      "fatal: config file not found at /etc/app/config.yaml",
	}}
	diagCtx.Pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}
	diagCtx.Pod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
	}
	diagCtx.Pod.Spec.Volumes = []corev1.Volume{{
		Name: "config",
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
		}},
	}}
	diagCtx.Pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name: "config", MountPath: "/etc/app",
	}}
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			_ = goal
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
		Planner:  configFollowupPlanner{},
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
	if result.PreAnalysis == nil {
		t.Fatal("expected CrashLoop pre-analysis lock")
	}
	if !hasSuccessfulTool(store.steps, "k8s.get_config_refs") {
		t.Fatalf("expected LLM config-reference follow-up, got %#v", store.steps)
	}
	if !hasSuccessfulTool(store.steps, "runbook.search") {
		t.Fatalf("expected LLM runbook follow-up, got %#v", store.steps)
	}
	foundConfigRef := false
	for _, evidence := range result.Report.Evidences {
		if evidence.SourceType == "k8s_config_ref" && strings.Contains(evidence.Content, "mountPath=/etc/app") {
			foundConfigRef = true
			break
		}
	}
	if !foundConfigRef {
		t.Fatalf("expected mounted ConfigMap evidence in final report, got %#v", result.Report.Evidences)
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

func oomLimitContext() *diagnostic.DiagnosticContext {
	ctx := oomContext()
	ctx.Logs = []diagnostic.ContainerLogs{{ContainerName: "app", Previous: "fatal oom memory working set limit exceeded"}}
	return ctx
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

func probeNotReadyContext() *diagnostic.DiagnosticContext {
	return &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "app",
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8080)},
				}},
			}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse,
				}},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "app",
					Ready: false,
				}},
			},
		},
		Events: []corev1.Event{{
			Reason:  "Unhealthy",
			Message: "Readiness probe failed: HTTP probe failed with statuscode: 404",
		}},
		Logs: []diagnostic.ContainerLogs{{ContainerName: "app", Current: "GET /healthz returned 404"}},
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
	return ToolResult{
		ToolName:    "k8s.get_pod",
		Success:     true,
		Observation: "confirmed memory evidence",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "prometheus",
			Title:      "OOM memory working set near limit",
			Content:    "oom memory working set limit prometheus memoryPattern=memory_limit_too_low",
			Severity:   "critical",
			Timestamp:  time.Now(),
		}},
		StateDelta: &ToolStateDelta{DiagnosticContext: oomContext()},
	}
}

type blockingTool struct{}

func (blockingTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_pod", Description: "blocking tool", RiskLevel: "low", ReadOnly: true, Critical: true}
}

func (blockingTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	_ = state
	select {}
}

type stopAfterPodPlanner struct{}

func (stopAfterPodPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	_ = ctx
	_ = tools
	return Plan{Steps: []PlanStep{{
		ID:       "pod",
		ToolName: "k8s.get_pod",
		Input:    map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName},
		Critical: true,
		Reason:   "minimal LLM plan",
	}}}
}

func (stopAfterPodPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	_ = plan
	_ = state
	_ = last
	_ = hypotheses
}

func (stopAfterPodPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	_ = ctx
	_ = plan
	_ = state
	_ = hypotheses
	return ReflectionResult{ShouldContinue: false, Reason: "LLM thinks pod snapshot is enough"}, nil
}

type eventLoopPlanner struct{}

func (eventLoopPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	return stopAfterPodPlanner{}.BuildInitialPlan(ctx, goal, tools)
}

func (eventLoopPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	_ = plan
	_ = state
	_ = last
	_ = hypotheses
}

func (eventLoopPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	_ = ctx
	_ = plan
	_ = state
	_ = hypotheses
	return ReflectionResult{
		ShouldContinue: true,
		Reason:         "keep collecting events",
		NewSteps: []PlanStep{{
			ID:       "reflect-events",
			ToolName: "k8s.get_events",
			Reason:   "extra event check",
		}},
	}, nil
}

type configFollowupPlanner struct{}

func (configFollowupPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	return stopAfterPodPlanner{}.BuildInitialPlan(ctx, goal, tools)
}

func (configFollowupPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	_ = plan
	_ = state
	_ = last
	_ = hypotheses
}

func (configFollowupPlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	_ = ctx
	_ = plan
	_ = hypotheses
	for _, evidence := range state.EvidenceSnapshot() {
		if !strings.Contains(strings.ToLower(evidence.Content), "config file not found") {
			continue
		}
		return ReflectionResult{
			ShouldContinue: true,
			Reason:         "inspect configuration references and a matching runbook",
			NewSteps: []PlanStep{
				{
					ToolName: "k8s.get_config_refs",
					Input:    map[string]interface{}{"container_name": "app"},
					Reason:   "verify ConfigMap/Secret references and mount paths for the failing container",
				},
				{
					ToolName: "runbook.search",
					Input:    map[string]interface{}{"fault_type": "CrashLoopBackOff", "query": "configmap missing config file not found"},
					Reason:   "search a specific startup configuration failure pattern",
				},
			},
		}, nil
	}
	return ReflectionResult{ShouldContinue: true, Reason: "wait for startup logs"}, nil
}

type stopWithPlannedEvidencePlanner struct{}

func (stopWithPlannedEvidencePlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	_ = ctx
	_ = tools
	return Plan{Steps: []PlanStep{
		{
			ID:       "pod",
			ToolName: "k8s.get_pod",
			Input:    map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName},
			Critical: true,
			Reason:   "read pod status",
		},
		{
			ID:            "logs",
			ToolName:      "k8s.get_logs",
			Input:         map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName},
			Reason:        "collect startup logs",
			ParallelGroup: "evidence",
		},
		{
			ID:            "events",
			ToolName:      "k8s.get_events",
			Input:         map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName},
			Reason:        "collect restart events",
			ParallelGroup: "evidence",
		},
	}}
}

func (stopWithPlannedEvidencePlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	_ = plan
	_ = state
	_ = last
	_ = hypotheses
}

func (stopWithPlannedEvidencePlanner) Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error) {
	_ = ctx
	_ = plan
	_ = state
	_ = hypotheses
	return ReflectionResult{ShouldContinue: false, Reason: "LLM thinks pod snapshot is enough"}, nil
}

type singlePodPlanner struct{}

func (singlePodPlanner) BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan {
	_ = ctx
	_ = tools
	return Plan{Steps: []PlanStep{{
		ID:       "pod",
		ToolName: "k8s.get_pod",
		Input:    map[string]interface{}{"namespace": goal.Namespace, "pod_name": goal.PodName},
		Critical: true,
		Reason:   "minimal pod snapshot",
	}}}
}

func (singlePodPlanner) AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore) {
	_ = plan
	_ = state
	_ = last
	_ = hypotheses
}

type ambiguousRootTool struct{}

func (ambiguousRootTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_pod", Description: "ambiguous node evidence", RiskLevel: "low", ReadOnly: true, Critical: true}
}

func (ambiguousRootTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	diagCtx := &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       testPod("Error"),
	}
	return ToolResult{
		ToolName:    "k8s.get_pod",
		Success:     true,
		Observation: "node evidence is ambiguous",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "Evicted pod on NotReady node",
			Content:    "evicted eviction nodenotready notready memorypressure",
			Severity:   "critical",
			Timestamp:  time.Now(),
		}},
		StateDelta: &ToolStateDelta{DiagnosticContext: diagCtx},
	}
}

type eventEvidenceTool struct{}

func (eventEvidenceTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_events", Description: "distinguishing events", RiskLevel: "low", ReadOnly: true}
}

func (eventEvidenceTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	_ = state
	return ToolResult{
		ToolName:    "k8s.get_events",
		Success:     true,
		Observation: "collected node pressure and eviction events",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_event",
			Title:      "Node pressure events",
			Content:    "eviction event memorypressure node notready",
			Severity:   "warning",
			Timestamp:  time.Now(),
		}},
	}
}

type highConfigPodTool struct{}

func (highConfigPodTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_pod", Description: "high confidence config evidence", RiskLevel: "low", ReadOnly: true, Critical: true}
}

func (highConfigPodTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	diagCtx := &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       testPod("Error"),
	}
	return ToolResult{
		ToolName:    "k8s.get_pod",
		Success:     true,
		Observation: "pod shows crashloop config failure",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      "CrashLoop config evidence",
			Content:    "crashloop backoff missing config file",
			Severity:   "warning",
			Timestamp:  time.Now(),
		}},
		StateDelta: &ToolStateDelta{DiagnosticContext: diagCtx},
	}
}

type logEvidenceTool struct{}

func (logEvidenceTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_logs", Description: "startup logs", RiskLevel: "low", ReadOnly: true}
}

func (logEvidenceTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	_ = state
	return ToolResult{
		ToolName:    "k8s.get_logs",
		Success:     true,
		Observation: "collected previous logs",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_log",
			Title:      "Previous startup log",
			Content:    "fatal missing config file",
			Severity:   "warning",
			Timestamp:  time.Now(),
		}},
	}
}

type configEventEvidenceTool struct{}

func (configEventEvidenceTool) Metadata() ToolMetadata {
	return ToolMetadata{Name: "k8s.get_events", Description: "config restart events", RiskLevel: "low", ReadOnly: true}
}

func (configEventEvidenceTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	_ = ctx
	_ = input
	_ = state
	return ToolResult{
		ToolName:    "k8s.get_events",
		Success:     true,
		Observation: "collected backoff config events",
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_event",
			Title:      "BackOff",
			Content:    "Back-off restarting failed container after missing config",
			Severity:   "warning",
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

func hasSuccessfulTool(steps []model.AgentStep, tool string) bool {
	for _, step := range steps {
		if step.Stage == StageToolCall && step.ToolName == tool && step.Status == model.AgentStepStatusSuccess {
			return true
		}
	}
	return false
}

func countSuccessfulTool(steps []model.AgentStep, tool string) int {
	count := 0
	for _, step := range steps {
		if step.Stage == StageToolCall && step.ToolName == tool && step.Status == model.AgentStepStatusSuccess {
			count++
		}
	}
	return count
}
