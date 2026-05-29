package repository

import (
	"context"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type DiagnosisRepository struct {
	db *gorm.DB
}

type ReportRepository struct {
	db *gorm.DB
}

func NewDiagnosisRepository(db *gorm.DB) *DiagnosisRepository {
	return &DiagnosisRepository{db: db}
}

func NewReportRepository(db *gorm.DB) *ReportRepository {
	return &ReportRepository{db: db}
}

func (r *DiagnosisRepository) Create(ctx context.Context, task *model.DiagnosisTask) error {
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *DiagnosisRepository) Update(ctx context.Context, task *model.DiagnosisTask) error {
	return r.db.WithContext(ctx).Save(task).Error
}

func (r *DiagnosisRepository) GetByID(ctx context.Context, id uint) (*model.DiagnosisTask, error) {
	var task model.DiagnosisTask
	err := r.db.WithContext(ctx).
		Preload("Evidences").
		Preload("Report").
		First(&task, id).Error
	if err != nil {
		return nil, err
	}
	if task.Report != nil {
		task.Report.Evidences = task.Evidences
	}
	return &task, nil
}

func (r *DiagnosisRepository) List(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error) {
	var total int64
	var tasks []model.DiagnosisTask
	query := r.db.WithContext(ctx).Model(&model.DiagnosisTask{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func (r *ReportRepository) Create(ctx context.Context, report *model.DiagnosisReport) error {
	return r.db.WithContext(ctx).Create(report).Error
}
