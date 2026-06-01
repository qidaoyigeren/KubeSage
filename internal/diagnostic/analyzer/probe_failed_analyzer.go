package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
)

type ProbeFailedAnalyzer struct{}

// NewProbeFailedAnalyzer creates the analyzer for readiness/liveness failures.
func NewProbeFailedAnalyzer() *ProbeFailedAnalyzer {
	return &ProbeFailedAnalyzer{}
}

// Name returns the analyzer identifier used in reports.
func (a *ProbeFailedAnalyzer) Name() string { return "probe_failed" }

func (a *ProbeFailedAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "ProbeFailed",
		Priority:         60,
		MatchSignals:     []string{"event.reason=Unhealthy", "readiness probe failed", "liveness probe failed", "startup probe failed"},
		RequiredEvidence: []string{"k8s_event", "k8s_pod_status", "k8s_log", "k8s_topology", "prometheus"},
	}
}

// Match decides whether pod events contain failed readiness/liveness probes.
func (a *ProbeFailedAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	for _, event := range ctx.Events {
		message := strings.ToLower(event.Message)
		if event.Reason == "Unhealthy" && failedProbeMessage(message) {
			return true
		}
	}
	return false
}

func failedProbeMessage(message string) bool {
	return strings.Contains(message, "readiness probe failed") ||
		strings.Contains(message, "liveness probe failed") ||
		strings.Contains(message, "startup probe failed")
}

// Analyze collects probe config, event, and health-related log evidence.
func (a *ProbeFailedAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	summary := "Pod Events 出现探针失败，可能是健康检查配置不合理或应用健康端点异常。"
	for _, event := range ctx.Events {
		if event.Reason == "Unhealthy" {
			evidences = append(evidences, eventEvidence(event, "warning"))
			if strings.Contains(strings.ToLower(event.Message), "liveness probe failed") {
				summary = "Liveness probe 失败可能导致 kubelet 重启容器。"
			}
			if strings.Contains(strings.ToLower(event.Message), "readiness probe failed") {
				summary = "Readiness probe 失败会使 Pod 暂时不接收流量。"
			}
		}
	}
	for _, c := range ctx.Pod.Spec.Containers {
		evidences = append(evidences, probeEvidence(c.Name, "readinessProbe", c.ReadinessProbe))
		evidences = append(evidences, probeEvidence(c.Name, "livenessProbe", c.LivenessProbe))
		evidences = append(evidences, probeEvidence(c.Name, "startupProbe", c.StartupProbe))
	}
	for _, logs := range ctx.Logs {
		if containsAny(logs.Current+"\n"+logs.Previous+"\n"+logs.Loki, probeFailedLogKeywords) {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_log",
				Title:      "Health check related logs",
				Content:    trimLog(logs.ContainerName, logs.Previous, logs.Current, logs.Loki),
				Severity:   "info",
				Raw:        logs,
				Timestamp:  time.Now(),
			})
		}
	}
	evidences = append(evidences, keyLogEvidences(ctx.Logs, "Key Probe Failed log fragments", probeFailedLogKeywords, "warning")...)

	confidence := probeConfidence(ctx, evidences)
	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "ProbeFailed",
		RootCauseSummary: summary,
		ConfidenceScore:  confidence,
		Evidences:        evidences,
		ImpactAnalysis:   "Readiness 失败会摘除流量，Liveness 失败会触发重启，可能造成流量抖动。",
		SuggestedActions: []string{"确认 probe path、port、scheme 是否与应用实际监听一致。", "慢启动服务可适当增加 initialDelaySeconds 或配置 startupProbe。", "偶发慢请求可评估提高 timeoutSeconds 或 failureThreshold。"},
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

// probeConfidence weighs unhealthy events, probe specs, and matching logs.
func probeConfidence(ctx *diagnostic.DiagnosticContext, evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.72
	if hasEvidence(evidences, "k8s_event", "Unhealthy") {
		score += 0.07
	}
	if hasEvidence(evidences, "k8s_pod_status", "Probe") {
		score += 0.03
	}
	if hasEvidence(evidences, "k8s_key_log", "") {
		score += 0.03
	}
	if ctx != nil && ctx.Pod != nil {
		for _, container := range ctx.Pod.Spec.Containers {
			if container.ReadinessProbe != nil || container.LivenessProbe != nil {
				score += 0.02
				break
			}
		}
	}
	return clampConfidence(score)
}

// probeEvidence converts one probe config into diagnostic evidence.
func probeEvidence(containerName, probeName string, probe interface{}) diagnostic.EvidenceRecord {
	severity := "info"
	content := fmt.Sprintf("container=%s %s=%v", containerName, probeName, probe)
	if probe == nil {
		severity = "debug"
		content = fmt.Sprintf("container=%s %s is not configured", containerName, probeName)
	}
	return diagnostic.EvidenceRecord{
		SourceType: "k8s_pod_status",
		Title:      probeName,
		Content:    content,
		Severity:   severity,
		Raw:        probe,
		Timestamp:  time.Now(),
	}
}
