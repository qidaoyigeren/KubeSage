package handler

import (
	"context"
	"net/http"
	"strconv"

	"kubesage/internal/model"

	"github.com/gin-gonic/gin"
)

type TaskHandler struct {
	service   TaskReader
	canceller TaskCanceller
}

type TaskReader interface {
	GetTask(ctx context.Context, id uint) (*model.DiagnosisTask, error)
	ListTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error)
}

type TaskCanceller interface {
	CancelTask(ctx context.Context, taskID uint) error
}

// NewTaskHandler creates handlers for reading diagnosis task state.
func NewTaskHandler(service TaskReader, canceller ...TaskCanceller) *TaskHandler {
	h := &TaskHandler{service: service}
	if len(canceller) > 0 {
		h.canceller = canceller[0]
	}
	return h
}

// GetTask loads one diagnosis task and its report/evidence by task ID.
func (h *TaskHandler) GetTask(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid task id"})
		return
	}
	task, err := h.service.GetTask(c.Request.Context(), uint(id64))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": task})
}

// ListTasks returns a paginated list of diagnosis tasks.
func (h *TaskHandler) ListTasks(c *gin.Context) {
	page := parsePositiveInt(c.DefaultQuery("page", "1"), 1)
	pageSize := parsePositiveInt(c.DefaultQuery("page_size", "20"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	tasks, total, err := h.service.ListTasks(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{
		"items":     tasks,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}})
}

// parsePositiveInt parses a positive integer query parameter with a fallback.
func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// CancelTask cancels a running diagnosis task.
func (h *TaskHandler) CancelTask(c *gin.Context) {
	if h.canceller == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"code": 501, "message": "task cancellation is not supported"})
		return
	}
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid task id"})
		return
	}
	if err := h.canceller.CancelTask(c.Request.Context(), uint(id64)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "task cancelled"})
}
