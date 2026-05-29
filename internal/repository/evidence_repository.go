package repository

import (
	"context"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type EvidenceRepository struct {
	db *gorm.DB
}

func NewEvidenceRepository(db *gorm.DB) *EvidenceRepository {
	return &EvidenceRepository{db: db}
}

func (r *EvidenceRepository) CreateBatch(ctx context.Context, evidences []model.Evidence) error {
	if len(evidences) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&evidences).Error
}

func (r *EvidenceRepository) ListByTaskID(ctx context.Context, taskID uint) ([]model.Evidence, error) {
	var evidences []model.Evidence
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("id asc").Find(&evidences).Error
	return evidences, err
}
