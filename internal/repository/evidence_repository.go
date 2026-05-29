package repository

import (
	"context"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type EvidenceRepository struct {
	db *gorm.DB
}

// NewEvidenceRepository creates the database access layer for evidence rows.
func NewEvidenceRepository(db *gorm.DB) *EvidenceRepository {
	return &EvidenceRepository{db: db}
}

// CreateBatch inserts evidence rows for one diagnosis task.
func (r *EvidenceRepository) CreateBatch(ctx context.Context, evidences []model.Evidence) error {
	if len(evidences) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&evidences).Error
}

// ListByTaskID loads all evidence rows that belong to a task.
func (r *EvidenceRepository) ListByTaskID(ctx context.Context, taskID uint) ([]model.Evidence, error) {
	var evidences []model.Evidence
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("id asc").Find(&evidences).Error
	return evidences, err
}
