package repository

import (
	"context"
	"time"

	"kubesage/internal/model"

	"gorm.io/gorm"
)

type FeedbackRepository struct {
	db *gorm.DB
}

type AuditRepository struct {
	db *gorm.DB
}

type RunbookRepository struct {
	db *gorm.DB
}

type DashboardRepository struct {
	db *gorm.DB
}

type DeadLetterRepository struct {
	db *gorm.DB
}

type RetentionRepository struct {
	db *gorm.DB
}

type LLMUsageRepository struct {
	db *gorm.DB
}

func NewFeedbackRepository(db *gorm.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

func NewRunbookRepository(db *gorm.DB) *RunbookRepository {
	return &RunbookRepository{db: db}
}

func NewDashboardRepository(db *gorm.DB) *DashboardRepository {
	return &DashboardRepository{db: db}
}

func NewDeadLetterRepository(db *gorm.DB) *DeadLetterRepository {
	return &DeadLetterRepository{db: db}
}

func NewRetentionRepository(db *gorm.DB) *RetentionRepository {
	return &RetentionRepository{db: db}
}

func NewLLMUsageRepository(db *gorm.DB) *LLMUsageRepository {
	return &LLMUsageRepository{db: db}
}

func (r *FeedbackRepository) Create(ctx context.Context, feedback *model.DiagnosisFeedback) error {
	return r.db.WithContext(ctx).Create(feedback).Error
}

func (r *AuditRepository) Create(ctx context.Context, entry *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(entry).Error
}

func (r *AuditRepository) List(ctx context.Context, page, pageSize int) ([]model.AuditLog, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var total int64
	var logs []model.AuditLog
	base := r.db.WithContext(ctx).Model(&model.AuditLog{})
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := base.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error
	return logs, total, err
}

func (r *RunbookRepository) Create(ctx context.Context, runbook *model.Runbook, chunks []model.RunbookChunk) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(runbook).Error; err != nil {
			return err
		}
		for i := range chunks {
			chunks[i].RunbookID = runbook.ID
		}
		if len(chunks) > 0 {
			return tx.Create(&chunks).Error
		}
		return nil
	})
}

func (r *RunbookRepository) Update(ctx context.Context, runbook *model.Runbook, chunks []model.RunbookChunk) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.Runbook
		if err := tx.First(&existing, runbook.ID).Error; err != nil {
			return err
		}
		existing.FaultType = runbook.FaultType
		existing.Title = runbook.Title
		existing.Content = runbook.Content
		existing.HintsJSON = runbook.HintsJSON
		existing.UpdatedBy = runbook.UpdatedBy
		existing.Version++
		if err := tx.Save(&existing).Error; err != nil {
			return err
		}
		if err := tx.Where("runbook_id = ?", existing.ID).Delete(&model.RunbookChunk{}).Error; err != nil {
			return err
		}
		for i := range chunks {
			chunks[i].RunbookID = existing.ID
		}
		if len(chunks) > 0 {
			return tx.Create(&chunks).Error
		}
		*runbook = existing
		return nil
	})
}

func (r *RunbookRepository) List(ctx context.Context) ([]model.Runbook, error) {
	var runbooks []model.Runbook
	err := r.db.WithContext(ctx).Preload("Chunks").Order("fault_type asc, id asc").Find(&runbooks).Error
	return runbooks, err
}

func (r *RunbookRepository) Get(ctx context.Context, id uint) (*model.Runbook, error) {
	var runbook model.Runbook
	if err := r.db.WithContext(ctx).Preload("Chunks").First(&runbook, id).Error; err != nil {
		return nil, err
	}
	return &runbook, nil
}

func (r *DeadLetterRepository) Create(ctx context.Context, entry *model.DiagnosisQueueDeadLetter) error {
	return r.db.WithContext(ctx).Create(entry).Error
}

func (r *LLMUsageRepository) Create(ctx context.Context, entry *model.LLMUsageRecord) error {
	return r.db.WithContext(ctx).Create(entry).Error
}

type DashboardSummary struct {
	TotalTasks             int64             `json:"total_tasks"`
	RunningTasks           int64             `json:"running_tasks"`
	SuccessTasks           int64             `json:"success_tasks"`
	FailedTasks            int64             `json:"failed_tasks"`
	SuccessRate            float64           `json:"success_rate"`
	AverageDurationSeconds float64           `json:"average_duration_seconds"`
	FeedbackUseful         int64             `json:"feedback_useful"`
	FeedbackNotUseful      int64             `json:"feedback_not_useful"`
	FeedbackAccuracyRate   float64           `json:"feedback_accuracy_rate"`
	TopRootCauses          []DashboardBucket `json:"top_root_causes"`
	AnalyzerHitRates       []DashboardBucket `json:"analyzer_hit_rates"`
	LLMCalls               map[string]int64  `json:"llm_calls"`
	LLMTotalTokens         int64             `json:"llm_total_tokens"`
	LLMAverageLatencyMS    float64           `json:"llm_average_latency_ms"`
	LLMEstimatedCost       float64           `json:"llm_estimated_cost"`
}

type DashboardBucket struct {
	Name  string  `json:"name"`
	Count int64   `json:"count"`
	Rate  float64 `json:"rate,omitempty"`
}

type DashboardTrendPoint struct {
	Date      string `json:"date"`
	FaultType string `json:"fault_type"`
	Count     int64  `json:"count"`
}

func (r *DashboardRepository) Summary(ctx context.Context, since time.Time) (*DashboardSummary, error) {
	summary := &DashboardSummary{LLMCalls: map[string]int64{}}
	base := r.db.WithContext(ctx).Model(&model.DiagnosisTask{}).Where("created_at >= ?", since)
	if err := base.Count(&summary.TotalTasks).Error; err != nil {
		return nil, err
	}
	if err := base.Where("status in ?", []string{model.TaskStatusPending, model.TaskStatusRunning}).Count(&summary.RunningTasks).Error; err != nil {
		return nil, err
	}
	if err := base.Where("status = ?", model.TaskStatusSuccess).Count(&summary.SuccessTasks).Error; err != nil {
		return nil, err
	}
	if err := base.Where("status = ?", model.TaskStatusFailed).Count(&summary.FailedTasks).Error; err != nil {
		return nil, err
	}
	if summary.TotalTasks > 0 {
		summary.SuccessRate = float64(summary.SuccessTasks) / float64(summary.TotalTasks)
	}
	type avgRow struct{ Average float64 }
	var avg avgRow
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COALESCE(AVG(TIMESTAMPDIFF(MICROSECOND, created_at, finished_at)) / 1000000, 0) AS average FROM diagnosis_tasks WHERE created_at >= ? AND finished_at IS NOT NULL",
		since,
	).Scan(&avg).Error; err != nil {
		return nil, err
	}
	summary.AverageDurationSeconds = avg.Average
	if err := r.db.WithContext(ctx).Model(&model.DiagnosisFeedback{}).Where("created_at >= ? AND rating = ?", since, model.FeedbackRatingUseful).Count(&summary.FeedbackUseful).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.DiagnosisFeedback{}).Where("created_at >= ? AND rating = ?", since, model.FeedbackRatingNotUseful).Count(&summary.FeedbackNotUseful).Error; err != nil {
		return nil, err
	}
	if totalFeedback := summary.FeedbackUseful + summary.FeedbackNotUseful; totalFeedback > 0 {
		summary.FeedbackAccuracyRate = float64(summary.FeedbackUseful) / float64(totalFeedback)
	}
	var topRows []DashboardBucket
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COALESCE(fault_type, 'unknown') AS name, COUNT(*) AS count FROM diagnosis_tasks WHERE created_at >= ? GROUP BY fault_type ORDER BY count DESC LIMIT 10",
		since,
	).Scan(&topRows).Error; err != nil {
		return nil, err
	}
	summary.TopRootCauses = topRows
	var analyzerRows []DashboardBucket
	if err := r.db.WithContext(ctx).Raw(
		"SELECT tool_name AS name, COUNT(*) AS count FROM agent_steps WHERE created_at >= ? AND stage = 'tool_call' AND tool_name IS NOT NULL GROUP BY tool_name ORDER BY count DESC LIMIT 10",
		since,
	).Scan(&analyzerRows).Error; err != nil {
		return nil, err
	}
	summary.AnalyzerHitRates = analyzerRows
	type llmRow struct {
		Status string
		Count  int64
	}
	var llmRows []llmRow
	if err := r.db.WithContext(ctx).Raw(
		"SELECT status, COUNT(*) AS count FROM agent_steps WHERE created_at >= ? AND tool_name = 'llm.plan' GROUP BY status",
		since,
	).Scan(&llmRows).Error; err != nil {
		return nil, err
	}
	for _, row := range llmRows {
		summary.LLMCalls[row.Status] = row.Count
	}
	type llmUsageRow struct {
		TotalTokens int64
		AvgLatency  float64
		Cost        float64
	}
	var usage llmUsageRow
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(AVG(latency_ms), 0) AS avg_latency, COALESCE(SUM(estimated_cost), 0) AS cost FROM llm_usage_records WHERE created_at >= ?",
		since,
	).Scan(&usage).Error; err != nil {
		return nil, err
	}
	summary.LLMTotalTokens = usage.TotalTokens
	summary.LLMAverageLatencyMS = usage.AvgLatency
	summary.LLMEstimatedCost = usage.Cost
	return summary, nil
}

func (r *DashboardRepository) Trends(ctx context.Context, since time.Time) ([]DashboardTrendPoint, error) {
	var points []DashboardTrendPoint
	err := r.db.WithContext(ctx).Raw(
		"SELECT DATE(created_at) AS date, COALESCE(fault_type, 'unknown') AS fault_type, COUNT(*) AS count FROM diagnosis_tasks WHERE created_at >= ? GROUP BY DATE(created_at), fault_type ORDER BY date ASC, count DESC",
		since,
	).Scan(&points).Error
	return points, err
}

func (r *RetentionRepository) CleanupOlderThan(ctx context.Context, cutoff time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&model.DiagnosisTask{}).Where("created_at < ?", cutoff).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.Evidence{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.DiagnosisReport{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.AgentStep{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.Hypothesis{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.RemediationExecution{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id IN ?", ids).Delete(&model.DiagnosisFeedback{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&model.DiagnosisTask{}).Error
	})
}
