package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kubesage/internal/api"
	"kubesage/internal/config"
	"kubesage/internal/db"
	"kubesage/internal/k8s"
	"kubesage/internal/llm"
	"kubesage/internal/logger"
	"kubesage/internal/loki"
	"kubesage/internal/model"
	"kubesage/internal/observability"
	"kubesage/internal/prometheus"
	"kubesage/internal/rag"
	"kubesage/internal/repository"
	"kubesage/internal/service"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// buildRunbookChunks loads markdown runbooks from disk and converts them into
// indexable chunks for the vector store.
func buildRunbookChunks(dir string) []rag.IndexChunk {
	runbooks, err := rag.LoadRunbooks(dir)
	if err != nil {
		return nil
	}
	var chunks []rag.IndexChunk
	for _, rb := range runbooks {
		for i, section := range rb.Sections {
			chunks = append(chunks, rag.IndexChunk{
				ID:        fmt.Sprintf("runbook_%s_%d", rb.FaultType, i),
				RunbookID: 0, // file-based runbooks have no DB ID
				FaultType: rb.FaultType,
				Title:     section.Title,
				Content:   section.Content,
			})
		}
	}
	return chunks
}

// main initializes dependencies, starts the HTTP server, and handles shutdown.
func main() {
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		panic(fmt.Sprintf("load config: %v", err))
	}

	log, err := logger.Init(cfg.Log)
	if err != nil {
		panic(fmt.Sprintf("init logger: %v", err))
	}
	defer func() { _ = log.Sync() }()

	// Print security warnings for sensitive config fields.
	for _, w := range cfg.SecurityWarnings() {
		log.Warn("SECURITY WARNING: " + w)
	}

	traceShutdown, err := observability.InitTracing(context.Background(), cfg.OTel)
	if err != nil {
		log.Fatal("init tracing failed", zap.Error(err))
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(ctx); err != nil {
			log.Warn("shutdown tracing failed", zap.Error(err))
		}
	}()

	database, err := db.NewMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal("connect mysql failed", zap.Error(err))
	}
	if cfg.Migrations.Enabled {
		if err := db.RunMigrations(cfg.MySQL, cfg.Migrations.Dir); err != nil {
			log.Fatal("run database migrations failed", zap.Error(err))
		}
	} else {
		log.Warn("database migrations disabled; falling back to GORM AutoMigrate")
		if err := database.AutoMigrate(&model.DiagnosisTask{}, &model.Evidence{}, &model.DiagnosisReport{}, &model.AgentStep{}, &model.Hypothesis{}, &model.RemediationExecution{}, &model.DiagnosisFeedback{}, &model.AuditLog{}, &model.Runbook{}, &model.RunbookChunk{}, &model.DiagnosisQueueDeadLetter{}, &model.LLMUsageRecord{}); err != nil {
			log.Fatal("auto migrate failed", zap.Error(err))
		}
	}

	k8sClient, err := k8s.NewClient(cfg.Kubernetes)
	if err != nil {
		log.Warn("kubernetes client is not ready; diagnosis requests will fail until config is fixed", zap.Error(err))
	}

	taskRepo := repository.NewDiagnosisRepository(database)
	evidenceRepo := repository.NewEvidenceRepository(database)
	reportRepo := repository.NewReportRepository(database)
	agentRepo := repository.NewAgentRepository(database)
	feedbackRepo := repository.NewFeedbackRepository(database)
	auditRepo := repository.NewAuditRepository(database)
	runbookRepo := repository.NewRunbookRepository(database)
	dashboardRepo := repository.NewDashboardRepository(database)
	deadLetterRepo := repository.NewDeadLetterRepository(database)
	retentionRepo := repository.NewRetentionRepository(database)
	llmUsageRepo := repository.NewLLMUsageRepository(database)

	keywordRetriever, err := rag.NewSimpleKeywordRetriever(cfg.Runbook.Dir)
	if err != nil {
		log.Warn("load runbooks failed", zap.Error(err))
	}
	vectorIndexer := rag.NewIndexer(cfg.RAG)
	var vectorRetriever rag.Retriever
	if vectorIndexer != nil {
		vectorRetriever = vectorIndexer
		// Ensure Qdrant collection exists and index runbooks on startup.
		go func() {
			indexCtx, indexCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer indexCancel()
			dimension := cfg.RAG.Embedding.Dimension
			if dimension <= 0 {
				dimension = 1536 // default for text-embedding-ada-002
			}
			if err := vectorIndexer.EnsureCollection(indexCtx, dimension); err != nil {
				log.Warn("ensure qdrant collection failed", zap.Error(err))
				return
			}
			chunks := buildRunbookChunks(cfg.Runbook.Dir)
			// Also index DB-stored runbooks.
			dbRunbooks, err := runbookRepo.List(indexCtx)
			if err == nil {
				for _, rb := range dbRunbooks {
					chunks = append(chunks, rag.IndexChunk{
						ID:        fmt.Sprintf("db_runbook_%d", rb.ID),
						RunbookID: rb.ID,
						FaultType: rb.FaultType,
						Title:     rb.Title,
						Content:   rb.Content,
					})
				}
			}
			if len(chunks) > 0 {
				if err := vectorIndexer.Upsert(indexCtx, chunks); err != nil {
					log.Warn("startup runbook indexing failed", zap.Error(err))
				} else {
					log.Info("startup runbook indexing completed", zap.Int("chunks", len(chunks)))
				}
			}
		}()
	}
	runbookRetriever := rag.NewHybridRetriever(vectorRetriever, keywordRetriever, cfg.RAG.PreferVector)
	var llmClient llm.LLMClient
	if cfg.LLM.Enabled {
		llmClient = llm.NewOpenAICompatibleClient(cfg.LLM)
	}
	var lokiClient *loki.Client
	if cfg.Loki.Enabled {
		lokiClient = loki.NewClient(cfg.Loki)
	}
	var redisClient redis.UniversalClient
	if cfg.Redis.Enabled {
		redisClient = db.NewRedis(cfg.Redis)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			log.Fatal("connect redis failed", zap.Error(err))
		}
		defer func() { _ = redisClient.Close() }()
	}
	prometheusClient := prometheus.NewClient(cfg.Prometheus)
	operationsSvc := service.NewOperationsService(service.OperationsOptions{
		FeedbackRepo:  feedbackRepo,
		AuditRepo:     auditRepo,
		DashboardRepo: dashboardRepo,
		RunbookRepo:   runbookRepo,
		RetentionRepo: retentionRepo,
		AgentRepo:     agentRepo,
		Indexer:       vectorIndexer,
		RetentionDays: cfg.Retention.TaskDays,
	})
	if err := operationsSvc.CleanupRetention(context.Background()); err != nil {
		log.Warn("retention cleanup failed", zap.Error(err))
	}
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	operationsSvc.StartRetentionLoop(appCtx, 24*time.Hour)

	diagnosisSvc := service.NewDiagnosisService(service.DiagnosisServiceOptions{
		Config:           cfg,
		Logger:           log,
		TaskRepo:         taskRepo,
		EvidenceRepo:     evidenceRepo,
		ReportRepo:       reportRepo,
		AgentRepo:        agentRepo,
		DeadLetterRepo:   deadLetterRepo,
		LLMUsageRepo:     llmUsageRepo,
		SnapshotService:  service.NewSnapshotService(cfg, k8sClient, log, lokiClient, prometheusClient),
		ReportService:    service.NewReportService(reportRepo),
		PrometheusClient: prometheusClient,
		LLMClient:        llmClient,
		RunbookRetriever: runbookRetriever,
		RedisClient:      redisClient,
		Notifier:         service.NewNotifier(cfg.Notification),
	})
	if err := diagnosisSvc.StartQueueWorkers(appCtx); err != nil {
		log.Fatal("start diagnosis queue worker failed", zap.Error(err))
	}

	router := api.NewRouter(api.RouterOptions{
		DiagnosisService:  diagnosisSvc,
		OperationsService: operationsSvc,
		Authorizer:        service.NewKubernetesAuthorizer(cfg.Auth, k8sClient),
		Logger:            log,
		DB:                database,
		K8sClient:         k8sClient,
		AuthToken:         cfg.Server.AuthToken,
		AuthMode:          cfg.Auth.Mode,
	})

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		log.Info("kubesage server started", zap.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Error("server shutdown failed", zap.Error(err))
	}
	drainTimeout := time.Duration(cfg.Queue.DrainTimeoutSeconds) * time.Second
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), drainTimeout)
	defer drainCancel()
	if err := diagnosisSvc.Shutdown(drainCtx); err != nil {
		log.Warn("diagnosis worker drain timed out", zap.Error(err))
	}
}
