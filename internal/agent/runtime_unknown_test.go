package agent

import (
	"context"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	"kubesage/internal/model"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRuntimeFallsBackToUnknownForUnsupportedCNIFailure(t *testing.T) {
	diagCtx := unsupportedRuntimeCNIFailureContext()
	store := &memoryStore{}
	policy := NewRemediationPolicy(true)
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return diagCtx, nil
		},
		Policy: policy,
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
		Registry: registry,
		Analyzer: diagnostic.NewDiagnosisEngine(analyzerRegistry.Analyzers()...),
		Policy:   policy,
		Planner:  NewRulePlanner(),
	})

	result, err := runtime.Run(context.Background(), RuntimeOptions{
		TaskID:      101,
		MaxSteps:    20,
		ToolTimeout: time.Second,
		Goal: Goal{
			Namespace:      diagCtx.Namespace,
			PodName:        diagCtx.PodName,
			ExpectedFault:  "CNIPluginFailure",
			IncludeLogs:    true,
			IncludeEvents:  true,
			IncludeMetrics: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Report.FaultType != "unknown" {
		t.Fatalf("fault type = %q, want unknown", result.Report.FaultType)
	}
	if result.Report.ConfidenceScore != 0.3 {
		t.Fatalf("confidence = %.2f, want 0.30", result.Report.ConfidenceScore)
	}
	if !result.Report.NeedHumanConfirm {
		t.Fatal("unknown diagnosis must require human confirmation")
	}
	if len(result.Report.RemediationActions) != 0 || len(store.executions) != 0 {
		t.Fatalf("unknown diagnosis must not create remediation work: actions=%#v executions=%#v", result.Report.RemediationActions, store.executions)
	}
	for _, hypothesis := range store.hypotheses {
		if hypothesis.Status == model.HypothesisStatusConfirmed {
			t.Fatalf("unsupported failure incorrectly confirmed hypothesis %q", hypothesis.HypothesisType)
		}
	}

	t.Logf(
		"fault_type=%s confidence=%.2f stop_reason=%s steps=%d planned_tools=%v hypotheses=%d remediation_actions=%d",
		result.Report.FaultType,
		result.Report.ConfidenceScore,
		result.StopReason,
		result.StepsExecuted,
		result.PlannedToolNames,
		len(store.hypotheses),
		len(result.Report.RemediationActions),
	)
}

func unsupportedRuntimeCNIFailureContext() *diagnostic.DiagnosticContext {
	return &diagnostic.DiagnosticContext{
		Namespace: "default",
		PodName:   "network-failure",
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "network-failure",
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "example/app:latest"}},
				NodeName:   "worker-1",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "app",
					Ready: false,
				}},
			},
		},
		Events: []corev1.Event{{
			Reason:  "FailedCreatePodSandBox",
			Message: "Failed to create pod sandbox: CNI plugin failed to set up pod network",
		}},
		Logs: []diagnostic.ContainerLogs{{
			ContainerName: "app",
			Current:       "application is waiting for pod network setup",
		}},
		Topology: &diagnostic.TopologyInfo{
			NodeName: "worker-1",
			Node: &diagnostic.NodeHealth{
				Name:  "worker-1",
				Ready: true,
			},
		},
	}
}
