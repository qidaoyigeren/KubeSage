package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"kubesage/internal/alertmanager"
	"kubesage/internal/api/handler"
	"kubesage/internal/config"
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
	Auth              config.AuthConfig
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
	taskHandler.SetAllowedNamespaces(opts.Auth.AllowedNamespaces)
	operationsHandler := handler.NewOperationsHandler(opts.OperationsService)
	alertHandler := alertmanager.NewWebhookHandler(opts.DiagnosisService, opts.Logger)

	router.GET("/health", healthHandler.Health)
	router.GET("/healthz", healthHandler.Health)
	router.GET("/readyz", healthHandler.Ready)
	router.GET("/metrics", healthHandler.Metrics)
	router.StaticFile("/openapi.yaml", "./docs/openapi.yaml")

	v1 := router.Group("/api/v1")
	authCfg := opts.Auth
	if authCfg.Mode == "" {
		authCfg.Mode = opts.AuthMode
	}
	v1.Use(authMiddleware(authCfg.Mode, opts.AuthToken, opts.K8sClient, authCfg))
	v1.Use(auditMiddleware(opts.OperationsService, opts.Logger))
	{
		v1.GET("/me", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{
				"actor":              Actor(c),
				"role":               ActorRole(c),
				"allowed_namespaces": authCfg.AllowedNamespaces,
			}})
		})
		v1.POST("/diagnose/pod", RequireOperator(), diagnoseHandler.DiagnosePod)
		v1.GET("/diagnose/task-events", taskHandler.StreamTasks)
		v1.GET("/diagnose/tasks/:id/events", taskHandler.StreamTask)
		v1.GET("/diagnose/tasks/:id", taskHandler.GetTask)
		v1.GET("/diagnose/tasks", taskHandler.ListTasks)
		v1.POST("/diagnose/tasks/:id/cancel", RequireOperator(), taskHandler.CancelTask)
		v1.POST("/diagnose/tasks/:id/feedback", operationsHandler.SubmitFeedback)
		v1.GET("/dashboard/summary", operationsHandler.DashboardSummary)
		v1.GET("/dashboard/trends", operationsHandler.DashboardTrends)
		v1.GET("/runbooks", operationsHandler.ListRunbooks)
		v1.POST("/runbooks", RequireAdmin(), operationsHandler.CreateRunbook)
		v1.PUT("/runbooks/:id", RequireAdmin(), operationsHandler.UpdateRunbook)
		v1.GET("/audit-logs", RequireAdmin(), operationsHandler.ListAuditLogs)
		v1.GET("/dead-letters", RequireOperator(), operationsHandler.ListDeadLetters)
		v1.POST("/dead-letters/:id/retry", RequireOperator(), operationsHandler.RetryDeadLetter)
		v1.GET("/remediation/pending", RequireOperator(), operationsHandler.ListPendingApprovals)
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
