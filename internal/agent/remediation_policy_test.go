package agent

import (
	"strings"
	"testing"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestRemediationPolicyRiskStates(t *testing.T) {
	policy := NewRemediationPolicy(true)
	actions, executions := policy.SanitizeActions(7, []diagnostic.RemediationAction{
		{ActionType: "read_logs", CommandPreview: "kubectl logs -n default pod -c app", RiskLevel: "low"},
		{ActionType: "patch_probe", CommandPreview: "kubectl patch deployment/api --dry-run=server -o yaml", RiskLevel: "medium"},
		{ActionType: "raise_memory", CommandPreview: "kubectl set resources deployment/api --dry-run=server -o yaml", RiskLevel: "high"},
		{ActionType: "danger", CommandPreview: "kubectl delete pvc data", RiskLevel: "low"},
	})
	if len(actions) != 3 {
		t.Fatalf("forbidden action should be filtered, got %d actions", len(actions))
	}
	statuses := map[string]string{}
	for _, execution := range executions {
		statuses[execution.ActionID] = execution.Status
	}
	if statuses["read_logs-1"] != model.RemediationExecutionStatusDryRunSuccess {
		t.Fatalf("unexpected low-risk status: %s", statuses["read_logs-1"])
	}
	if statuses["patch_probe-2"] != model.RemediationExecutionStatusPendingApproval {
		t.Fatalf("unexpected medium-risk status: %s", statuses["patch_probe-2"])
	}
	if statuses["raise_memory-3"] != model.RemediationExecutionStatusProposed {
		t.Fatalf("unexpected high-risk status: %s", statuses["raise_memory-3"])
	}
	if statuses["danger-4"] != model.RemediationExecutionStatusBlocked {
		t.Fatalf("expected forbidden action to be blocked")
	}
}

func TestDryRunPreviewDoesNotExecuteShell(t *testing.T) {
	policy := NewRemediationPolicy(true)
	execution := policy.ValidateDryRunPreview(1, "kubectl patch deployment/api --dry-run=server -o yaml", "medium")
	if execution.Status != model.RemediationExecutionStatusDryRunSuccess {
		t.Fatalf("unexpected dry-run status: %s", execution.Status)
	}
	if !strings.Contains(execution.DryRunOutput, "shell execution was not performed") {
		t.Fatalf("dry-run preview should be validation-only, got %q", execution.DryRunOutput)
	}
	blocked := policy.ValidateDryRunPreview(1, "kubectl delete namespace prod", "low")
	if blocked.Status != model.RemediationExecutionStatusBlocked {
		t.Fatalf("expected forbidden command to be blocked, got %s", blocked.Status)
	}
}
