package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"kubesage/internal/model"

	"github.com/gin-gonic/gin"
)

type TaskHandler struct {
	service           TaskReader
	canceller         TaskCanceller
	allowedNamespaces []string
}

type TaskReader interface {
	GetTask(ctx context.Context, id uint) (*model.DiagnosisTask, error)
	ListTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error)
}

type NamespaceTaskReader interface {
	ListTasksByNamespaces(ctx context.Context, page, pageSize int, namespaces []string) ([]model.DiagnosisTask, int64, error)
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

func (h *TaskHandler) SetAllowedNamespaces(namespaces []string) {
	h.allowedNamespaces = namespaces
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
	if !namespaceAllowed(task.Namespace, h.allowedNamespaces) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "namespace is not allowed"})
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

	tasks, total, err := h.listTasks(c.Request.Context(), page, pageSize)
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

func (h *TaskHandler) listTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error) {
	if namespaceWildcard(h.allowedNamespaces) {
		return h.service.ListTasks(ctx, page, pageSize)
	}
	if reader, ok := h.service.(NamespaceTaskReader); ok {
		return reader.ListTasksByNamespaces(ctx, page, pageSize, h.allowedNamespaces)
	}
	tasks, _, err := h.service.ListTasks(ctx, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]model.DiagnosisTask, 0, len(tasks))
	for _, task := range tasks {
		if namespaceAllowed(task.Namespace, h.allowedNamespaces) {
			filtered = append(filtered, task)
		}
	}
	return filtered, int64(len(filtered)), nil
}

func namespaceWildcard(allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, namespace := range allowed {
		if strings.TrimSpace(namespace) == "*" {
			return true
		}
	}
	return false
}

func namespaceAllowed(namespace string, allowed []string) bool {
	if namespaceWildcard(allowed) {
		return true
	}
	for _, item := range allowed {
		if strings.TrimSpace(item) == namespace {
			return true
		}
	}
	return false
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
