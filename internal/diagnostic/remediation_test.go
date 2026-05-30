package diagnostic

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestGenerateRemediationActionsForOOM verifies low-risk inspection and
// high-risk memory tuning suggestions stay advisory only.
func TestGenerateRemediationActionsForOOM(t *testing.T) {
	ctx := &DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "api"}},
			},
		},
		Topology: &TopologyInfo{DeploymentName: "api"},
	}
	report := &Report{FaultType: "OOMKilled"}

	actions := GenerateRemediationActions(ctx, report)
	byType := remediationActionsByType(actions)

	logAction := byType[remediationViewPreviousLogs]
	if logAction.ActionType == "" || logAction.RiskLevel != "low" || logAction.NeedHumanConfirm || logAction.Executable {
		t.Fatalf("unexpected previous log action: %+v", logAction)
	}
	if !strings.Contains(logAction.CommandPreview, "--previous") {
		t.Fatalf("expected previous logs command preview, got %q", logAction.CommandPreview)
	}

	memoryAction := byType[remediationAdjustMemoryLimit]
	if memoryAction.ActionType == "" || memoryAction.RiskLevel != "high" || !memoryAction.NeedHumanConfirm || memoryAction.Executable {
		t.Fatalf("unexpected memory action: %+v", memoryAction)
	}
	if !strings.Contains(memoryAction.CommandPreview, "--dry-run=server") {
		t.Fatalf("expected memory action dry-run preview, got %q", memoryAction.CommandPreview)
	}
}

// TestGenerateRemediationActionsForCrashLoop verifies config inspection is
// generated for startup failure scenarios.
func TestGenerateRemediationActionsForCrashLoop(t *testing.T) {
	ctx := &DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "api"}},
			},
		},
	}
	report := &Report{FaultType: "CrashLoopBackOff"}

	actions := remediationActionsByType(GenerateRemediationActions(ctx, report))
	configAction := actions[remediationCheckConfigMapSecret]
	if configAction.ActionType == "" || configAction.RiskLevel != "low" || configAction.NeedHumanConfirm || configAction.Executable {
		t.Fatalf("unexpected config action: %+v", configAction)
	}
	if !strings.Contains(configAction.CommandPreview, "configmap,secret") {
		t.Fatalf("expected configmap/secret preview, got %q", configAction.CommandPreview)
	}
}

// TestGenerateRemediationActionsForProbe verifies probe fixes use the original
// container index in dry-run patch previews.
func TestGenerateRemediationActionsForProbe(t *testing.T) {
	ctx := &DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "sidecar"},
					{
						Name: "api",
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: "/bad"},
							},
						},
					},
				},
			},
		},
		Topology: &TopologyInfo{DeploymentName: "api"},
	}
	report := &Report{FaultType: "ProbeFailed"}

	actions := GenerateRemediationActions(ctx, report)
	byType := remediationActionsByType(actions)
	delayAction := byType[remediationExtendProbeDelay]
	pathAction := byType[remediationFixProbePath]

	if delayAction.RiskLevel != "medium" || !delayAction.NeedHumanConfirm || delayAction.Executable {
		t.Fatalf("unexpected delay action: %+v", delayAction)
	}
	if !strings.Contains(delayAction.CommandPreview, "/containers/1/readinessProbe") {
		t.Fatalf("expected original container index in delay preview, got %q", delayAction.CommandPreview)
	}
	if pathAction.RiskLevel != "medium" || !pathAction.NeedHumanConfirm || pathAction.Executable {
		t.Fatalf("unexpected path action: %+v", pathAction)
	}
	if !strings.Contains(pathAction.CommandPreview, "<correct-health-path>") {
		t.Fatalf("expected placeholder health path, got %q", pathAction.CommandPreview)
	}
}

// TestGenerateRemediationActionsForPending verifies scheduler troubleshooting
// includes taint/toleration inspection.
func TestGenerateRemediationActionsForPending(t *testing.T) {
	ctx := &DiagnosticContext{
		Namespace: "default",
		PodName:   "api-0",
		Pod:       &corev1.Pod{Spec: corev1.PodSpec{NodeName: "node-a"}},
	}
	report := &Report{FaultType: "PodPending"}

	actions := remediationActionsByType(GenerateRemediationActions(ctx, report))
	taintAction := actions[remediationCheckTaintToleration]
	if taintAction.ActionType == "" || taintAction.RiskLevel != "low" || taintAction.NeedHumanConfirm || taintAction.Executable {
		t.Fatalf("unexpected taint action: %+v", taintAction)
	}
	if !strings.Contains(taintAction.CommandPreview, "describe node node-a") {
		t.Fatalf("expected node taint preview, got %q", taintAction.CommandPreview)
	}
}

// TestSanitizeRemediationActionsDropsForbiddenCommands ensures future actions
// cannot suggest destructive database, PVC, or Namespace deletion.
func TestSanitizeRemediationActionsDropsForbiddenCommands(t *testing.T) {
	actions := sanitizeRemediationActions([]RemediationAction{
		{ActionType: "danger_db", CommandPreview: "DROP DATABASE prod", RiskLevel: "high"},
		{ActionType: "danger_pvc", CommandPreview: "kubectl delete pvc data -n default", RiskLevel: "high"},
		{ActionType: "danger_ns", CommandPreview: "kubectl delete namespace default", RiskLevel: "high"},
		{ActionType: "safe", CommandPreview: "kubectl get pod -n default api-0", RiskLevel: "low"},
	})

	if len(actions) != 1 {
		t.Fatalf("expected only one safe action, got %+v", actions)
	}
	if actions[0].ActionType != "safe" || actions[0].Executable {
		t.Fatalf("unexpected sanitized action: %+v", actions[0])
	}
}

// remediationActionsByType keeps tests focused on generated action metadata.
func remediationActionsByType(actions []RemediationAction) map[string]RemediationAction {
	result := map[string]RemediationAction{}
	for _, action := range actions {
		if _, ok := result[action.ActionType]; !ok {
			result[action.ActionType] = action
		}
	}
	return result
}
