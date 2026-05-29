package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
)

type OOMKilledAnalyzer struct{}

func NewOOMKilledAnalyzer() *OOMKilledAnalyzer {
	return &OOMKilledAnalyzer{}
}

func (a *OOMKilledAnalyzer) Name() string { return "oomkilled" }

func (a *OOMKilledAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Pod == nil {
		return false
	}
	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if status.LastTerminationState.Terminated != nil {
			terminated := status.LastTerminationState.Terminated
			if terminated.Reason == "OOMKilled" || terminated.ExitCode == 137 {
				return true
			}
		}
	}
	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Message), "oomkilled") {
			return true
		}
	}
	return false
}

func (a *OOMKilledAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	for _, c := range ctx.Pod.Spec.Containers {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container memory requests and limits",
			Content:    fmt.Sprintf("container=%s memoryRequest=%s memoryLimit=%s", c.Name, c.Resources.Requests.Memory().String(), c.Resources.Limits.Memory().String()),
			Severity:   "warning",
			Raw:        c.Resources,
			Timestamp:  time.Now(),
		})
	}
	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if status.LastTerminationState.Terminated != nil {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_pod_status",
				Title:      "OOM termination evidence",
				Content:    fmt.Sprintf("container=%s restartCount=%d reason=%s exitCode=%d finishedAt=%s", status.Name, status.RestartCount, status.LastTerminationState.Terminated.Reason, status.LastTerminationState.Terminated.ExitCode, status.LastTerminationState.Terminated.FinishedAt.Time.Format(time.RFC3339)),
				Severity:   "critical",
				Raw:        status,
				Timestamp:  time.Now(),
			})
		}
	}
	for _, logs := range ctx.Logs {
		if logs.Previous != "" {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_log",
				Title:      "Previous logs before OOMKilled",
				Content:    trimLog(logs.ContainerName, logs.Previous, logs.Current),
				Severity:   "info",
				Raw:        logs,
				Timestamp:  time.Now(),
			})
		}
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "OOMKilled",
		RootCauseSummary: "容器最近一次退出由 OOMKilled 或退出码 137 触发，更倾向内存 limit 不足或应用内存持续增长。",
		ConfidenceScore:  0.9,
		Evidences:        evidences,
		ImpactAnalysis:   "容器被 kubelet 杀死并重启，可能造成请求中断、队列堆积或批处理失败。",
		SuggestedActions: []string{"检查内存泄漏、大对象缓存、批量查询、JVM/Go heap 参数。", "接入 Prometheus 后查看容器内存曲线是否持续上涨。", "临时提高 memory limit 可缓解风险，但需要标记为临时方案并继续根因排查。"},
		RiskLevel:        "high",
		NeedHumanConfirm: true,
	}, nil
}
