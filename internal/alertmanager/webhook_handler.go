package alertmanager

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type WebhookHandler struct {
	service *service.DiagnosisService
	log     *zap.Logger
}

type WebhookPayload struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"`
	Alerts            []Alert           `json:"alerts"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type WebhookTaskResponse struct {
	TaskID        uint   `json:"task_id,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	PodName       string `json:"pod_name,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	AlertName     string `json:"alertname,omitempty"`
	Severity      string `json:"severity,omitempty"`
	FaultType     string `json:"fault_type,omitempty"`
	Deduped       bool   `json:"deduped"`
	Skipped       bool   `json:"skipped"`
	Message       string `json:"message,omitempty"`
}

var podDescriptionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bpod(?:\s+|[=:/"']+)[a-z0-9](?:[-a-z0-9]*[a-z0-9])?/([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)`),
	regexp.MustCompile(`(?i)\bpod(?:\s+|[=:/"']+)([a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*)`),
	regexp.MustCompile(`(?i)\bpod_name[=:"'\s]+([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)`),
}

// NewWebhookHandler creates the Alertmanager webhook HTTP handler.
func NewWebhookHandler(service *service.DiagnosisService, log *zap.Logger) *WebhookHandler {
	return &WebhookHandler{service: service, log: log}
}

// Handle receives Alertmanager webhook payloads and starts diagnosis tasks for
// firing alerts that can be mapped to a Kubernetes pod.
func (h *WebhookHandler) Handle(c *gin.Context) {
	var payload WebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	responses := make([]WebhookTaskResponse, 0, len(payload.Alerts))
	for _, alert := range payload.Alerts {
		response := h.handleAlert(c, payload, alert)
		responses = append(responses, response)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": gin.H{"tasks": responses}})
}

// handleAlert parses one alert and either starts/reuses a diagnosis task or
// returns a skipped response with the reason.
func (h *WebhookHandler) handleAlert(c *gin.Context, payload WebhookPayload, alert Alert) WebhookTaskResponse {
	if strings.EqualFold(alert.Status, "resolved") {
		return WebhookTaskResponse{Skipped: true, Message: "resolved alert ignored"}
	}
	parsed := parseAlert(payload, alert)
	response := WebhookTaskResponse{
		Namespace:     parsed.Namespace,
		PodName:       parsed.PodName,
		ContainerName: parsed.ContainerName,
		AlertName:     parsed.AlertName,
		Severity:      parsed.Severity,
		FaultType:     parsed.FaultType,
	}
	if parsed.Namespace == "" || parsed.PodName == "" || parsed.AlertName == "" {
		response.Skipped = true
		response.Message = "namespace, pod, and alertname are required"
		h.log.Warn("skip alert diagnosis",
			zap.String("namespace", parsed.Namespace),
			zap.String("pod", parsed.PodName),
			zap.String("alertname", parsed.AlertName),
			zap.String("description", parsed.Description),
		)
		return response
	}
	result, err := h.service.TriggerFromAlert(c.Request.Context(), service.AlertDiagnosisRequest{
		Namespace:     parsed.Namespace,
		PodName:       parsed.PodName,
		ContainerName: parsed.ContainerName,
		AlertName:     parsed.AlertName,
		Severity:      parsed.Severity,
		FaultType:     parsed.FaultType,
		AlertTime:     parsed.AlertTime,
	})
	if err != nil {
		response.Skipped = true
		response.Message = err.Error()
		h.log.Warn("skip alert diagnosis", zap.String("alertname", parsed.AlertName), zap.Error(err))
		return response
	}
	response.TaskID = result.Task.ID
	response.Deduped = result.Deduped
	return response
}

type parsedAlert struct {
	Namespace     string
	PodName       string
	ContainerName string
	AlertName     string
	Severity      string
	FaultType     string
	Description   string
	AlertTime     *time.Time
}

// parseAlert extracts Kubernetes diagnosis fields from Alertmanager labels and
// annotations, using common labels as a fallback.
func parseAlert(payload WebhookPayload, alert Alert) parsedAlert {
	labels := mergedLabels(payload.CommonLabels, alert.Labels)
	annotations := mergedLabels(payload.CommonAnnotations, alert.Annotations)
	description := annotations["description"]
	podName := firstNonEmpty(
		labels["pod"],
		labels["pod_name"],
		labels["kubernetes_pod_name"],
		parsePodNameFromDescription(description),
	)
	var alertTime *time.Time
	if !alert.StartsAt.IsZero() {
		alertTime = &alert.StartsAt
	}
	alertName := labels["alertname"]
	return parsedAlert{
		Namespace: firstNonEmpty(labels["namespace"], labels["kubernetes_namespace"]),
		PodName:   podName,
		ContainerName: firstNonEmpty(
			labels["container"],
			labels["container_name"],
			labels["kubernetes_container_name"],
		),
		AlertName:   alertName,
		Severity:    labels["severity"],
		FaultType:   faultTypeFromAlertName(alertName),
		Description: description,
		AlertTime:   alertTime,
	}
}

// mergedLabels overlays alert-specific values on top of common values.
func mergedLabels(common, specific map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range common {
		result[key] = value
	}
	for key, value := range specific {
		result[key] = value
	}
	return result
}

// faultTypeFromAlertName maps common Kubernetes alert names to KubeSage fault
// types.
func faultTypeFromAlertName(alertName string) string {
	switch alertName {
	case "KubePodCrashLooping":
		return "CrashLoopBackOff"
	case "KubePodOOMKilled":
		return "OOMKilled"
	case "KubePodNotReady":
		return "ProbeFailed/Pending"
	case "KubePodPending":
		return "Pending"
	default:
		return ""
	}
}

// parsePodNameFromDescription tries to recover the pod name from the human
// readable Alertmanager annotation.
func parsePodNameFromDescription(description string) string {
	for _, pattern := range podDescriptionPatterns {
		matches := pattern.FindStringSubmatch(description)
		if len(matches) > 1 {
			return strings.Trim(matches[1], "`'\".,;:()[]{}")
		}
	}
	return ""
}

// firstNonEmpty returns the first non-empty string from a list of candidates.
func firstNonEmpty(items ...string) string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	return ""
}
