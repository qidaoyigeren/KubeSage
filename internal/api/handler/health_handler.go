package handler

import (
	"github.com/gin-gonic/gin"
)

type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(200, gin.H{"code": 0, "message": "success", "data": gin.H{"status": "ok"}})
}
