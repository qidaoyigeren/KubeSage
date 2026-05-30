package handler

import (
	"context"
	"net/http"

	"kubesage/internal/model"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
)

type DiagnoseHandler struct {
	service DiagnosisStarter
}

type DiagnosisStarter interface {
	StartPodDiagnosis(ctx context.Context, req service.PodDiagnosisRequest) (*model.DiagnosisTask, error)
}

// NewDiagnoseHandler creates the HTTP handler for pod diagnosis requests.
func NewDiagnoseHandler(service DiagnosisStarter) *DiagnoseHandler {
	return &DiagnoseHandler{service: service}
}

// DiagnosePod validates the request body and starts an asynchronous pod
// diagnosis task.
func (h *DiagnoseHandler) DiagnosePod(c *gin.Context) {
	var req service.PodDiagnosisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Namespace == "" || req.PodName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "namespace and pod_name are required"})
		return
	}

	task, err := h.service.StartPodDiagnosis(c.Request.Context(), req)
	if err != nil {
		if service.IsInvalidDiagnosisRequest(err) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
		if service.IsDiagnosisAlreadyRunning(err) {
			c.JSON(http.StatusConflict, gin.H{"code": 409, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": task})
}
