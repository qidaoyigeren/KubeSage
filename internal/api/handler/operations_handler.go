package handler

import (
	"context"
	"net/http"
	"strconv"

	"kubesage/internal/model"
	"kubesage/internal/repository"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
)

type OperationsReader interface {
	SubmitFeedback(ctx context.Context, taskID uint, req service.FeedbackRequest, actor string) (*model.DiagnosisFeedback, error)
	DashboardSummary(ctx context.Context, days int) (*repository.DashboardSummary, error)
	DashboardTrends(ctx context.Context, days int) ([]repository.DashboardTrendPoint, error)
	CreateRunbook(ctx context.Context, req service.RunbookRequest, actor string) (*model.Runbook, error)
	UpdateRunbook(ctx context.Context, id uint, req service.RunbookRequest, actor string) (*model.Runbook, error)
	ListRunbooks(ctx context.Context) ([]model.Runbook, error)
	ListAuditLogs(ctx context.Context, page, pageSize int) ([]model.AuditLog, int64, error)
	ListDeadLetters(ctx context.Context, page, pageSize int) ([]model.DiagnosisQueueDeadLetter, int64, error)
	RetryDeadLetter(ctx context.Context, id uint, actor string) (*model.DiagnosisTask, error)
	ListPendingApprovals(ctx context.Context) ([]model.RemediationExecution, error)
	ApproveRemediation(ctx context.Context, executionID uint, actor string) error
	RejectRemediation(ctx context.Context, executionID uint, actor, reason string) error
}

type OperationsHandler struct {
	service OperationsReader
}

func NewOperationsHandler(service OperationsReader) *OperationsHandler {
	return &OperationsHandler{service: service}
}

func (h *OperationsHandler) SubmitFeedback(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	var req service.FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	feedback, err := h.service.SubmitFeedback(c.Request.Context(), id, req, actorName(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": feedback})
}

func (h *OperationsHandler) DashboardSummary(c *gin.Context) {
	days := parsePositiveInt(c.DefaultQuery("days", "7"), 7)
	summary, err := h.service.DashboardSummary(c.Request.Context(), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": summary})
}

func (h *OperationsHandler) DashboardTrends(c *gin.Context) {
	days := parsePositiveInt(c.DefaultQuery("days", "14"), 14)
	trends, err := h.service.DashboardTrends(c.Request.Context(), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": trends})
}

func (h *OperationsHandler) ListRunbooks(c *gin.Context) {
	runbooks, err := h.service.ListRunbooks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": runbooks})
}

func (h *OperationsHandler) CreateRunbook(c *gin.Context) {
	var req service.RunbookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	runbook, err := h.service.CreateRunbook(c.Request.Context(), req, actorName(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": runbook})
}

func (h *OperationsHandler) UpdateRunbook(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	var req service.RunbookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	runbook, err := h.service.UpdateRunbook(c.Request.Context(), id, req, actorName(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": runbook})
}

func (h *OperationsHandler) ListAuditLogs(c *gin.Context) {
	page := parsePositiveInt(c.DefaultQuery("page", "1"), 1)
	pageSize := parsePositiveInt(c.DefaultQuery("page_size", "20"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	logs, total, err := h.service.ListAuditLogs(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{
		"items":     logs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}})
}

func (h *OperationsHandler) ListDeadLetters(c *gin.Context) {
	page := parsePositiveInt(c.DefaultQuery("page", "1"), 1)
	pageSize := parsePositiveInt(c.DefaultQuery("page_size", "20"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	items, total, err := h.service.ListDeadLetters(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}})
}

func (h *OperationsHandler) RetryDeadLetter(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	task, err := h.service.RetryDeadLetter(c.Request.Context(), id, actorName(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "dead letter retry scheduled", "data": task})
}

func (h *OperationsHandler) ListPendingApprovals(c *gin.Context) {
	approvals, err := h.service.ListPendingApprovals(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": approvals})
}

func (h *OperationsHandler) ApproveRemediation(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := h.service.ApproveRemediation(c.Request.Context(), id, actorName(c)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "remediation acknowledged for manual handling"})
}

func (h *OperationsHandler) RejectRemediation(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.service.RejectRemediation(c.Request.Context(), id, actorName(c), req.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "remediation rejected"})
}

func parseUintParam(c *gin.Context, name string) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid id"})
		return 0, false
	}
	return uint(id64), true
}
