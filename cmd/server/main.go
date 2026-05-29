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
	"kubesage/internal/logger"
	"kubesage/internal/model"
	"kubesage/internal/prometheus"
	"kubesage/internal/rag"
	"kubesage/internal/repository"
	"kubesage/internal/service"

	"go.uber.org/zap"
)

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

	database, err := db.NewMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal("connect mysql failed", zap.Error(err))
	}
	if err := database.AutoMigrate(&model.DiagnosisTask{}, &model.Evidence{}, &model.DiagnosisReport{}); err != nil {
		log.Fatal("auto migrate failed", zap.Error(err))
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

	diagnosisSvc := service.NewDiagnosisService(service.DiagnosisServiceOptions{
		Config:           cfg,
		Logger:           log,
		TaskRepo:         taskRepo,
		EvidenceRepo:     evidenceRepo,
		ReportRepo:       reportRepo,
		SnapshotService:  service.NewSnapshotService(cfg, k8sClient, log),
		ReportService:    service.NewReportService(reportRepo),
		PrometheusClient: prometheus.NewClient(cfg.Prometheus),
		RunbookRetriever: runbookRetriever,
	})

	router := api.NewRouter(api.RouterOptions{
		DiagnosisService: diagnosisSvc,
		Logger:           log,
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
