package handler

import (
	"context"
	"net/http"

	"kubesage/internal/model"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
)

type DiagnoseHandler struct {
	service    DiagnosisStarter
	authorizer service.PodAuthorizer
	audit      AuditRecorder
}

type DiagnosisStarter interface {
	StartPodDiagnosis(ctx context.Context, req service.PodDiagnosisRequest) (*model.DiagnosisTask, error)
}

type AuditRecorder interface {
	RecordAudit(ctx context.Context, actor, action, namespace, resourceKind, resourceName string, taskID *uint, summary string, metadata interface{}) error
}

// NewDiagnoseHandler creates the HTTP handler for pod diagnosis requests.
func NewDiagnoseHandler(starter DiagnosisStarter, authorizer service.PodAuthorizer, audit AuditRecorder) *DiagnoseHandler {
	return &DiagnoseHandler{service: starter, authorizer: authorizer, audit: audit}
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
	if h.authorizer != nil {
		if err := h.authorizer.AuthorizePodDiagnosis(c.Request.Context(), bearerToken(c), req.Namespace, req.PodName); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": err.Error()})
			return
		}
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
	if h.audit != nil {
		_ = h.audit.RecordAudit(c.Request.Context(), actorName(c), "diagnosis.start", req.Namespace, "pod", req.PodName, &task.ID, req.ExpectedFault, map[string]interface{}{
			"include_logs": req.IncludeLogs, "include_events": req.IncludeEvents, "include_metrics": req.IncludeMetrics,
		})
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": task})
}

func actorName(c *gin.Context) string {
	if value, ok := c.Get("actor"); ok {
		if actor, ok := value.(string); ok && actor != "" {
			return actor
		}
	}
	return "anonymous"
}

func bearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}
