package handler

import (
	"github.com/gin-gonic/gin"
)

type HealthHandler struct{}

// NewHealthHandler creates the lightweight health-check handler.
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health returns a simple ok response for service liveness checks.
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(200, gin.H{"code": 0, "message": "success", "data": gin.H{"status": "ok"}})
}
