package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/model"
	"kubesage/internal/rag"
	"kubesage/internal/repository"
)

type FeedbackRequest struct {
	Rating                 string   `json:"rating" binding:"required"`
	CorrectedRootCause     string   `json:"corrected_root_cause"`
	HelpfulEvidenceRefs    []string `json:"helpful_evidence_refs"`
	HelpfulToolNames       []string `json:"helpful_tool_names"`
	HelpfulHypothesisTypes []string `json:"helpful_hypothesis_types"`
	Comment                string   `json:"comment"`
}

type RunbookRequest struct {
	FaultType string           `json:"fault_type" binding:"required"`
	Title     string           `json:"title" binding:"required"`
	Content   string           `json:"content" binding:"required"`
	Hints     rag.RunbookHints `json:"hints"`
}

type OperationsService struct {
	feedback      *repository.FeedbackRepository
	audit         *repository.AuditRepository
	dashboard     *repository.DashboardRepository
	runbooks      *repository.RunbookRepository
	retention     *repository.RetentionRepository
	agentRepo     *repository.AgentRepository
	indexer       rag.Indexer
	retentionDays int
}

type OperationsOptions struct {
	FeedbackRepo  *repository.FeedbackRepository
	AuditRepo     *repository.AuditRepository
	DashboardRepo *repository.DashboardRepository
	RunbookRepo   *repository.RunbookRepository
	RetentionRepo *repository.RetentionRepository
	AgentRepo     *repository.AgentRepository
	Indexer       rag.Indexer
	RetentionDays int
}

func NewOperationsService(opts OperationsOptions) *OperationsService {
	return &OperationsService{
		feedback:      opts.FeedbackRepo,
		audit:         opts.AuditRepo,
		dashboard:     opts.DashboardRepo,
		runbooks:      opts.RunbookRepo,
		retention:     opts.RetentionRepo,
		agentRepo:     opts.AgentRepo,
		indexer:       opts.Indexer,
		retentionDays: opts.RetentionDays,
	}
}

func (s *OperationsService) SubmitFeedback(ctx context.Context, taskID uint, req FeedbackRequest, actor string) (*model.DiagnosisFeedback, error) {
	if s == nil || s.feedback == nil {
		return nil, fmt.Errorf("feedback service is not configured")
	}
	rating := strings.ToLower(strings.TrimSpace(req.Rating))
	if rating != model.FeedbackRatingUseful && rating != model.FeedbackRatingNotUseful {
		return nil, fmt.Errorf("rating must be useful or not_useful")
	}
	feedback := &model.DiagnosisFeedback{
		TaskID:                 taskID,
		Rating:                 rating,
		CorrectedRootCause:     req.CorrectedRootCause,
		HelpfulEvidenceRefs:    jsonText(req.HelpfulEvidenceRefs),
		HelpfulToolNames:       jsonText(req.HelpfulToolNames),
		HelpfulHypothesisTypes: jsonText(req.HelpfulHypothesisTypes),
		Comment:                req.Comment,
		CreatedBy:              actor,
		CreatedAt:              time.Now(),
	}
	if err := s.feedback.Create(ctx, feedback); err != nil {
		return nil, err
	}
	_ = s.RecordAudit(ctx, actor, "diagnosis.feedback", "", "diagnosis_task", fmt.Sprintf("%d", taskID), &taskID, rating, map[string]interface{}{"rating": rating})
	return feedback, nil
}

func (s *OperationsService) DashboardSummary(ctx context.Context, days int) (*repository.DashboardSummary, error) {
	if s == nil || s.dashboard == nil {
		return nil, fmt.Errorf("dashboard service is not configured")
	}
	if days <= 0 {
		days = 7
	}
	return s.dashboard.Summary(ctx, time.Now().AddDate(0, 0, -days))
}

func (s *OperationsService) DashboardTrends(ctx context.Context, days int) ([]repository.DashboardTrendPoint, error) {
	if s == nil || s.dashboard == nil {
		return nil, fmt.Errorf("dashboard service is not configured")
	}
	if days <= 0 {
		days = 14
	}
	return s.dashboard.Trends(ctx, time.Now().AddDate(0, 0, -days))
}

func (s *OperationsService) CreateRunbook(ctx context.Context, req RunbookRequest, actor string) (*model.Runbook, error) {
	if s == nil || s.runbooks == nil {
		return nil, fmt.Errorf("runbook service is not configured")
	}
	hints := jsonText(req.Hints)
	runbook := &model.Runbook{
		FaultType: strings.TrimSpace(req.FaultType),
		Title:     strings.TrimSpace(req.Title),
		Content:   req.Content,
		HintsJSON: hints,
		Version:   1,
		CreatedBy: actor,
		UpdatedBy: actor,
	}
	chunks := buildRunbookChunks(runbook.Title, runbook.Content)
	if err := s.runbooks.Create(ctx, runbook, chunks); err != nil {
		return nil, err
	}
	_ = s.indexRunbook(ctx, runbook, chunks)
	_ = s.RecordAudit(ctx, actor, "runbook.create", "", "runbook", runbook.Title, nil, runbook.FaultType, nil)
	return runbook, nil
}

func (s *OperationsService) UpdateRunbook(ctx context.Context, id uint, req RunbookRequest, actor string) (*model.Runbook, error) {
	if s == nil || s.runbooks == nil {
		return nil, fmt.Errorf("runbook service is not configured")
	}
	runbook := &model.Runbook{
		ID:        id,
		FaultType: strings.TrimSpace(req.FaultType),
		Title:     strings.TrimSpace(req.Title),
		Content:   req.Content,
		HintsJSON: jsonText(req.Hints),
		UpdatedBy: actor,
	}
	chunks := buildRunbookChunks(runbook.Title, runbook.Content)
	if err := s.runbooks.Update(ctx, runbook, chunks); err != nil {
		return nil, err
	}
	_ = s.indexRunbook(ctx, runbook, chunks)
	_ = s.RecordAudit(ctx, actor, "runbook.update", "", "runbook", runbook.Title, nil, runbook.FaultType, map[string]interface{}{"id": id})
	return runbook, nil
}

func (s *OperationsService) ListRunbooks(ctx context.Context) ([]model.Runbook, error) {
	if s == nil || s.runbooks == nil {
		return nil, fmt.Errorf("runbook service is not configured")
	}
	return s.runbooks.List(ctx)
}

// ListAuditLogs returns paginated audit logs.
func (s *OperationsService) ListAuditLogs(ctx context.Context, page, pageSize int) ([]model.AuditLog, int64, error) {
	if s == nil || s.audit == nil {
		return nil, 0, fmt.Errorf("audit service is not configured")
	}
	return s.audit.List(ctx, page, pageSize)
}

// ListPendingApprovals returns remediation executions pending approval.
func (s *OperationsService) ListPendingApprovals(ctx context.Context) ([]model.RemediationExecution, error) {
	if s == nil || s.agentRepo == nil {
		return nil, fmt.Errorf("operations service is not configured")
	}
	return s.agentRepo.ListPendingApprovals(ctx)
}

// ApproveRemediation approves a pending remediation execution.
func (s *OperationsService) ApproveRemediation(ctx context.Context, executionID uint, actor string) error {
	if s == nil || s.agentRepo == nil {
		return fmt.Errorf("operations service is not configured")
	}
	if err := s.agentRepo.ApproveRemediation(ctx, executionID, actor); err != nil {
		return err
	}
	_ = s.RecordAudit(ctx, actor, "remediation.approve", "", "remediation_execution", fmt.Sprintf("%d", executionID), nil, "approved", nil)
	return nil
}

// RejectRemediation rejects a pending remediation execution.
func (s *OperationsService) RejectRemediation(ctx context.Context, executionID uint, actor, reason string) error {
	if s == nil || s.agentRepo == nil {
		return fmt.Errorf("operations service is not configured")
	}
	if err := s.agentRepo.RejectRemediation(ctx, executionID, actor, reason); err != nil {
		return err
	}
	_ = s.RecordAudit(ctx, actor, "remediation.reject", "", "remediation_execution", fmt.Sprintf("%d", executionID), nil, reason, nil)
	return nil
}

func (s *OperationsService) RecordAudit(ctx context.Context, actor, action, namespace, resourceKind, resourceName string, taskID *uint, summary string, metadata interface{}) error {
	if s == nil || s.audit == nil {
		return nil
	}
	return s.audit.Create(ctx, &model.AuditLog{
		Actor:        actor,
		Action:       action,
		Namespace:    namespace,
		ResourceKind: resourceKind,
		ResourceName: resourceName,
		TaskID:       taskID,
		Summary:      summary,
		MetadataJSON: jsonText(metadata),
		CreatedAt:    time.Now(),
	})
}

func (s *OperationsService) CleanupRetention(ctx context.Context) error {
	if s == nil || s.retention == nil || s.retentionDays <= 0 {
		return nil
	}
	return s.retention.CleanupOlderThan(ctx, time.Now().AddDate(0, 0, -s.retentionDays))
}

func (s *OperationsService) StartRetentionLoop(ctx context.Context, interval time.Duration) {
	if s == nil || s.retention == nil || s.retentionDays <= 0 {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.CleanupRetention(ctx)
			}
		}
	}()
}

func (s *OperationsService) indexRunbook(ctx context.Context, runbook *model.Runbook, chunks []model.RunbookChunk) error {
	if s == nil || s.indexer == nil || runbook == nil {
		return nil
	}
	items := make([]rag.IndexChunk, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, rag.IndexChunk{
			ID:        fmt.Sprintf("runbook-%d-%d", runbook.ID, chunk.ChunkIndex),
			RunbookID: runbook.ID,
			FaultType: runbook.FaultType,
			Title:     chunk.Title,
			Content:   chunk.Content,
		})
	}
	return s.indexer.Upsert(ctx, items)
}

func buildRunbookChunks(title, content string) []model.RunbookChunk {
	paragraphs := strings.Split(content, "\n\n")
	chunks := make([]model.RunbookChunk, 0, len(paragraphs))
	buffer := ""
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if len(buffer)+len(paragraph) > 2000 && buffer != "" {
			chunks = append(chunks, model.RunbookChunk{ChunkIndex: len(chunks), Title: title, Content: buffer})
			buffer = ""
		}
		if buffer != "" {
			buffer += "\n\n"
		}
		buffer += paragraph
	}
	if strings.TrimSpace(buffer) != "" {
		chunks = append(chunks, model.RunbookChunk{ChunkIndex: len(chunks), Title: title, Content: buffer})
	}
	return chunks
}

func jsonText(value interface{}) model.JSONText {
	bytes, err := json.Marshal(value)
	if err != nil {
		return model.JSONText("null")
	}
	return model.JSONText(bytes)
}
