package repository

import (
	"context"
	"time"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type DiagnosisRepository struct {
	db *gorm.DB
}

type ReportRepository struct {
	db *gorm.DB
}

// NewDiagnosisRepository creates the database access layer for diagnosis tasks.
func NewDiagnosisRepository(db *gorm.DB) *DiagnosisRepository {
	return &DiagnosisRepository{db: db}
}

// NewReportRepository creates the database access layer for diagnosis reports.
func NewReportRepository(db *gorm.DB) *ReportRepository {
	return &ReportRepository{db: db}
}

// Create inserts a new diagnosis task.
func (r *DiagnosisRepository) Create(ctx context.Context, task *model.DiagnosisTask) error {
	return r.db.WithContext(ctx).Create(task).Error
}

// Update saves the latest diagnosis task state.
func (r *DiagnosisRepository) Update(ctx context.Context, task *model.DiagnosisTask) error {
	return r.db.WithContext(ctx).Save(task).Error
}

// GetByID loads a task with its evidence and report.
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

// FindRecentAlertTask finds an existing task for the same alert fingerprint
// inside the deduplication window.
func (r *DiagnosisRepository) FindRecentAlertTask(ctx context.Context, namespace, podName, alertName string, since time.Time) (*model.DiagnosisTask, error) {
	var task model.DiagnosisTask
	err := r.db.WithContext(ctx).
		Where("namespace = ? AND pod_name = ? AND alert_name = ? AND created_at >= ?", namespace, podName, alertName, since).
		Order("id desc").
		First(&task).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

// List returns a page of tasks and the total task count.
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

// Create inserts a diagnosis report.
func (r *ReportRepository) Create(ctx context.Context, report *model.DiagnosisReport) error {
	return r.db.WithContext(ctx).Create(report).Error
}
