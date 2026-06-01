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
		if imagePullFailureEvent(event) {
			return true
		}
	}
	return false
}

func (a *ImagePullBackOffAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	summary := "容器镜像无法拉取，常见原因是镜像名或标签错误、镜像仓库不可访问，或 imagePullSecret 配置异常。"
	actions := []string{
		"确认镜像仓库、标签、网络可达性和 imagePullPolicy 是否正确。",
		"检查命名空间内的 imagePullSecrets 和镜像仓库凭据是否有效。",
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
		if imagePullFailureEvent(event) {
			text := strings.ToLower(event.Reason + " " + event.Message)
			evidences = append(evidences, eventEvidence(event, "critical"))
			if strings.Contains(text, "not found") || strings.Contains(text, "manifest unknown") {
				summary = "镜像拉取失败，仓库中可能不存在该镜像或标签。"
			}
			if strings.Contains(text, "unauthorized") || strings.Contains(text, "denied") || strings.Contains(text, "authentication") {
				summary = "镜像拉取失败，镜像仓库认证或 imagePullSecret 可能无效。"
			}
		}
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "ImagePullBackOff",
		RootCauseSummary: summary,
		ConfidenceScore:  imagePullConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "在 kubelet 成功拉取所有必需镜像之前，Pod 无法进入正常运行状态。",
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

func imagePullFailureEvent(event corev1.Event) bool {
	text := strings.ToLower(event.Reason + " " + event.Message)
	if strings.Contains(text, "imagepullbackoff") || strings.Contains(text, "errimagepull") {
		return true
	}
	for _, phrase := range []string{
		"failed to pull image",
		"manifest unknown",
		"unauthorized",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
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
