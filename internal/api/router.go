package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"kubesage/internal/alertmanager"
	"kubesage/internal/api/handler"
	"kubesage/internal/k8s"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type RouterOptions struct {
	DiagnosisService  *service.DiagnosisService
	OperationsService *service.OperationsService
	Authorizer        service.PodAuthorizer
	Logger            *zap.Logger
	DB                *gorm.DB
	K8sClient         *k8s.Client
	AuthToken         string
	AuthMode          string
}

// NewRouter wires all HTTP routes and their handlers into a Gin engine.
func NewRouter(opts RouterOptions) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())
	router.Use(traceMiddleware())

	healthHandler := handler.NewHealthHandler(opts.DB, opts.K8sClient)
	diagnoseHandler := handler.NewDiagnoseHandler(opts.DiagnosisService, opts.Authorizer, opts.OperationsService)
	taskHandler := handler.NewTaskHandler(opts.DiagnosisService, opts.DiagnosisService)
	operationsHandler := handler.NewOperationsHandler(opts.OperationsService)
	alertHandler := alertmanager.NewWebhookHandler(opts.DiagnosisService, opts.Logger)

	router.GET("/health", healthHandler.Health)
	router.GET("/healthz", healthHandler.Health)
	router.GET("/readyz", healthHandler.Ready)
	router.GET("/metrics", healthHandler.Metrics)
	router.StaticFile("/openapi.yaml", "./docs/openapi.yaml")

	v1 := router.Group("/api/v1")
	v1.Use(authMiddleware(opts.AuthMode, opts.AuthToken, opts.K8sClient))
	v1.Use(auditMiddleware(opts.OperationsService, opts.Logger))
	{
		v1.POST("/diagnose/pod", diagnoseHandler.DiagnosePod)
		v1.GET("/diagnose/tasks/:id", taskHandler.GetTask)
		v1.GET("/diagnose/tasks", taskHandler.ListTasks)
		v1.POST("/diagnose/tasks/:id/cancel", RequireOperator(), taskHandler.CancelTask)
		v1.POST("/diagnose/tasks/:id/feedback", operationsHandler.SubmitFeedback)
		v1.GET("/dashboard/summary", operationsHandler.DashboardSummary)
		v1.GET("/dashboard/trends", operationsHandler.DashboardTrends)
		v1.GET("/runbooks", operationsHandler.ListRunbooks)
		v1.POST("/runbooks", RequireOperator(), operationsHandler.CreateRunbook)
		v1.PUT("/runbooks/:id", RequireOperator(), operationsHandler.UpdateRunbook)
		v1.GET("/audit-logs", operationsHandler.ListAuditLogs)
		v1.GET("/remediation/pending", operationsHandler.ListPendingApprovals)
		v1.POST("/remediation/:id/approve", RequireOperator(), operationsHandler.ApproveRemediation)
		v1.POST("/remediation/:id/reject", RequireOperator(), operationsHandler.RejectRemediation)
		v1.POST("/alertmanager/webhook", alertHandler.Handle)
		v1.GET("/openapi.yaml", func(c *gin.Context) { c.File("./docs/openapi.yaml") })
	}

	// Serve frontend static files from web/dist if available
	staticDir := "./web/dist"
	if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
		router.Static("/assets", filepath.Join(staticDir, "assets"))
		router.StaticFile("/favicon.svg", filepath.Join(staticDir, "favicon.svg"))

		// SPA catch-all: serve index.html for any non-API route
		router.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/health") ||
				strings.HasPrefix(path, "/healthz") || strings.HasPrefix(path, "/readyz") ||
				strings.HasPrefix(path, "/metrics") {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
				return
			}
			c.File(filepath.Join(staticDir, "index.html"))
		})
	}

	return router
}
