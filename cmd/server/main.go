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
		if err := database.AutoMigrate(&model.DiagnosisTask{}, &model.Evidence{}, &model.DiagnosisReport{}); err != nil {
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

	runbookRetriever, err := rag.NewSimpleKeywordRetriever(cfg.Runbook.Dir)
	if err != nil {
		log.Warn("load runbooks failed", zap.Error(err))
	}
	var llmClient llm.LLMClient
	if cfg.LLM.Enabled {
		llmClient = llm.NewOpenAICompatibleClient(cfg.LLM)
	}
	var lokiClient *loki.Client
	if cfg.Loki.Enabled {
		lokiClient = loki.NewClient(cfg.Loki)
	}
	var redisClient *redis.Client
	if cfg.Redis.Enabled {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Address,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			log.Fatal("connect redis failed", zap.Error(err))
		}
		defer func() { _ = redisClient.Close() }()
	}

	diagnosisSvc := service.NewDiagnosisService(service.DiagnosisServiceOptions{
		Config:           cfg,
		Logger:           log,
		TaskRepo:         taskRepo,
		EvidenceRepo:     evidenceRepo,
		ReportRepo:       reportRepo,
		SnapshotService:  service.NewSnapshotService(cfg, k8sClient, log, lokiClient),
		ReportService:    service.NewReportService(reportRepo),
		PrometheusClient: prometheus.NewClient(cfg.Prometheus),
		LLMClient:        llmClient,
		RunbookRetriever: runbookRetriever,
		RedisClient:      redisClient,
	})

	router := api.NewRouter(api.RouterOptions{
		DiagnosisService: diagnosisSvc,
		Logger:           log,
		DB:               database,
		K8sClient:        k8sClient,
		AuthToken:        cfg.Server.AuthToken,
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
}
