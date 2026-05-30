package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type CrashLoopBackOffAnalyzer struct{}

// NewCrashLoopBackOffAnalyzer creates the analyzer for CrashLoopBackOff cases.
func NewCrashLoopBackOffAnalyzer() *CrashLoopBackOffAnalyzer {
	return &CrashLoopBackOffAnalyzer{}
}

// Name returns the analyzer identifier used in reports.
func (a *CrashLoopBackOffAnalyzer) Name() string { return "crashloopbackoff" }

func (a *CrashLoopBackOffAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "CrashLoopBackOff",
		Priority:         90,
		MatchSignals:     []string{"waiting.reason=CrashLoopBackOff", "restartCount>0", "lastState.terminated"},
		RequiredEvidence: []string{"k8s_pod_status", "k8s_event", "k8s_log", "k8s_topology", "correlation_evidence"},
	}
}

// Match decides whether pod status indicates repeated container restarts.
func (a *CrashLoopBackOffAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Pod == nil {
		return false
	}
	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
		if status.RestartCount > 0 && status.LastTerminationState.Terminated != nil {
			return true
		}
	}
	return false
}

// Analyze collects status, event, and log evidence for CrashLoopBackOff.
func (a *CrashLoopBackOffAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	actions := []string{"查看 previous logs 中的启动错误栈和最近发布变更。", "确认启动命令、配置文件、环境变量、依赖服务地址和端口是否正确。"}
	summary := "容器反复启动失败，命中 CrashLoopBackOff 或最近一次 terminated 状态。"
	confidence := 0.82

	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if status.RestartCount == 0 && status.LastTerminationState.Terminated == nil {
			continue
		}
		exitCode := int32(0)
		reason := ""
		if status.LastTerminationState.Terminated != nil {
			exitCode = status.LastTerminationState.Terminated.ExitCode
			reason = status.LastTerminationState.Terminated.Reason
		}
		content := fmt.Sprintf("container=%s restartCount=%d lastReason=%s exitCode=%d", status.Name, status.RestartCount, reason, exitCode)
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container restart evidence",
			Content:    content,
			Severity:   "warning",
			Raw:        status,
			Timestamp:  time.Now(),
		})
		if exitCode == 1 {
			summary = "容器退出码为 1，更倾向应用启动失败、配置错误或依赖不可用。"
			actions = append(actions, "优先检查应用启动参数、配置加载、环境变量和依赖连接错误。")
		}
		if exitCode == 137 || strings.EqualFold(reason, "OOMKilled") {
			summary = "容器退出码为 137 或 reason 为 OOMKilled，CrashLoop 可能由内存不足触发。"
			actions = append(actions, "转入 OOMKilled 排查：检查 memory limit、内存曲线和堆内存增长。")
			confidence = 0.9
		}
	}

	for _, container := range ctx.Pod.Spec.Containers {
		envNames := make([]string, 0, len(container.Env))
		for _, env := range container.Env {
			envNames = append(envNames, env.Name)
		}
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container command and environment names",
			Content:    fmt.Sprintf("container=%s command=%v args=%v envNames=%v", container.Name, container.Command, container.Args, envNames),
			Severity:   "info",
			Raw:        container,
			Timestamp:  time.Now(),
		})
	}

	for _, event := range ctx.Events {
		if containsAny(event.Reason+" "+event.Message, []string{"BackOff", "Failed", "Error"}) {
			evidences = append(evidences, eventEvidence(event, "warning"))
		}
	}

	for _, logs := range ctx.Logs {
		text := strings.ToLower(logs.Previous + "\n" + logs.Current + "\n" + logs.Loki)
		if containsAny(text, crashLoopLogKeywords) {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_log",
				Title:      "Suspicious startup log keywords",
				Content:    trimLog(logs.ContainerName, logs.Previous, logs.Current, logs.Loki),
				Severity:   "warning",
				Raw:        logs,
				Timestamp:  time.Now(),
			})
			actions = append(actions, "日志出现 config/env/missing/refused/timeout 等关键词，请核对配置、环境变量和下游依赖。")
		}
	}

	evidences = append(evidences, keyLogEvidences(ctx.Logs, "Key CrashLoopBackOff log fragments", crashLoopLogKeywords, "warning")...)
	confidence = crashLoopConfidence(ctx, evidences, confidence)

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "CrashLoopBackOff",
		RootCauseSummary: summary,
		ConfidenceScore:  confidence,
		Evidences:        evidences,
		ImpactAnalysis:   "该 Pod 可能无法稳定提供服务；如果 Deployment 其他副本不足，可能造成业务不可用。",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

// crashLoopConfidence increases confidence when restart status, exit code, and
// suspicious log evidence agree.
func crashLoopConfidence(ctx *diagnostic.DiagnosticContext, evidences []diagnostic.EvidenceRecord, base float64) float64 {
	score := base
	if hasEvidence(evidences, "k8s_pod_status", "restart") {
		score += 0.04
	}
	if hasEvidence(evidences, "k8s_key_log", "") {
		score += 0.03
	}
	if ctx != nil && ctx.Pod != nil {
		for _, status := range ctx.Pod.Status.ContainerStatuses {
			if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.FinishedAt.IsZero() == false {
				score += 0.02
				break
			}
		}
	}
	// Boost confidence when metric trends show increasing restarts.
	if ctx != nil {
		for _, trend := range ctx.MetricTrends {
			if strings.Contains(strings.ToLower(trend.Metric), "restart") && trend.Classification == "progressive_growth" {
				score += 0.05
			}
		}
	}
	return clampConfidence(score)
}

// eventEvidence converts one Kubernetes Event into a diagnostic evidence row.
func eventEvidence(event corev1.Event, severity string) diagnostic.EvidenceRecord {
	return diagnostic.EvidenceRecord{
		SourceType: "k8s_event",
		Title:      event.Reason,
		Content:    event.Message,
		Severity:   severity,
		Raw:        event,
		Timestamp:  event.LastTimestamp.Time,
	}
}

// containsAny reports whether text contains any keyword, case-insensitively.
func containsAny(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// trimLog chooses previous/current/Loki logs in that order and caps stored text.
func trimLog(containerName, previous, current string, extras ...string) string {
	text := previous
	if text == "" {
		text = current
	}
	for _, extra := range extras {
		if text == "" {
			text = extra
		}
	}
	if len(text) > 4000 {
		text = text[len(text)-4000:]
	}
	return fmt.Sprintf("container=%s\n%s", containerName, text)
}
