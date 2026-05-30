package api

import (
	"kubesage/internal/alertmanager"
	"kubesage/internal/api/handler"
	"kubesage/internal/k8s"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type RouterOptions struct {
	DiagnosisService *service.DiagnosisService
	Logger           *zap.Logger
	DB               *gorm.DB
	K8sClient        *k8s.Client
	AuthToken        string
}

// NewRouter wires all HTTP routes and their handlers into a Gin engine.
func NewRouter(opts RouterOptions) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())
	router.Use(traceMiddleware())

	healthHandler := handler.NewHealthHandler(opts.DB, opts.K8sClient)
	diagnoseHandler := handler.NewDiagnoseHandler(opts.DiagnosisService)
	taskHandler := handler.NewTaskHandler(opts.DiagnosisService)
	alertHandler := alertmanager.NewWebhookHandler(opts.DiagnosisService, opts.Logger)

	router.GET("/health", healthHandler.Health)
	router.GET("/healthz", healthHandler.Health)
	router.GET("/readyz", healthHandler.Ready)
	router.GET("/metrics", healthHandler.Metrics)

	v1 := router.Group("/api/v1")
	v1.Use(bearerAuth(opts.AuthToken))
	{
		v1.POST("/diagnose/pod", diagnoseHandler.DiagnosePod)
		v1.GET("/diagnose/tasks/:id", taskHandler.GetTask)
		v1.GET("/diagnose/tasks", taskHandler.ListTasks)
		v1.POST("/alertmanager/webhook", alertHandler.Handle)
	}

	return router
}
