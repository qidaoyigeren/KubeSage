package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"kubesage/internal/agent"
	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	"kubesage/internal/llm"
	"kubesage/internal/model"
	"kubesage/internal/observability"
	"kubesage/internal/prometheus"
	"kubesage/internal/rag"
	"kubesage/internal/repository"
	"kubesage/internal/resilience"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type PodDiagnosisRequest struct {
	Namespace      string     `json:"namespace" binding:"required"`
	PodName        string     `json:"pod_name" binding:"required"`
	ContainerName  string     `json:"container_name"`
	ExpectedFault  string     `json:"expected_fault_type"`
	AlertName      string     `json:"alert_name"`
	AlertSeverity  string     `json:"alert_severity"`
	IncludeLogs    bool       `json:"include_logs"`
	IncludeEvents  bool       `json:"include_events"`
	IncludeMetrics bool       `json:"include_metrics"`
	AlertTime      *time.Time `json:"alert_time"`
}

type AlertDiagnosisRequest struct {
	Namespace     string
	PodName       string
	ContainerName string
	AlertName     string
	Severity      string
	FaultType     string
	AlertTime     *time.Time
}

type AlertDiagnosisResult struct {
	Task    *model.DiagnosisTask
	Deduped bool
}

type DiagnosisServiceOptions struct {
	Config           *config.Config
	Logger           *zap.Logger
	TaskRepo         *repository.DiagnosisRepository
	EvidenceRepo     *repository.EvidenceRepository
	ReportRepo       *repository.ReportRepository
	AgentRepo        *repository.AgentRepository
	SnapshotService  *SnapshotService
	ReportService    *ReportService
	PrometheusClient *prometheus.Client
	LLMClient        llm.LLMClient
	RunbookRetriever rag.Retriever
	RedisClient      *redis.Client
}

type DiagnosisService struct {
	cfg              *config.Config
	log              *zap.Logger
	taskRepo         *repository.DiagnosisRepository
	evidenceRepo     *repository.EvidenceRepository
	reportRepo       *repository.ReportRepository
	agentRepo        *repository.AgentRepository
	snapshotService  *SnapshotService
	reportService    *ReportService
	prometheusClient *prometheus.Client
	llmClient        llm.LLMClient
	runbookRetriever rag.Retriever
	engine           *diagnostic.DiagnosisEngine
	alertDedupMu     sync.Mutex
	podLocks         diagnosisLock
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
		agentRepo:        opts.AgentRepo,
		snapshotService:  opts.SnapshotService,
		reportService:    opts.ReportService,
		prometheusClient: opts.PrometheusClient,
		llmClient:        opts.LLMClient,
		runbookRetriever: opts.RunbookRetriever,
		podLocks:         newDiagnosisLock(opts.Config.Redis, opts.RedisClient),
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
	if err := ValidatePodDiagnosisRequest(req); err != nil {
		return nil, err
	}
	if !req.IncludeEvents && !req.IncludeLogs && !req.IncludeMetrics {
		req.IncludeEvents = true
		req.IncludeLogs = true
		req.IncludeMetrics = true
	}
	lockKey := podLockKey(req.Namespace, req.PodName)
	if s.podLocks == nil {
		s.podLocks = newDiagnosisLock(config.RedisConfig{}, nil)
	}
	lockToken, ok, err := s.podLocks.TryAcquire(ctx, lockKey, s.podLockTTL())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: namespace=%s pod=%s", ErrDiagnosisAlreadyRunning, req.Namespace, req.PodName)
	}
	task := &model.DiagnosisTask{
		Namespace:     req.Namespace,
		PodName:       req.PodName,
		Status:        model.TaskStatusRunning,
		AlertName:     req.AlertName,
		AlertSeverity: req.AlertSeverity,
		FaultType:     req.ExpectedFault,
	}
	if err := s.taskRepo.Create(ctx, task); err != nil {
		_ = s.podLocks.Release(ctx, lockKey, lockToken)
		return nil, err
	}

	go s.runDiagnosis(context.WithoutCancel(ctx), task.ID, req, lockKey, lockToken)
	return task, nil
}

// GetTask loads a diagnosis task by ID.
func (s *DiagnosisService) GetTask(ctx context.Context, id uint) (*model.DiagnosisTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	task, err := s.taskRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.hydrateAgentReport(ctx, task)
	return task, nil
}

// ListTasks returns a paginated task list.
func (s *DiagnosisService) ListTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.taskRepo.List(ctx, page, pageSize)
}

// runDiagnosis selects the Agent Runtime by default and keeps the legacy path
// available when agent.enabled=false.
func (s *DiagnosisService) runDiagnosis(parentCtx context.Context, taskID uint, req PodDiagnosisRequest, lockKey, lockToken string) {
	if !s.agentRuntimeEnabled() {
		s.runLegacyDiagnosis(parentCtx, taskID, req, lockKey, lockToken)
		return
	}
	s.runAgentDiagnosis(parentCtx, taskID, req, lockKey, lockToken)
}

// runAgentDiagnosis runs the auditable Agent Runtime and persists the final
// report using the same evidence/report tables as the legacy pipeline.
func (s *DiagnosisService) runAgentDiagnosis(parentCtx context.Context, taskID uint, req PodDiagnosisRequest, lockKey, lockToken string) {
	defer func() { _ = s.podLocks.Release(context.Background(), lockKey, lockToken) }()
	log := s.log.With(zap.Uint("task_id", taskID), zap.String("namespace", req.Namespace), zap.String("pod", req.PodName), zap.String("stage", "agent"))
	timeout := time.Duration(s.cfg.Diagnosis.TaskTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	baseCtx := parentCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	ctx, span := observability.Tracer().Start(ctx, "diagnosis.agent_run",
		trace.WithAttributes(
			attribute.Int64("task.id", int64(taskID)),
			attribute.String("k8s.namespace", req.Namespace),
			attribute.String("k8s.pod.name", req.PodName),
		),
	)
	defer span.End()
	log = log.With(zap.String("trace_id", span.SpanContext().TraceID().String()))

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		log.Error("load task failed", zap.Error(err))
		return
	}

	finish := func(status string, report *diagnostic.Report, failure error) {
		task.Status = status
		finishedAt := time.Now()
		task.FinishedAt = &finishedAt
		if report != nil {
			task.FaultType = report.FaultType
			task.RootCauseSummary = report.RootCauseSummary
			task.ConfidenceScore = report.ConfidenceScore
		}
		if failure != nil {
			task.RootCauseSummary = failure.Error()
		}
		if err := s.taskRepo.Update(ctx, task); err != nil {
			log.Error("update task failed", zap.Error(err))
		}
		observability.IncDiagnosisTotal(task.FaultType, status)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("agent diagnosis worker panic: %v", recovered)
			log.Error("agent diagnosis worker panic recovered", zap.Any("panic", recovered))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			finish(model.TaskStatusFailed, nil, err)
		}
	}()

	agentPolicy := agent.NewRemediationPolicy(s.cfg.Agent.EnableDryRunPreview)
	registry, err := agent.NewDefaultRegistry(agent.RegistryOptions{
		Snapshot:    s.agentSnapshotFunc(req),
		Retriever:   agentRunbookRetriever{base: s.runbookRetriever},
		Policy:      agentPolicy,
		ToolTimeout: time.Duration(s.cfg.Agent.ToolTimeoutSeconds) * time.Second,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		finish(model.TaskStatusFailed, nil, err)
		return
	}
	runtime := agent.NewRuntime(agent.RuntimeDeps{
		Store:    s.agentRepo,
		Registry: registry,
		Analyzer: s.engine,
		Policy:   agentPolicy,
	})
	result, err := runtime.Run(ctx, agent.RuntimeOptions{
		TaskID:              taskID,
		MaxSteps:            s.agentMaxSteps(),
		ToolTimeout:         s.agentToolTimeout(),
		EnableDryRunPreview: s.cfg.Agent.EnableDryRunPreview,
		Goal:                toAgentGoal(req),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		finish(model.TaskStatusFailed, nil, err)
		return
	}

	s.enhanceReportWithLLM(ctx, result.DiagContext, result.Report, toRAGHits(result.RunbookHits))
	s.refreshAgentSnapshot(result)

	start := time.Now()
	_, stageSpan := observability.Tracer().Start(ctx, "diagnosis.agent_persist")
	evidences := toModelEvidences(taskID, result.Report.Evidences)
	if err := s.evidenceRepo.CreateBatch(ctx, evidences); err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
		stageSpan.End()
		observability.ObserveDiagnosisStage("agent_persist", time.Since(start))
		finish(model.TaskStatusFailed, result.Report, err)
		return
	}
	reportModel := toModelReport(taskID, result.Report, evidences)
	if err := s.reportService.Save(ctx, reportModel); err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
		stageSpan.End()
		observability.ObserveDiagnosisStage("agent_persist", time.Since(start))
		finish(model.TaskStatusFailed, result.Report, err)
		return
	}
	stageSpan.End()
	observability.ObserveDiagnosisStage("agent_persist", time.Since(start))

	finish(model.TaskStatusSuccess, result.Report, nil)
}

// runLegacyDiagnosis collects snapshots, runs analyzers, persists evidence, and marks
// the task final status.
func (s *DiagnosisService) runLegacyDiagnosis(parentCtx context.Context, taskID uint, req PodDiagnosisRequest, lockKey, lockToken string) {
	// The worker is read-only against Kubernetes: it collects snapshots, runs rules,
	// and stores evidence/report rows. It never deletes or patches cluster objects.
	defer func() { _ = s.podLocks.Release(context.Background(), lockKey, lockToken) }()
	log := s.log.With(zap.Uint("task_id", taskID), zap.String("namespace", req.Namespace), zap.String("pod", req.PodName))
	timeout := time.Duration(s.cfg.Diagnosis.TaskTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	baseCtx := parentCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	ctx, span := observability.Tracer().Start(ctx, "diagnosis.run",
		trace.WithAttributes(
			attribute.Int64("task.id", int64(taskID)),
			attribute.String("k8s.namespace", req.Namespace),
			attribute.String("k8s.pod.name", req.PodName),
		),
	)
	defer span.End()
	log = log.With(zap.String("trace_id", span.SpanContext().TraceID().String()), zap.String("stage", "legacy"))

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		log.Error("load task failed", zap.Error(err))
		return
	}

	finish := func(status string, report *diagnostic.Report, failure error) {
		task.Status = status
		finishedAt := time.Now()
		task.FinishedAt = &finishedAt
		if report != nil {
			task.FaultType = report.FaultType
			task.RootCauseSummary = report.RootCauseSummary
			task.ConfidenceScore = report.ConfidenceScore
		}
		if failure != nil {
			task.RootCauseSummary = failure.Error()
		}
		if err := s.taskRepo.Update(ctx, task); err != nil {
			log.Error("update task failed", zap.Error(err))
		}
		observability.IncDiagnosisTotal(task.FaultType, status)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("diagnosis worker panic: %v", recovered)
			log.Error("diagnosis worker panic recovered", zap.Any("panic", recovered))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			finish(model.TaskStatusFailed, nil, err)
		}
	}()

	start := time.Now()
	snapshotCtx, stageSpan := observability.Tracer().Start(ctx, "diagnosis.snapshot")
	diagCtx, err := s.snapshotService.Collect(snapshotCtx, req)
	if err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
	}
	stageSpan.End()
	observability.ObserveDiagnosisStage("snapshot", time.Since(start))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		finish(model.TaskStatusFailed, nil, err)
		return
	}

	start = time.Now()
	_, stageSpan = observability.Tracer().Start(ctx, "diagnosis.analyze")
	report, err := s.engine.Diagnose(diagCtx)
	if err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
	}
	stageSpan.End()
	observability.ObserveDiagnosisStage("analyze", time.Since(start))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		finish(model.TaskStatusFailed, nil, err)
		return
	}

	var runbookHits []rag.Hit
	if s.runbookRetriever != nil {
		hits, err := s.runbookRetriever.Retrieve(ctx, report.FaultType, report.RootCauseSummary, 3)
		if err == nil {
			runbookHits = hits
			diagCtx.RunbookHits = toDiagnosticRunbookHits(hits)
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
	s.enhanceReportWithLLM(ctx, diagCtx, report, runbookHits)

	start = time.Now()
	_, stageSpan = observability.Tracer().Start(ctx, "diagnosis.persist")
	evidences := toModelEvidences(taskID, report.Evidences)
	if err := s.evidenceRepo.CreateBatch(ctx, evidences); err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
		stageSpan.End()
		observability.ObserveDiagnosisStage("persist", time.Since(start))
		finish(model.TaskStatusFailed, report, err)
		return
	}

	reportModel := toModelReport(taskID, report, evidences)
	if err := s.reportService.Save(ctx, reportModel); err != nil {
		stageSpan.RecordError(err)
		stageSpan.SetStatus(codes.Error, err.Error())
		stageSpan.End()
		observability.ObserveDiagnosisStage("persist", time.Since(start))
		finish(model.TaskStatusFailed, report, err)
		return
	}
	stageSpan.End()
	observability.ObserveDiagnosisStage("persist", time.Since(start))

	finish(model.TaskStatusSuccess, report, nil)
}

// enhanceReportWithLLM asks the LLM to summarize the rule report. Failures are
// recorded as warning evidence and the rule-based report remains authoritative.
func (s *DiagnosisService) enhanceReportWithLLM(ctx context.Context, diagCtx *diagnostic.DiagnosticContext, report *diagnostic.Report, runbookHits []rag.Hit) {
	ruleResult := llm.RuleResultFromReport(report)
	report.RuleBasedResult = ruleResult
	if s.llmClient == nil {
		return
	}
	prompt := llm.BuildPrompt(diagCtx, ruleResult, report.Evidences, runbookHits)
	var summary *llm.EnhancedSummary
	start := time.Now()
	ctx, span := observability.Tracer().Start(ctx, "diagnosis.llm")
	err := resilience.Do(ctx, s.llmRetryConfig(), func() error {
		var err error
		summary, err = s.llmClient.GenerateDiagnosisSummary(ctx, prompt)
		return err
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	observability.ObserveDiagnosisStage("llm", time.Since(start))
	if err != nil {
		observability.IncLLMCallTotal("failed")
		s.log.Warn("llm enhancement failed; keep rule-based report", zap.Error(err))
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "llm",
			Title:      "LLM enhancement failed",
			Content:    err.Error(),
			Severity:   "warning",
			Raw:        map[string]string{"error": err.Error()},
			Timestamp:  time.Now(),
		})
		return
	}
	observability.IncLLMCallTotal("success")
	report.LLMEnhancedSummary = summary
}

// llmRetryConfig returns retry settings for optional LLM enhancement calls.
func (s *DiagnosisService) llmRetryConfig() resilience.RetryConfig {
	if s == nil || s.cfg == nil {
		return resilience.FromMilliseconds(1, 0, 0)
	}
	return resilience.FromMilliseconds(
		s.cfg.LLM.RetryMaxAttempts,
		s.cfg.LLM.RetryInitialBackoffMS,
		s.cfg.LLM.RetryMaxBackoffMS,
	)
}

func (s *DiagnosisService) agentRuntimeEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Agent.Enabled && s.agentRepo != nil
}

func (s *DiagnosisService) agentMaxSteps() int {
	if s == nil || s.cfg == nil || s.cfg.Agent.MaxSteps <= 0 {
		return 12
	}
	return s.cfg.Agent.MaxSteps
}

func (s *DiagnosisService) agentToolTimeout() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Agent.ToolTimeoutSeconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(s.cfg.Agent.ToolTimeoutSeconds) * time.Second
}

func (s *DiagnosisService) agentSnapshotFunc(req PodDiagnosisRequest) agent.SnapshotFunc {
	return func(ctx context.Context, goal agent.Goal) (*diagnostic.DiagnosticContext, error) {
		if s == nil || s.snapshotService == nil {
			return nil, fmt.Errorf("snapshot service is not configured")
		}
		next := req
		next.Namespace = goal.Namespace
		next.PodName = goal.PodName
		next.ContainerName = goal.ContainerName
		next.ExpectedFault = goal.ExpectedFault
		next.AlertName = goal.AlertName
		next.AlertSeverity = goal.AlertSeverity
		next.IncludeLogs = goal.IncludeLogs
		next.IncludeEvents = goal.IncludeEvents
		next.IncludeMetrics = goal.IncludeMetrics
		next.AlertTime = goal.AlertTime
		return s.snapshotService.Collect(ctx, next)
	}
}

func toAgentGoal(req PodDiagnosisRequest) agent.Goal {
	return agent.Goal{
		Namespace:      req.Namespace,
		PodName:        req.PodName,
		ContainerName:  req.ContainerName,
		ExpectedFault:  req.ExpectedFault,
		AlertName:      req.AlertName,
		AlertSeverity:  req.AlertSeverity,
		IncludeLogs:    req.IncludeLogs,
		IncludeEvents:  req.IncludeEvents,
		IncludeMetrics: req.IncludeMetrics,
		AlertTime:      req.AlertTime,
	}
}

type agentRunbookRetriever struct {
	base rag.Retriever
}

func (r agentRunbookRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]agent.RunbookHit, error) {
	if r.base == nil {
		return nil, nil
	}
	hits, err := r.base.Retrieve(ctx, faultType, query, limit)
	if err != nil {
		return nil, err
	}
	result := make([]agent.RunbookHit, 0, len(hits))
	for _, hit := range hits {
		result = append(result, agent.RunbookHit{
			Title:            hit.Title,
			Content:          hit.Content,
			Score:            hit.Score,
			RecommendedTools: hit.Hints.RecommendedTools,
			StopConditions:   hit.Hints.StopConditions,
		})
	}
	return result, nil
}

func toRAGHits(hits []agent.RunbookHit) []rag.Hit {
	result := make([]rag.Hit, 0, len(hits))
	for _, hit := range hits {
		result = append(result, rag.Hit{
			Title:   hit.Title,
			Content: hit.Content,
			Score:   hit.Score,
		})
	}
	return result
}

func (s *DiagnosisService) refreshAgentSnapshot(result *agent.RunResult) {
	if result == nil || result.Report == nil {
		return
	}
	snapshot := agent.BuildReportSnapshot(result.Report, result.Hypotheses, result.RunbookHits, result.Executions, result.VerificationPlan, result.StopReason)
	result.Report.AgentExecutionSummary = snapshot.AgentExecutionSummary
	result.Report.AgentReportSnapshot = snapshot
	result.Report.VerificationPlan = snapshot.VerificationPlan
	result.Report.ResidualRisks = snapshot.ResidualRisks
}

func (s *DiagnosisService) hydrateAgentReport(ctx context.Context, task *model.DiagnosisTask) {
	if s == nil || s.agentRepo == nil || task == nil || task.Report == nil {
		return
	}
	if steps, err := s.agentRepo.ListStepsByTaskID(ctx, task.ID); err == nil {
		task.Report.AgentTimeline = steps
	}
	if hypotheses, err := s.agentRepo.ListHypothesesByTaskID(ctx, task.ID); err == nil {
		task.Report.Hypotheses = hypotheses
	}
	if executions, err := s.agentRepo.ListRemediationExecutionsByTaskID(ctx, task.ID); err == nil {
		task.Report.RemediationExecutions = executions
	}
}

// toDiagnosticRunbookHits converts RAG hits into context runbook hits.
func toDiagnosticRunbookHits(hits []rag.Hit) []diagnostic.RunbookHit {
	result := make([]diagnostic.RunbookHit, 0, len(hits))
	for _, hit := range hits {
		result = append(result, diagnostic.RunbookHit{
			Title:   hit.Title,
			Content: hit.Content,
		})
	}
	return result
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
	remediationBytes, _ := json.Marshal(report.RemediationActions)
	now := time.Now()
	return &model.DiagnosisReport{
		TaskID:                taskID,
		Namespace:             report.Namespace,
		PodName:               report.PodName,
		FaultType:             report.FaultType,
		RootCauseSummary:      report.RootCauseSummary,
		ConfidenceScore:       clamp(report.ConfidenceScore),
		ImpactAnalysis:        report.ImpactAnalysis,
		SuggestedActions:      string(actionsBytes),
		RemediationActions:    model.JSONText(remediationBytes),
		RiskLevel:             normalizeRisk(report.RiskLevel),
		NeedHumanConfirm:      report.NeedHumanConfirm,
		RuleBasedResult:       marshalJSONField(report.RuleBasedResult),
		LLMEnhancedSummary:    marshalJSONField(report.LLMEnhancedSummary),
		AgentExecutionSummary: report.AgentExecutionSummary,
		AgentReportSnapshot:   model.JSONText(marshalJSONField(report.AgentReportSnapshot)),
		CreatedAt:             now,
		GeneratedAt:           now,
		Evidences:             evidences,
	}
}

// marshalJSONField serializes optional structured report sections.
func marshalJSONField(value interface{}) string {
	if value == nil {
		return ""
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(bytes)
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

// TriggerFromAlert starts or reuses a pod diagnosis from an Alertmanager alert.
func (s *DiagnosisService) TriggerFromAlert(ctx context.Context, req AlertDiagnosisRequest) (*AlertDiagnosisResult, error) {
	if req.Namespace == "" || req.PodName == "" {
		return nil, fmt.Errorf("namespace and pod_name are required in alert %s", req.AlertName)
	}
	if req.AlertName != "" {
		s.alertDedupMu.Lock()
		defer s.alertDedupMu.Unlock()
		since := time.Now().Add(-5 * time.Minute)
		existing, err := s.taskRepo.FindRecentAlertTask(ctx, req.Namespace, req.PodName, req.AlertName, since)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return &AlertDiagnosisResult{Task: existing, Deduped: true}, nil
		}
	}
	task, err := s.StartPodDiagnosis(ctx, PodDiagnosisRequest{
		Namespace:      req.Namespace,
		PodName:        req.PodName,
		ContainerName:  req.ContainerName,
		ExpectedFault:  req.FaultType,
		AlertName:      req.AlertName,
		AlertSeverity:  req.Severity,
		IncludeLogs:    true,
		IncludeEvents:  true,
		IncludeMetrics: true,
		AlertTime:      req.AlertTime,
	})
	if err != nil {
		return nil, err
	}
	return &AlertDiagnosisResult{Task: task}, nil
}

// podLockTTL returns the configured duplicate diagnosis lock duration.
func (s *DiagnosisService) podLockTTL() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Diagnosis.PodLockTTLSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(s.cfg.Diagnosis.PodLockTTLSeconds) * time.Second
}

// diagnosisRetryConfig returns retry settings shared by read-only diagnosis calls.
func (s *DiagnosisService) diagnosisRetryConfig() resilience.RetryConfig {
	if s == nil || s.cfg == nil {
		return resilience.FromMilliseconds(1, 0, 0)
	}
	return resilience.FromMilliseconds(
		s.cfg.Diagnosis.RetryMaxAttempts,
		s.cfg.Diagnosis.RetryInitialBackoffMS,
		s.cfg.Diagnosis.RetryMaxBackoffMS,
	)
}

// podLockKey builds a stable duplicate guard key.
func podLockKey(namespace, podName string) string {
	return namespace + "/" + podName
}

// IsInvalidDiagnosisRequest reports whether an error is request validation.
func IsInvalidDiagnosisRequest(err error) bool {
	return errors.Is(err, ErrInvalidDiagnosisRequest)
}

// IsDiagnosisAlreadyRunning reports whether a pod diagnosis lock is held.
func IsDiagnosisAlreadyRunning(err error) bool {
	return errors.Is(err, ErrDiagnosisAlreadyRunning)
}
