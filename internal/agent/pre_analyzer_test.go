package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	"kubesage/internal/model"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTargetedEvidenceRequestsByFaultType(t *testing.T) {
	pvcPod := minimalPod()
	pvcPod.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
		},
	}}
	tests := []struct {
		name  string
		fault string
		pod   *corev1.Pod
		want  []string
	}{
		{name: "oom", fault: "OOMKilled", pod: minimalPod(), want: []string{"k8s.get_logs", "prometheus.query_range", "k8s.get_topology"}},
		{name: "crash loop", fault: "CrashLoopBackOff", pod: minimalPod(), want: []string{"k8s.get_logs", "k8s.get_events", "k8s.get_topology"}},
		{name: "image pull", fault: "ImagePullBackOff", pod: minimalPod(), want: []string{"k8s.get_events"}},
		{name: "init error", fault: "InitError", pod: minimalPod(), want: []string{"k8s.get_logs", "k8s.get_events"}},
		{name: "evicted", fault: "Evicted", pod: minimalPod(), want: []string{"k8s.get_events", "k8s.get_topology", "prometheus.query_range"}},
		{name: "pending with pvc", fault: "PodPending", pod: pvcPod, want: []string{"k8s.get_events", "k8s.get_pvc", "k8s.get_topology"}},
		{name: "probe", fault: "ProbeFailed", pod: minimalPod(), want: []string{"k8s.get_events", "k8s.get_logs", "k8s.get_topology"}},
		{name: "node not ready", fault: "NodeNotReady", pod: minimalPod(), want: []string{"k8s.get_topology", "k8s.get_events"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ToolState{
				Goal:              Goal{Namespace: "default", PodName: "api-0"},
				DiagnosticContext: &diagnostic.DiagnosticContext{Pod: tt.pod},
			}
			requests := targetedEvidenceRequests(tt.fault, state)
			got := make([]string, 0, len(requests))
			for _, request := range requests {
				got = append(got, request.tool)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("targeted tools for %s = %v, want %v", tt.fault, got, tt.want)
			}
		})
	}
}

func TestPreAnalysisRejectsOOMLabelWithoutStructuredOOMReason(t *testing.T) {
	pod := minimalPod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:         "app",
		RestartCount: 1,
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "Error",
			ExitCode: 137,
		}},
	}}
	state := &ToolState{
		Goal: Goal{Namespace: "default", PodName: "api-0"},
		DiagnosticContext: &diagnostic.DiagnosticContext{
			Pod:  pod,
			Logs: []diagnostic.ContainerLogs{{ContainerName: "app", Previous: "memory allocation failed"}},
		},
	}
	report := authoritativeReport("OOMKilled", "oomkilled")
	if decision := buildPreAnalysisDecision(report, state); decision != nil {
		t.Fatalf("exitCode 137 plus memory text must not lock OOMKilled: %#v", decision)
	}

	pod.Status.ContainerStatuses[0].LastTerminationState.Terminated.Reason = "OOMKilled"
	if decision := buildPreAnalysisDecision(report, state); decision == nil || decision.FaultType != "OOMKilled" {
		t.Fatalf("structured OOMKilled reason should lock the fault, got %#v", decision)
	}
}

func TestApplyTargetedEvidencePlanSkipsUnrelatedSteps(t *testing.T) {
	state := &ToolState{
		Goal:              Goal{Namespace: "default", PodName: "api-0"},
		DiagnosticContext: evictedDiagnosticContext(),
		AvailableTools: []ToolMetadata{
			{Name: "k8s.get_pod"},
			{Name: "k8s.get_events"},
			{Name: "k8s.get_logs"},
			{Name: "k8s.get_pvc"},
			{Name: "k8s.get_topology"},
			{Name: "prometheus.query_range"},
			{Name: "runbook.search"},
		},
		CompletedTools: map[string]bool{"k8s.get_pod": true},
	}
	plan := Plan{Steps: []PlanStep{
		{ID: "pod", ToolName: "k8s.get_pod", Completed: true},
		{ID: "logs", ToolName: "k8s.get_logs"},
		{ID: "pvc", ToolName: "k8s.get_pvc"},
		{ID: "runbook", ToolName: "runbook.search"},
	}}
	decision := &PreAnalysisDecision{FaultType: "Evicted"}
	rewrite := applyTargetedEvidencePlan(&plan, state, decision)
	if !rewrite.changed {
		t.Fatal("expected the generic plan to be rewritten")
	}
	for _, tool := range []string{"k8s.get_logs", "k8s.get_pvc"} {
		if planHasPendingTool(&plan, tool) {
			t.Fatalf("unrelated tool %s remained runnable", tool)
		}
	}
	for _, tool := range []string{"k8s.get_events", "k8s.get_topology", "prometheus.query_range"} {
		if !planHasPendingTool(&plan, tool) {
			t.Fatalf("targeted tool %s was not scheduled: %#v", tool, plan.Steps)
		}
	}
	ids := map[string]bool{}
	for _, step := range plan.Steps {
		if ids[step.ID] {
			t.Fatalf("duplicate plan step id %q after rewrite", step.ID)
		}
		ids[step.ID] = true
	}
}

func TestRuntimePreAnalyzerBuildsEvictedEvidencePlan(t *testing.T) {
	store := &memoryStore{}
	toolRegistry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			_ = ctx
			snapshot := cloneDiagnosticContext(evictedDiagnosticContext())
			if !goal.IncludeEvents {
				snapshot.Events = nil
			}
			if !goal.IncludeLogs {
				snapshot.Logs = nil
			}
			return snapshot, nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	analyzerRegistry, err := diagnosticanalyzer.NewDefaultRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeDeps{
		Store:    store,
		Registry: toolRegistry,
		Analyzer: diagnostic.NewDiagnosisEngine(analyzerRegistry.Analyzers()...),
		Policy:   NewRemediationPolicy(true),
	})
	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      1,
		MaxSteps:    8,
		ToolTimeout: time.Second,
		Goal:        Goal{Namespace: "default", PodName: "api-0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreAnalysis == nil || result.PreAnalysis.FaultType != "Evicted" {
		t.Fatalf("expected locked Evicted pre-analysis, got %#v", result.PreAnalysis)
	}
	for _, tool := range []string{"k8s.get_events", "k8s.get_topology", "prometheus.query_range"} {
		if !hasToolCall(store.steps, tool) {
			t.Fatalf("expected targeted %s call, got %#v", tool, store.steps)
		}
	}
	for _, tool := range []string{"k8s.get_logs", "k8s.get_pvc"} {
		if hasToolCall(store.steps, tool) {
			t.Fatalf("unexpected unrelated %s call after Evicted classification", tool)
		}
	}
	if !hasStepStage(store.steps, StagePreAnalysis) {
		t.Fatalf("expected an auditable pre-analysis stage, got %#v", store.steps)
	}
}

func authoritativeReport(faultType, analyzerName string) *diagnostic.Report {
	return &diagnostic.Report{
		FaultType:        faultType,
		RootCauseSummary: faultType + " detected from structured status",
		ConfidenceScore:  0.84,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			AnalyzerName:     analyzerName,
			FaultType:        faultType,
			Summary:          faultType + " detected from structured status",
			ConfidenceScore:  0.84,
			ContributingRole: "primary",
		},
		Evidences: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod_status",
			Title:      faultType + " structured status",
			Content:    "reason=" + faultType,
			Severity:   "critical",
			Timestamp:  time.Now(),
		}},
	}
}

func evictedDiagnosticContext() *diagnostic.DiagnosticContext {
	pod := minimalPod()
	pod.Status.Phase = corev1.PodFailed
	pod.Status.Reason = "Evicted"
	pod.Status.Message = "The node was low on resource: ephemeral-storage."
	pod.Spec.NodeName = "node-a"
	return &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       pod,
		Events: []corev1.Event{{
			Reason:  "Evicted",
			Message: "The node was low on resource: ephemeral-storage.",
		}},
		Topology: &diagnostic.TopologyInfo{
			NodeName: "node-a",
			Node: &diagnostic.NodeHealth{
				Name:         "node-a",
				Ready:        true,
				DiskPressure: true,
			},
		},
	}
}

func minimalPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func hasToolCall(steps []model.AgentStep, tool string) bool {
	for _, step := range steps {
		if step.Stage == StageToolCall && step.ToolName == tool {
			return true
		}
	}
	return false
}
