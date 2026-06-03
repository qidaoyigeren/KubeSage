package repository

import (
	"context"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type AgentRepository struct {
	db *gorm.DB
}

// NewAgentRepository creates the database access layer for agent runtime rows.
func NewAgentRepository(db *gorm.DB) *AgentRepository {
	return &AgentRepository{db: db}
}

// CreateStep appends one auditable agent timeline step.
func (r *AgentRepository) CreateStep(ctx context.Context, step *model.AgentStep) error {
	return r.db.WithContext(ctx).Create(step).Error
}

// CreateHypotheses stores the final hypothesis set for one diagnosis task.
func (r *AgentRepository) CreateHypotheses(ctx context.Context, hypotheses []model.Hypothesis) error {
	if len(hypotheses) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&hypotheses).Error
}

// CreateRemediationExecutions stores policy-vetted remediation execution rows.
func (r *AgentRepository) CreateRemediationExecutions(ctx context.Context, executions []model.RemediationExecution) error {
	if len(executions) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&executions).Error
}

// ListStepsByTaskID loads the timeline for an agent diagnosis task.
func (r *AgentRepository) ListStepsByTaskID(ctx context.Context, taskID uint) ([]model.AgentStep, error) {
	var steps []model.AgentStep
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("step_index asc, id asc").Find(&steps).Error
	return steps, err
}

// ListHypothesesByTaskID loads the final hypothesis set for a task.
func (r *AgentRepository) ListHypothesesByTaskID(ctx context.Context, taskID uint) ([]model.Hypothesis, error) {
	var hypotheses []model.Hypothesis
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("confidence_score desc, id asc").Find(&hypotheses).Error
	return hypotheses, err
}

// ListRemediationExecutionsByTaskID loads remediation lifecycle rows for a task.
func (r *AgentRepository) ListRemediationExecutionsByTaskID(ctx context.Context, taskID uint) ([]model.RemediationExecution, error) {
	var executions []model.RemediationExecution
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("id asc").Find(&executions).Error
	return executions, err
}

// ListPendingApprovals returns remediation executions with status pending_approval.
func (r *AgentRepository) ListPendingApprovals(ctx context.Context) ([]model.RemediationExecution, error) {
	var executions []model.RemediationExecution
	err := r.db.WithContext(ctx).Where("status = ?", model.RemediationExecutionStatusPendingApproval).Order("created_at DESC").Find(&executions).Error
	return executions, err
}

// DryRunVerifier validates a remediation command preview during approval.
// It returns the verification status and output string.
type DryRunVerifier func(taskID uint, commandPreview, riskLevel string) (status, output string)

// ApproveRemediation records that an operator acknowledged the proposal.
// When a verifier is provided, the command is validated through a dry-run
// policy check before the status is finalized. Without a verifier, the
// approval is recorded as a manual acknowledgement.
func (r *AgentRepository) ApproveRemediation(ctx context.Context, executionID uint, actor string, verifier DryRunVerifier) error {
	if verifier == nil {
		return r.db.WithContext(ctx).Model(&model.RemediationExecution{}).
			Where("id = ? AND status = ?", executionID, model.RemediationExecutionStatusPendingApproval).
			Updates(map[string]interface{}{
				"status":         model.RemediationExecutionStatusManualAck,
				"approval_by":    actor,
				"dry_run_output": "manual acknowledgement recorded; no automated dry-run or execution was performed",
			}).Error
	}

	// Load the execution to get command preview and risk level for verification.
	var execution model.RemediationExecution
	if err := r.db.WithContext(ctx).Where("id = ? AND status = ?", executionID, model.RemediationExecutionStatusPendingApproval).First(&execution).Error; err != nil {
		return err
	}

	status, output := verifier(execution.TaskID, execution.CommandPreview, execution.RiskLevel)
	return r.db.WithContext(ctx).Model(&model.RemediationExecution{}).
		Where("id = ?", executionID).
		Updates(map[string]interface{}{
			"status":         status,
			"approval_by":    actor,
			"dry_run_output": output,
		}).Error
}

// RejectRemediation updates a remediation execution to blocked status.
func (r *AgentRepository) RejectRemediation(ctx context.Context, executionID uint, actor, reason string) error {
	return r.db.WithContext(ctx).Model(&model.RemediationExecution{}).
		Where("id = ? AND status = ?", executionID, model.RemediationExecutionStatusPendingApproval).
		Updates(map[string]interface{}{
			"status":      model.RemediationExecutionStatusBlocked,
			"approval_by": actor,
		}).Error
}
