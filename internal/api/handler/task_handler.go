package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kubesage/internal/model"

	"github.com/gin-gonic/gin"
)

const (
	taskStreamInterval  = time.Second
	tasksStreamInterval = 2 * time.Second
	streamHeartbeat     = 15 * time.Second
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

// StreamTask streams task snapshots for a detail page using Server-Sent Events.
func (h *TaskHandler) StreamTask(c *gin.Context) {
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
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "streaming is not supported"})
		return
	}

	setStreamHeaders(c)
	lastVersion := ""
	sendTask := func(task *model.DiagnosisTask) {
		version := taskStreamVersion(task)
		if version == lastVersion {
			return
		}
		lastVersion = version
		c.SSEvent("task", task)
		flusher.Flush()
	}
	sendTask(task)
	if taskTerminal(task) {
		return
	}

	ticker := time.NewTicker(taskStreamInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			task, err := h.service.GetTask(c.Request.Context(), uint(id64))
			if err != nil {
				c.SSEvent("error", gin.H{"message": err.Error()})
				flusher.Flush()
				return
			}
			if !namespaceAllowed(task.Namespace, h.allowedNamespaces) {
				c.SSEvent("error", gin.H{"message": "namespace is not allowed"})
				flusher.Flush()
				return
			}
			sendTask(task)
			if taskTerminal(task) {
				return
			}
		case <-heartbeat.C:
			writeStreamHeartbeat(c, flusher)
		}
	}
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

// StreamTasks streams the current task list page using Server-Sent Events.
func (h *TaskHandler) StreamTasks(c *gin.Context) {
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
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "streaming is not supported"})
		return
	}

	setStreamHeaders(c)
	lastVersion := ""
	sendTasks := func(tasks []model.DiagnosisTask, total int64) {
		version := tasksStreamVersion(tasks, total)
		if version == lastVersion {
			return
		}
		lastVersion = version
		c.SSEvent("tasks", gin.H{
			"items":     tasks,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		})
		flusher.Flush()
	}
	sendTasks(tasks, total)

	ticker := time.NewTicker(tasksStreamInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			tasks, total, err := h.listTasks(c.Request.Context(), page, pageSize)
			if err != nil {
				c.SSEvent("error", gin.H{"message": err.Error()})
				flusher.Flush()
				return
			}
			sendTasks(tasks, total)
		case <-heartbeat.C:
			writeStreamHeartbeat(c, flusher)
		}
	}
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

func setStreamHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
}

func writeStreamHeartbeat(c *gin.Context, flusher http.Flusher) {
	_, _ = c.Writer.Write([]byte(": keepalive\n\n"))
	flusher.Flush()
}

func taskTerminal(task *model.DiagnosisTask) bool {
	return task != nil && (task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailed)
}

func taskStreamVersion(task *model.DiagnosisTask) string {
	if task == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(strconv.FormatUint(uint64(task.ID), 10))
	b.WriteByte('|')
	b.WriteString(task.Status)
	b.WriteByte('|')
	b.WriteString(task.UpdatedAt.UTC().Format(time.RFC3339Nano))
	b.WriteByte('|')
	b.WriteString(task.FaultType)
	b.WriteByte('|')
	b.WriteString(task.RootCauseSummary)
	b.WriteByte('|')
	b.WriteString(strconv.FormatFloat(task.ConfidenceScore, 'f', 4, 64))
	if task.Report != nil {
		b.WriteByte('|')
		b.WriteString(task.Report.CreatedAt.UTC().Format(time.RFC3339Nano))
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(len(task.Report.AgentTimeline)))
		if len(task.Report.AgentTimeline) > 0 {
			last := task.Report.AgentTimeline[len(task.Report.AgentTimeline)-1]
			b.WriteByte('|')
			b.WriteString(strconv.FormatUint(uint64(last.ID), 10))
			b.WriteByte('|')
			b.WriteString(last.CreatedAt.UTC().Format(time.RFC3339Nano))
		}
	}
	return b.String()
}

func tasksStreamVersion(tasks []model.DiagnosisTask, total int64) string {
	var b strings.Builder
	b.WriteString(strconv.FormatInt(total, 10))
	for _, task := range tasks {
		b.WriteByte('|')
		b.WriteString(strconv.FormatUint(uint64(task.ID), 10))
		b.WriteByte(':')
		b.WriteString(task.Status)
		b.WriteByte(':')
		b.WriteString(task.UpdatedAt.UTC().Format(time.RFC3339Nano))
		b.WriteByte(':')
		b.WriteString(task.FaultType)
		b.WriteByte(':')
		b.WriteString(task.RootCauseSummary)
	}
	return b.String()
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
