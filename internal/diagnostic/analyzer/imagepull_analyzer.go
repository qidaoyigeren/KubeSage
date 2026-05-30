package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type ImagePullBackOffAnalyzer struct{}

func NewImagePullBackOffAnalyzer() *ImagePullBackOffAnalyzer {
	return &ImagePullBackOffAnalyzer{}
}

func (a *ImagePullBackOffAnalyzer) Name() string { return "image_pull_backoff" }

func (a *ImagePullBackOffAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "ImagePullBackOff",
		Priority:         85,
		MatchSignals:     []string{"waiting.reason=ImagePullBackOff", "waiting.reason=ErrImagePull", "event.reason=Failed"},
		RequiredEvidence: []string{"k8s_pod_status", "k8s_event", "image_pull_secret"},
	}
}

func (a *ImagePullBackOffAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx == nil || ctx.Pod == nil {
		return false
	}
	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if imagePullWaiting(status.State.Waiting) {
			return true
		}
	}
	for _, status := range ctx.Pod.Status.InitContainerStatuses {
		if imagePullWaiting(status.State.Waiting) {
			return true
		}
	}
	for _, event := range ctx.Events {
		text := strings.ToLower(event.Reason + " " + event.Message)
		if strings.Contains(text, "pull") && (strings.Contains(text, "image") || strings.Contains(text, "registry")) {
			return true
		}
	}
	return false
}

func (a *ImagePullBackOffAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	summary := "Container image cannot be pulled; likely image name/tag, registry access, or imagePullSecret problem."
	actions := []string{
		"Verify image repository, tag, registry reachability, and imagePullPolicy.",
		"Check imagePullSecrets and registry credentials in the namespace.",
	}

	for _, container := range append(ctx.Pod.Spec.InitContainers, ctx.Pod.Spec.Containers...) {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container image configuration",
			Content:    fmt.Sprintf("container=%s image=%s imagePullPolicy=%s", container.Name, container.Image, container.ImagePullPolicy),
			Severity:   "warning",
			Raw:        container,
			Timestamp:  time.Now(),
		})
	}
	for _, secret := range ctx.Pod.Spec.ImagePullSecrets {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "image_pull_secret",
			Title:      "Image pull secret reference",
			Content:    fmt.Sprintf("imagePullSecret=%s", secret.Name),
			Severity:   "info",
			Raw:        secret,
			Timestamp:  time.Now(),
		})
	}
	for _, event := range ctx.Events {
		text := strings.ToLower(event.Reason + " " + event.Message)
		if strings.Contains(text, "pull") || strings.Contains(text, "image") || strings.Contains(text, "registry") {
			evidences = append(evidences, eventEvidence(event, "critical"))
			if strings.Contains(text, "not found") || strings.Contains(text, "manifest unknown") {
				summary = "Image pull failed because the image or tag was not found in the registry."
			}
			if strings.Contains(text, "unauthorized") || strings.Contains(text, "denied") || strings.Contains(text, "authentication") {
				summary = "Image pull failed because registry authentication or imagePullSecret is invalid."
			}
		}
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "ImagePullBackOff",
		RootCauseSummary: summary,
		ConfidenceScore:  imagePullConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "Pod cannot start until kubelet pulls every required image successfully.",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

func imagePullWaiting(waiting *corev1.ContainerStateWaiting) bool {
	if waiting == nil {
		return false
	}
	return waiting.Reason == "ImagePullBackOff" || waiting.Reason == "ErrImagePull"
}

func imagePullConfidence(evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.70
	if hasEvidence(evidences, "k8s_event", "") {
		score += 0.12
	}
	if hasEvidence(evidences, "image_pull_secret", "") {
		score += 0.03
	}
	return clampConfidence(score)
}
