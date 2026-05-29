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

// Match decides whether pod events contain failed readiness/liveness probes.
func (a *ProbeFailedAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	for _, event := range ctx.Events {
		message := strings.ToLower(event.Message)
		if event.Reason == "Unhealthy" && (strings.Contains(message, "readiness probe failed") || strings.Contains(message, "liveness probe failed")) {
			return true
		}
	}
	return false
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
		if containsAny(logs.Current+"\n"+logs.Previous, probeFailedLogKeywords) {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_log",
				Title:      "Health check related logs",
				Content:    trimLog(logs.ContainerName, logs.Previous, logs.Current),
				Severity:   "info",
				Raw:        logs,
				Timestamp:  time.Now(),
			})
		}
	}
	evidences = append(evidences, keyLogEvidences(ctx.Logs, "Key Probe Failed log fragments", probeFailedLogKeywords, "warning")...)

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "ProbeFailed",
		RootCauseSummary: summary,
		ConfidenceScore:  0.84,
		Evidences:        evidences,
		ImpactAnalysis:   "Readiness 失败会摘除流量，Liveness 失败会触发重启，可能造成流量抖动。",
		SuggestedActions: []string{"确认 probe path、port、scheme 是否与应用实际监听一致。", "慢启动服务可适当增加 initialDelaySeconds 或配置 startupProbe。", "偶发慢请求可评估提高 timeoutSeconds 或 failureThreshold。"},
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
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
