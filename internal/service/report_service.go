package service

import (
	"context"

	"kubesage/internal/model"
	"kubesage/internal/repository"
)

type ReportService struct {
	repo *repository.ReportRepository
}

func NewReportService(repo *repository.ReportRepository) *ReportService {
	return &ReportService{repo: repo}
}

func (s *ReportService) Save(ctx context.Context, report *model.DiagnosisReport) error {
	return s.repo.Create(ctx, report)
}
