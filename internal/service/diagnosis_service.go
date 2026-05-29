package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	"kubesage/internal/model"
	"kubesage/internal/prometheus"
	"kubesage/internal/rag"
	"kubesage/internal/repository"

	"go.uber.org/zap"
)

type PodDiagnosisRequest struct {
	Namespace      string `json:"namespace" binding:"required"`
	PodName        string `json:"pod_name" binding:"required"`
	ContainerName  string `json:"container_name"`
	IncludeLogs    bool   `json:"include_logs"`
	IncludeEvents  bool   `json:"include_events"`
	IncludeMetrics bool   `json:"include_metrics"`
}

type DiagnosisServiceOptions struct {
	Config           *config.Config
	Logger           *zap.Logger
	TaskRepo         *repository.DiagnosisRepository
	EvidenceRepo     *repository.EvidenceRepository
	ReportRepo       *repository.ReportRepository
	SnapshotService  *SnapshotService
	ReportService    *ReportService
	PrometheusClient *prometheus.Client
	RunbookRetriever rag.Retriever
}

type DiagnosisService struct {
	cfg              *config.Config
	log              *zap.Logger
	taskRepo         *repository.DiagnosisRepository
	evidenceRepo     *repository.EvidenceRepository
	reportRepo       *repository.ReportRepository
	snapshotService  *SnapshotService
	reportService    *ReportService
	prometheusClient *prometheus.Client
	runbookRetriever rag.Retriever
	engine           *diagnostic.DiagnosisEngine
}

// NewDiagnosisService wires repositories, collectors, analyzers, and optional
// enrichment dependencies into the diagnosis service.
func NewDiagnosisService(opts DiagnosisServiceOptions) *DiagnosisService {
	return &DiagnosisService{
		cfg:              opts.Config,
		log:              opts.Logger,
		taskRepo:         opts.TaskRepo,
		evidenceRepo:     opts.EvidenceRepo,
		reportRepo:       opts.ReportRepo,
		snapshotService:  opts.SnapshotService,
		reportService:    opts.ReportService,
		prometheusClient: opts.PrometheusClient,
		runbookRetriever: opts.RunbookRetriever,
		engine: diagnostic.NewDiagnosisEngine(
			diagnosticanalyzer.NewCrashLoopBackOffAnalyzer(),
			diagnosticanalyzer.NewOOMKilledAnalyzer(opts.PrometheusClient),
			diagnosticanalyzer.NewPendingAnalyzer(),
			diagnosticanalyzer.NewProbeFailedAnalyzer(),
		),
	}
}

// StartPodDiagnosis creates a task and runs the pod diagnosis asynchronously.
func (s *DiagnosisService) StartPodDiagnosis(ctx context.Context, req PodDiagnosisRequest) (*model.DiagnosisTask, error) {
	if !req.IncludeEvents && !req.IncludeLogs && !req.IncludeMetrics {
		req.IncludeEvents = true
		req.IncludeLogs = true
		req.IncludeMetrics = true
	}
	task := &model.DiagnosisTask{
		Namespace: req.Namespace,
		PodName:   req.PodName,
		Status:    model.TaskStatusRunning,
	}
	if err := s.taskRepo.Create(ctx, task); err != nil {
		return nil, err
	}

	go s.runDiagnosis(task.ID, req)
	return task, nil
}

// GetTask loads a diagnosis task by ID.
func (s *DiagnosisService) GetTask(ctx context.Context, id uint) (*model.DiagnosisTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.taskRepo.GetByID(ctx, id)
}

// ListTasks returns a paginated task list.
func (s *DiagnosisService) ListTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.taskRepo.List(ctx, page, pageSize)
}

// runDiagnosis collects snapshots, runs analyzers, persists evidence, and marks
// the task final status.
func (s *DiagnosisService) runDiagnosis(taskID uint, req PodDiagnosisRequest) {
	// The worker is read-only against Kubernetes: it collects snapshots, runs rules,
	// and stores evidence/report rows. It never deletes or patches cluster objects.
	timeout := time.Duration(s.cfg.Diagnosis.TaskTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		s.log.Error("load task failed", zap.Uint("task_id", taskID), zap.Error(err))
		return
	}

	finish := func(status string, report *diagnostic.Report, failure error) {
		task.Status = status
		task.FinishedAt = new(time.Now())
		if report != nil {
			task.FaultType = report.FaultType
			task.RootCauseSummary = report.RootCauseSummary
			task.ConfidenceScore = report.ConfidenceScore
		}
		if failure != nil {
			task.RootCauseSummary = failure.Error()
		}
		if err := s.taskRepo.Update(ctx, task); err != nil {
			s.log.Error("update task failed", zap.Uint("task_id", taskID), zap.Error(err))
		}
	}

	diagCtx, err := s.snapshotService.Collect(ctx, req)
	if err != nil {
		finish(model.TaskStatusFailed, nil, err)
		return
	}

	report, err := s.engine.Diagnose(diagCtx)
	if err != nil {
		finish(model.TaskStatusFailed, nil, err)
		return
	}

	if s.runbookRetriever != nil {
		hits, err := s.runbookRetriever.Retrieve(ctx, report.FaultType, report.RootCauseSummary, 3)
		if err == nil {
			for _, hit := range hits {
				report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
					SourceType: "runbook",
					Title:      hit.Title,
					Content:    hit.Content,
					Severity:   "info",
					Raw:        hit,
					Timestamp:  time.Now(),
				})
			}
		}
	}

	evidences := toModelEvidences(taskID, report.Evidences)
	if err := s.evidenceRepo.CreateBatch(ctx, evidences); err != nil {
		finish(model.TaskStatusFailed, report, err)
		return
	}

	reportModel := toModelReport(taskID, report, evidences)
	if err := s.reportService.Save(ctx, reportModel); err != nil {
		finish(model.TaskStatusFailed, report, err)
		return
	}

	finish(model.TaskStatusSuccess, report, nil)
}

// toModelEvidences converts in-memory evidence records into database models.
func toModelEvidences(taskID uint, records []diagnostic.EvidenceRecord) []model.Evidence {
	result := make([]model.Evidence, 0, len(records))
	for _, record := range records {
		rawBytes, _ := json.Marshal(record.Raw)
		timestamp := record.Timestamp
		if timestamp.IsZero() {
			timestamp = time.Now()
		}
		result = append(result, model.Evidence{
			TaskID:     taskID,
			SourceType: record.SourceType,
			Title:      record.Title,
			Content:    record.Content,
			Severity:   record.Severity,
			RawJSON:    string(rawBytes),
			CreatedAt:  timestamp,
		})
	}
	return result
}

// toModelReport converts an analyzer report into the persisted report model.
func toModelReport(taskID uint, report *diagnostic.Report, evidences []model.Evidence) *model.DiagnosisReport {
	actionsBytes, _ := json.Marshal(report.SuggestedActions)
	now := time.Now()
	return &model.DiagnosisReport{
		TaskID:           taskID,
		Namespace:        report.Namespace,
		PodName:          report.PodName,
		FaultType:        report.FaultType,
		RootCauseSummary: report.RootCauseSummary,
		ConfidenceScore:  clamp(report.ConfidenceScore),
		ImpactAnalysis:   report.ImpactAnalysis,
		SuggestedActions: string(actionsBytes),
		RiskLevel:        normalizeRisk(report.RiskLevel),
		NeedHumanConfirm: report.NeedHumanConfirm,
		CreatedAt:        now,
		GeneratedAt:      now,
		Evidences:        evidences,
	}
}

// clamp keeps confidence scores inside the [0, 1] range.
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// normalizeRisk maps unknown risk values to a safe medium default.
func normalizeRisk(risk string) string {
	risk = strings.ToLower(strings.TrimSpace(risk))
	switch risk {
	case "low", "medium", "high":
		return risk
	default:
		return "medium"
	}
}

// TriggerFromAlert starts a pod diagnosis from an Alertmanager alert mapping.
func (s *DiagnosisService) TriggerFromAlert(ctx context.Context, namespace, podName, alertName string) (*model.DiagnosisTask, error) {
	if namespace == "" || podName == "" {
		return nil, fmt.Errorf("namespace and pod_name are required in alert %s", alertName)
	}
	return s.StartPodDiagnosis(ctx, PodDiagnosisRequest{
		Namespace:      namespace,
		PodName:        podName,
		IncludeLogs:    true,
		IncludeEvents:  true,
		IncludeMetrics: true,
	})
}
