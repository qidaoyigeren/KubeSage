package alertmanager

import (
	"net/http"

	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type WebhookHandler struct {
	service *service.DiagnosisService
	log     *zap.Logger
}

type WebhookPayload struct {
	Alerts []Alert `json:"alerts"`
}

type Alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// NewWebhookHandler creates the Alertmanager webhook HTTP handler.
func NewWebhookHandler(service *service.DiagnosisService, log *zap.Logger) *WebhookHandler {
	return &WebhookHandler{service: service, log: log}
}

// Handle receives Alertmanager webhook payloads and starts diagnosis tasks for
// alerts that can be mapped to a namespace and pod.
func (h *WebhookHandler) Handle(c *gin.Context) {
	var payload WebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	tasks := []interface{}{}
	for _, alert := range payload.Alerts {
		namespace := firstNonEmpty(alert.Labels["namespace"], alert.Labels["kubernetes_namespace"])
		podName := firstNonEmpty(alert.Labels["pod"], alert.Labels["pod_name"], alert.Labels["kubernetes_pod_name"])
		alertName := alert.Labels["alertname"]
		task, err := h.service.TriggerFromAlert(c.Request.Context(), namespace, podName, alertName)
		if err != nil {
			h.log.Warn("skip alert diagnosis", zap.String("alertname", alertName), zap.Error(err))
			continue
		}
		tasks = append(tasks, task)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"tasks": tasks}})
}

// firstNonEmpty returns the first non-empty string from a list of candidates.
func firstNonEmpty(items ...string) string {
	for _, item := range items {
		if item != "" {
			return item
		}
	}
	return ""
}
