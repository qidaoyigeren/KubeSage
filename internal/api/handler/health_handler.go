package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"kubesage/internal/k8s"
	"kubesage/internal/observability"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HealthHandler struct {
	db  *gorm.DB
	k8s *k8s.Client
}

// NewHealthHandler creates the lightweight health-check handler.
func NewHealthHandler(db *gorm.DB, k8sClient *k8s.Client) *HealthHandler {
	return &HealthHandler{db: db, k8s: k8sClient}
}

// Health returns a simple ok response for service liveness checks.
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(200, gin.H{"code": 0, "message": "success", "data": gin.H{"status": "ok"}})
}

// Ready checks dependencies that are required for serving diagnosis requests.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := gin.H{}
	status := http.StatusOK
	if err := h.pingMySQL(ctx); err != nil {
		status = http.StatusServiceUnavailable
		checks["mysql"] = gin.H{"status": "unhealthy", "error": err.Error()}
	} else {
		checks["mysql"] = gin.H{"status": "ok"}
	}
	if err := h.pingKubernetes(ctx); err != nil {
		status = http.StatusServiceUnavailable
		checks["kubernetes"] = gin.H{"status": "unhealthy", "error": err.Error()}
	} else {
		checks["kubernetes"] = gin.H{"status": "ok"}
	}

	c.JSON(status, gin.H{"code": statusCode(status), "message": statusMessage(status), "data": checks})
}

// Metrics exposes KubeSage's Prometheus text metrics.
func (h *HealthHandler) Metrics(c *gin.Context) {
	observability.Handler().ServeHTTP(c.Writer, c.Request)
}

// pingMySQL verifies that the underlying SQL connection is reachable.
func (h *HealthHandler) pingMySQL(ctx context.Context) error {
	if h.db == nil {
		return fmt.Errorf("mysql db is not configured")
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// pingKubernetes verifies Kubernetes API connectivity.
func (h *HealthHandler) pingKubernetes(ctx context.Context) error {
	if h.k8s == nil {
		return fmt.Errorf("kubernetes client is not configured")
	}
	return h.k8s.Ping(ctx)
}

// statusCode converts HTTP status into the API response code field.
func statusCode(status int) int {
	if status == http.StatusOK {
		return 0
	}
	return status
}

// statusMessage converts HTTP status into a compact health message.
func statusMessage(status int) string {
	if status == http.StatusOK {
		return "success"
	}
	return "unhealthy"
}
