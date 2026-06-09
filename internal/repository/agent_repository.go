package repository

import (
	"context"
	"time"

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

// CreateMemory inserts a new diagnosis memory record. If a memory with the same
// task_id already exists, it updates the existing row atomically.
func (r *AgentRepository) CreateMemory(ctx context.Context, m *model.DiagnosisMemory) error {
	if m == nil {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.DiagnosisMemory
		err := tx.Where("task_id = ?", m.TaskID).First(&existing).Error
		if err == nil {
			// Update existing: preserve original creation time and ID.
			m.ID = existing.ID
			m.CreatedAt = existing.CreatedAt
			return tx.Save(m).Error
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		return tx.Create(m).Error
	})
}

// SearchSimilar queries diagnosis memories by namespace, fault_type, and pod
// name prefix. Returns the most relevant matches ordered by confidence and recency.
func (r *AgentRepository) SearchSimilar(ctx context.Context, namespace, faultType, podPrefix string, limit int) ([]model.DiagnosisMemory, error) {
	if limit <= 0 {
		limit = 5
	}
	var memories []model.DiagnosisMemory
	query := r.db.WithContext(ctx).Model(&model.DiagnosisMemory{})
	if namespace != "" {
		query = query.Where("namespace = ?", namespace)
	}
	if faultType != "" {
		query = query.Where("fault_type = ?", faultType)
	}
	if podPrefix != "" {
		query = query.Where("pod_name_prefix = ?", podPrefix)
	}
	err := query.Order("confidence DESC, last_hit_at DESC").Limit(limit).Find(&memories).Error
	return memories, err
}

// UpdateHit increments the hit count and refreshes last_hit_at for a memory.
func (r *AgentRepository) UpdateHit(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&model.DiagnosisMemory{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"hit_count":   gorm.Expr("hit_count + 1"),
			"last_hit_at": time.Now(),
		}).Error
}

// BatchUpdateHits increments hit counts for multiple memory IDs in one query.
func (r *AgentRepository) BatchUpdateHits(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.DiagnosisMemory{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"hit_count":   gorm.Expr("hit_count + 1"),
			"last_hit_at": time.Now(),
		}).Error
}

// FindByTaskID retrieves a diagnosis memory by its associated task ID.
func (r *AgentRepository) FindByTaskID(ctx context.Context, taskID uint) (*model.DiagnosisMemory, error) {
	var memory model.DiagnosisMemory
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).First(&memory).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &memory, nil
}

// ListByFaultType returns diagnosis memories for a given fault type.
func (r *AgentRepository) ListByFaultType(ctx context.Context, faultType string, limit int) ([]model.DiagnosisMemory, error) {
	if limit <= 0 {
		limit = 10
	}
	var memories []model.DiagnosisMemory
	err := r.db.WithContext(ctx).Where("fault_type = ?", faultType).
		Order("confidence DESC, last_hit_at DESC").Limit(limit).Find(&memories).Error
	return memories, err
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
