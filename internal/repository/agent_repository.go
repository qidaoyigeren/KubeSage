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
	err := r.db.WithContext(ctx).Where("status = ?", "pending_approval").Order("created_at DESC").Find(&executions).Error
	return executions, err
}

// ApproveRemediation updates a remediation execution to approved status.
func (r *AgentRepository) ApproveRemediation(ctx context.Context, executionID uint, actor string) error {
	return r.db.WithContext(ctx).Model(&model.RemediationExecution{}).
		Where("id = ? AND status = ?", executionID, "pending_approval").
		Updates(map[string]interface{}{
			"status":      "dry_run_pending",
			"approval_by": actor,
		}).Error
}

// RejectRemediation updates a remediation execution to blocked status.
func (r *AgentRepository) RejectRemediation(ctx context.Context, executionID uint, actor, reason string) error {
	return r.db.WithContext(ctx).Model(&model.RemediationExecution{}).
		Where("id = ? AND status = ?", executionID, "pending_approval").
		Updates(map[string]interface{}{
			"status":      "blocked",
			"approval_by": actor,
		}).Error
}
