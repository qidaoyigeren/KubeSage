package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
	"kubesage/internal/observability"
)

var forbiddenCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(ns|namespace|namespaces)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(pvc|persistentvolumeclaim|persistentvolumeclaims)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(pv|persistentvolume|persistentvolumes)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+secret\b`),
	regexp.MustCompile(`(?i)\bdelete\s+database\b`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
	regexp.MustCompile(`(?i)\btruncate\s+table\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+scale\s+(deployment|statefulset)[^\n]*--replicas\s*=\s*0\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+patch\s+storageclass\b`),
}

type RemediationPolicy struct {
	enableDryRunPreview bool
}

// NewRemediationPolicy creates the safety policy used for all remediation
// actions before they are returned or stored.
func NewRemediationPolicy(enableDryRunPreview bool) *RemediationPolicy {
	return &RemediationPolicy{enableDryRunPreview: enableDryRunPreview}
}

// SanitizeActions normalizes action risk, blocks forbidden commands, and emits
// the MVP remediation execution state for each proposal.
func (p *RemediationPolicy) SanitizeActions(taskID uint, actions []diagnostic.RemediationAction) ([]diagnostic.RemediationAction, []model.RemediationExecution) {
	sanitized := make([]diagnostic.RemediationAction, 0, len(actions))
	executions := make([]model.RemediationExecution, 0, len(actions))
	seen := map[string]struct{}{}
	for i, action := range actions {
		action.ActionID = ensureActionID(action, i)
		action.RiskLevel = normalizeRisk(action.RiskLevel)
		action.Executable = false
		if action.RiskLevel == "medium" || action.RiskLevel == "high" {
			action.NeedHumanConfirm = true
		}
		key := action.ActionType + "\x00" + action.CommandPreview
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		execution := model.RemediationExecution{
			TaskID:         taskID,
			ActionID:       action.ActionID,
			RiskLevel:      action.RiskLevel,
			CommandPreview: action.CommandPreview,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		switch {
		case action.ActionType == "" || action.RiskLevel == "forbidden" || forbiddenCommand(action.CommandPreview):
			execution.Status = model.RemediationExecutionStatusBlocked
			execution.DryRunOutput = "blocked by remediation policy"
			observability.IncRemediationActionTotal(action.RiskLevel, execution.Status)
			executions = append(executions, execution)
			continue
		case action.RiskLevel == "high":
			execution.Status = model.RemediationExecutionStatusProposed
		case action.RiskLevel == "medium":
			execution.Status = model.RemediationExecutionStatusPendingApproval
		default:
			execution.Status = lowRiskExecutionStatus(p.enableDryRunPreview, action.CommandPreview)
			execution.DryRunOutput = dryRunPreviewOutput(execution.Status, action.CommandPreview)
		}
		observability.IncRemediationActionTotal(action.RiskLevel, execution.Status)
		executions = append(executions, execution)
		sanitized = append(sanitized, action)
	}
	return sanitized, executions
}

// ValidateDryRunPreview validates a command preview without invoking a shell or
// Kubernetes API. It is a policy gate, not real execution.
func (p *RemediationPolicy) ValidateDryRunPreview(taskID uint, commandPreview, riskLevel string) model.RemediationExecution {
	risk := normalizeRisk(riskLevel)
	status := model.RemediationExecutionStatusDryRunPending
	output := "dry-run preview is pending approval"
	if !p.enableDryRunPreview {
		status = model.RemediationExecutionStatusDryRunFailed
		output = "dry-run preview is disabled"
	} else if forbiddenCommand(commandPreview) {
		status = model.RemediationExecutionStatusBlocked
		output = "blocked by remediation policy"
	} else if strings.TrimSpace(commandPreview) == "" {
		status = model.RemediationExecutionStatusDryRunFailed
		output = "empty command preview"
	} else if risk == "high" {
		status = model.RemediationExecutionStatusProposed
		output = "high-risk actions are proposal-only in MVP"
	} else if readOnlyKubectlCommand(commandPreview) {
		status = model.RemediationExecutionStatusDryRunSuccess
		output = "read-only command preview accepted; shell execution was not performed"
	} else if risk == "medium" && !strings.Contains(commandPreview, "--dry-run=server") {
		status = model.RemediationExecutionStatusPendingApproval
		output = "medium-risk command requires human approval and server dry-run preview"
	} else {
		status = model.RemediationExecutionStatusDryRunSuccess
		output = "policy validation passed; shell execution was not performed"
	}
	execution := model.RemediationExecution{
		TaskID:         taskID,
		ActionID:       "dry-run-preview-" + uuid.NewString(),
		Status:         status,
		RiskLevel:      risk,
		CommandPreview: commandPreview,
		DryRunOutput:   output,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	observability.IncRemediationActionTotal(risk, status)
	return execution
}

func lowRiskExecutionStatus(enabled bool, command string) string {
	if !enabled {
		return model.RemediationExecutionStatusProposed
	}
	if strings.Contains(command, "--dry-run=server") || readOnlyKubectlCommand(command) {
		return model.RemediationExecutionStatusDryRunSuccess
	}
	return model.RemediationExecutionStatusProposed
}

func dryRunPreviewOutput(status, command string) string {
	if status == model.RemediationExecutionStatusDryRunSuccess {
		if readOnlyKubectlCommand(command) && !strings.Contains(command, "--dry-run=server") {
			return fmt.Sprintf("read-only command preview accepted; shell execution was not performed: %s", command)
		}
		return fmt.Sprintf("policy validation passed for dry-run preview: %s", command)
	}
	return ""
}

func errorForExecution(execution model.RemediationExecution) string {
	switch execution.Status {
	case model.RemediationExecutionStatusBlocked, model.RemediationExecutionStatusDryRunFailed:
		return execution.DryRunOutput
	default:
		return ""
	}
}

func ensureActionID(action diagnostic.RemediationAction, index int) string {
	if strings.TrimSpace(action.ActionID) != "" {
		return strings.TrimSpace(action.ActionID)
	}
	base := strings.Trim(action.ActionType, "_- ")
	if base == "" {
		base = "action"
	}
	return fmt.Sprintf("%s-%d", base, index+1)
}

func forbiddenCommand(command string) bool {
	for _, pattern := range forbiddenCommandPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func readOnlyKubectlCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if !strings.HasPrefix(command, "kubectl ") {
		return false
	}
	for _, verb := range []string{" get ", " describe ", " logs ", " top ", " auth can-i "} {
		if strings.Contains(" "+command+" ", verb) {
			return true
		}
	}
	return false
}

func normalizeRisk(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "low", "medium", "high", "forbidden":
		return strings.ToLower(strings.TrimSpace(risk))
	default:
		return "medium"
	}
}
