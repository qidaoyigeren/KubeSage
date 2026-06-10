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
		if oomLikeTermination(status.LastTerminationState.Terminated) {
			continue
		}
		if status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
		if status.RestartCount > 0 && status.LastTerminationState.Terminated != nil {
			return true
		}
	}
	return false
}

func oomLikeTermination(terminated *corev1.ContainerStateTerminated) bool {
	if terminated == nil {
		return false
	}
	return terminated.ExitCode == 137 || strings.EqualFold(terminated.Reason, "OOMKilled")
}

// Analyze collects status, event, and log evidence for CrashLoopBackOff.
func (a *CrashLoopBackOffAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	actions := []string{"查看 previous logs 中的启动错误栈和最近发布变更。", "确认启动命令、配置文件、环境变量、依赖服务地址和端口是否正确。"}
	summary := "CrashLoopBackOff: 容器反复启动失败，命中 CrashLoopBackOff 或最近一次 terminated 状态。"
	// Base 0.75 — generic CrashLoop match without exit code or log evidence
	// is less certain than a specific sub-pattern. Per K8s docs, CrashLoopBackOff
	// indicates exponential backoff (10s→20s→40s→...→5min cap) after repeated
	// container failures; the specific root cause requires exit code and log analysis.
	confidence := 0.75

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
			summary = fmt.Sprintf("CrashLoopBackOff restartCount=%d exitCode 1: 容器退出码为 1，更倾向应用启动失败、配置错误或依赖不可用。", status.RestartCount)
			actions = append(actions, "优先检查应用启动参数、配置加载、环境变量和依赖连接错误。")
		}
		if exitCode == 2 {
			summary = fmt.Sprintf("CrashLoopBackOff restartCount=%d exitCode 2: 容器退出码为 2，可能是 panic 或 fatal 错误。", status.RestartCount)
			actions = append(actions, "检查应用日志中的 panic/fatal 堆栈信息。")
		}
		if exitCode == 126 {
			summary = fmt.Sprintf("CrashLoopBackOff restartCount=%d exitCode 126 permission denied: 容器退出码为 126，可能是文件权限问题。", status.RestartCount)
			actions = append(actions, "检查容器镜像中的文件权限和 entrypoint 设置。")
		}
		if exitCode == 137 || strings.EqualFold(reason, "OOMKilled") {
			summary = fmt.Sprintf("CrashLoopBackOff restartCount=%d exitCode 137: 容器退出码为 137 或 reason 为 OOMKilled，CrashLoop 可能由内存不足触发。", status.RestartCount)
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

	// Detect specific failure patterns from events and logs to enrich summary.
	crashText := ""
	for _, event := range ctx.Events {
		crashText += " " + event.Message
	}
	for _, logs := range ctx.Logs {
		crashText += " " + logs.Current + " " + logs.Previous
	}
	crashText = strings.ToLower(crashText)
	switch {
	case strings.Contains(crashText, "config") && (strings.Contains(crashText, "error") || strings.Contains(crashText, "parse") || strings.Contains(crashText, "invalid") || strings.Contains(crashText, "missing")):
		summary = "CrashLoopBackOff startup failure config parse error: 应用配置解析错误。"
	case strings.Contains(crashText, "permission denied"):
		summary = "CrashLoopBackOff startup failure permission denied exitCode 126: 应用启动时权限被拒绝。"
	case strings.Contains(crashText, "connection refused"):
		summary = "CrashLoopBackOff startup failure dependency connection refused: 应用启动时依赖服务连接被拒绝。"
	case strings.Contains(crashText, "panic") || strings.Contains(crashText, "fatal"):
		summary = "CrashLoopBackOff startup failure panic fatal exitCode 2: 应用启动时发生 panic 或 fatal 错误。"
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
// suspicious log evidence agree. Per K8s docs:
//   - CrashLoopBackOff means container is in exponential backoff (10s, 20s, 40s... capped at 5min)
//   - Exit code 137 = SIGKILL (OOM or external kill) — strong signal (+0.05)
//   - Exit code 143 = SIGTERM (graceful termination) — moderate signal (+0.03)
//   - Exit code 1 = application error — common but less specific (+0.04)
//   - Exit code 2 = configuration/panic error — specific signal (+0.04)
//   - High restart count confirms recurring crash pattern (+0.03)
//   - postStart hook failures also cause CrashLoopBackOff (+0.03)
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
	// Exit code specific confidence boost — per K8s docs, different exit codes
	// indicate different failure modes with varying diagnostic certainty.
	if hasEvidenceContent(evidences, "exitcode=137") {
		// SIGKILL — OOM or external kill, very strong signal.
		score += 0.05
	} else if hasEvidenceContent(evidences, "exitcode=143") {
		// SIGTERM — graceful termination requested, moderate signal.
		score += 0.03
	} else if hasEvidenceContent(evidences, "exitcode=1") {
		// Application error — common exit code, confirms crash.
		score += 0.04
	} else if hasEvidenceContent(evidences, "exitcode=2") {
		// Configuration or panic error — specific signal.
		score += 0.04
	}
	// High restart count confirms recurring crash pattern.
	if hasHighRestartCount(evidences, 5) {
		score += 0.03
	}
	// postStart hook failure detection — per K8s docs, postStart hooks that
	// fail cause container restart, leading to CrashLoopBackOff.
	if ctx != nil && ctx.Pod != nil {
		for _, c := range ctx.Pod.Spec.Containers {
			if c.Lifecycle != nil && c.Lifecycle.PostStart != nil {
				score += 0.03
				break
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
