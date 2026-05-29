package service

import (
	"context"

	"kubesage/internal/model"
	"kubesage/internal/repository"
)

type ReportService struct {
	repo *repository.ReportRepository
}

// NewReportService creates the service responsible for report persistence.
func NewReportService(repo *repository.ReportRepository) *ReportService {
	return &ReportService{repo: repo}
}

// Save persists a diagnosis report.
func (s *ReportService) Save(ctx context.Context, report *model.DiagnosisReport) error {
	return s.repo.Create(ctx, report)
}
